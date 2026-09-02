package telegram

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/actor"
	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/store"
)

type Bot struct {
	api          *tgbotapi.BotAPI
	sender       *Sender
	supervisor   *actor.Supervisor
	store        store.Store
	outbox       chan actor.OutgoingMessage
	roleDelivery *roleDeliveryTracker
	boards       *boardTracker
	httpServer   *http.Server
}

func NewBot(token string, st store.Store) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("create bot api: %w", err)
	}

	outbox := make(chan actor.OutgoingMessage, 256)
	b := &Bot{
		api:          api,
		sender:       NewSender(api, 3),
		supervisor:   actor.NewSupervisor(outbox),
		store:        st,
		outbox:       outbox,
		roleDelivery: newRoleDeliveryTracker(),
		boards:       newBoardTracker(),
	}

	b.sender.OnDMFailure = b.handleDMFailure

	go b.processOutbox()
	b.recoverGames()
	return b, nil
}

// handleDMFailure marks an unreachable player as disconnected. Role DMs do not
// come through here — they carry their own result callback, because a failure
// there means the role must be redealt rather than the player merely muted.
func (b *Bot) handleDMFailure(userID int64, err error) {
	playerID := engine.PlayerID(userID)
	for _, gid := range b.supervisor.ActiveGames() {
		ga := b.supervisor.GetGame(gid)
		if ga == nil {
			continue
		}
		if _, ok := ga.PlayerSnapshot(playerID); !ok {
			continue
		}
		// While roles are being dealt, the delivery tracker owns this
		// decision: the player needs a redeal, not a disconnect mark.
		if ga.Phase() == engine.PhaseRoleAssign {
			continue
		}
		// Dispatched asynchronously: this runs on a sender worker, and the
		// actor's inbox is bounded.
		go ga.Send(engine.PlayerDisconnectedEvent{PlayerID: playerID})
	}
}

// startGame registers the actor and claims the delivery tracker before the
// actor can emit anything. begin has to win that race: a role DM dispatched
// against a forgotten game would be refused, and one dispatched against a
// leftover batch from the previous game in this chat would inherit it.
func (b *Bot) startGame(state *engine.GameState) *actor.GameActor {
	gen := b.roleDelivery.begin(state.ID)
	ga := b.supervisor.StartGame(state)
	b.attachHooks(ga, gen)
	return ga
}

func (b *Bot) attachHooks(ga *actor.GameActor, gen uint64) {
	ga.OnPersist = func(s *engine.GameState) {
		if err := b.store.Save(s); err != nil {
			log.Printf("persist error: %v", err)
		}
	}
	ga.OnFinish = func(id engine.GameID) {
		b.roleDelivery.forget(id, gen)
		b.boards.forget(id)
		if err := b.store.Delete(id); err != nil {
			log.Printf("cleanup error for game %s: %v", id, err)
		}
	}
}

// roleDMFailureEvent decides how the game should answer a role DM that did not
// arrive.
//
// The two outcomes are deliberately different. A player who has blocked the bot
// can never receive a role, so they are removed and the roles are redealt. Any
// other error — an exhausted rate-limit retry, a 5xx, a queue that was full —
// says nothing about whether the player is reachable, and ejecting them would
// mean a busy minute costs the game a player, one per redeal, until the roster
// falls below the minimum. Those are marked unreachable instead: they keep
// their seat, count toward no quorum, and the game starts on time.
func roleDMFailureEvent(pid engine.PlayerID, deal int, err error) engine.Event {
	if isBotBlocked(err) {
		return engine.RoleDeliveryFailedEvent{PlayerID: pid, Deal: deal}
	}
	return engine.PlayerDisconnectedEvent{PlayerID: pid}
}

func (b *Bot) reportRoleDMFailure(ga *actor.GameActor, pid engine.PlayerID, deal int, err error) {
	ev := roleDMFailureEvent(pid, deal, err)
	if _, transient := ev.(engine.PlayerDisconnectedEvent); transient {
		log.Printf("role DM to %d failed without proving them unreachable, marking silent: %v", pid, err)
	}
	ga.Send(ev)
}

// newGameConfig builds the config for a chat's next game from its saved
// settings, falling back to the default if anything is wrong with them.
func (b *Bot) newGameConfig(chatID int64) engine.GameConfig {
	settings, err := b.store.LoadChatSettings(chatID)
	if err != nil {
		log.Printf("settings: failed to load for chat %d, using defaults: %v", chatID, err)
		return engine.DefaultConfig()
	}
	cfg := settings.Config()
	if err := engine.ValidateConfig(cfg); err != nil {
		log.Printf("settings: chat %d has an unplayable config (%v), using defaults", chatID, err)
		return engine.DefaultConfig()
	}
	return cfg
}

// recoverGames reloads persisted game states on boot (§8.7, §8a.8)
func (b *Bot) recoverGames() {
	gameIDs, err := b.store.ListActive()
	if err != nil {
		log.Printf("recovery: failed to list active games: %v", err)
		return
	}
	for _, gid := range gameIDs {
		state, err := b.store.Load(gid)
		if err != nil {
			log.Printf("recovery: failed to load game %s: %v", gid, err)
			continue
		}
		if state.Phase.IsTerminal() {
			_ = b.store.Delete(gid)
			continue
		}
		// A phase this build no longer knows about has no timeout handler, so
		// resuming it would park the game forever.
		if !state.Phase.IsValid() {
			log.Printf("recovery: discarding game %s with unknown phase %q", gid, state.Phase)
			_ = b.store.Delete(gid)
			continue
		}

		ga := b.startGame(state)

		b.sender.SendText(state.ChatID, "🔄 The bot has restarted. Your game has been resumed!")
		// Timers only ever existed in memory, so without this the restored
		// game would sit in its current phase forever.
		ga.Send(engine.ResumeEvent{})
		log.Printf("recovery: resumed game %s (phase: %s, day: %d)", gid, state.Phase, state.DayNumber)
	}
}

func (b *Bot) Start(cfg ListenConfig) error {
	log.Printf("Bot started as @%s", b.api.Self.UserName)
	if cfg.WebhookURL != "" {
		return b.serveWebhook(cfg)
	}
	return b.poll()
}

func (b *Bot) poll() error {
	if _, err := b.api.Request(tgbotapi.DeleteWebhookConfig{}); err != nil {
		log.Printf("deleteWebhook: %v (GetUpdates will fail if a webhook is still registered)", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		b.dispatchUpdate(update)
	}
	return nil
}

func (b *Bot) dispatchUpdate(update tgbotapi.Update) {
	if update.CallbackQuery != nil {
		b.handleCallback(update.CallbackQuery)
		return
	}
	if update.Message == nil {
		return
	}
	if update.Message.LeftChatMember != nil {
		b.handleLeftChatMember(update.Message)
		return
	}
	if update.Message.IsCommand() {
		b.handleCommand(update.Message)
		return
	}
	if update.Message.Chat.IsPrivate() {
		b.handleDMStart(update.Message)
		return
	}
	b.trackDiscussionActivity(update.Message)
}

// Stop drains running games before shutting the sender down, so a redeploy
// leaves every game in a state that recovery can pick up.
func (b *Bot) Stop() {
	if b.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := b.httpServer.Shutdown(ctx); err != nil {
			log.Printf("webhook server shutdown: %v", err)
		}
		cancel()
	}
	b.supervisor.Shutdown(10 * time.Second)
	b.sender.Stop()
}

func gameIDForChat(chatID int64) engine.GameID {
	return engine.GameID(strconv.FormatInt(chatID, 10))
}

// gameFor resolves the game for a group message, replying when there is none.
func (b *Bot) gameFor(msg *tgbotapi.Message, complain bool) *actor.GameActor {
	if msg.Chat.IsPrivate() {
		if complain {
			b.sender.SendDM(msg.Chat.ID, "Use this command in your group chat.")
		}
		return nil
	}
	ga := b.supervisor.GetGame(gameIDForChat(msg.Chat.ID))
	if ga == nil && complain {
		b.sender.SendText(msg.Chat.ID, "No active game. Use /startgame first.")
	}
	return ga
}

func (b *Bot) handleLeftChatMember(msg *tgbotapi.Message) {
	ga := b.supervisor.GetGame(gameIDForChat(msg.Chat.ID))
	if ga == nil {
		return
	}
	ga.Send(engine.PlayerDisconnectedEvent{PlayerID: engine.PlayerID(msg.LeftChatMember.ID)})
}

func (b *Bot) handleDMStart(msg *tgbotapi.Message) {
	playerID := engine.PlayerID(msg.From.ID)
	_ = b.store.SetDMConfirmed(playerID)
	b.sender.SendDM(msg.Chat.ID, "You're all set! You can now join Mafia games in group chats.")
}

func (b *Bot) handleCommand(msg *tgbotapi.Message) {
	switch msg.Command() {
	case "startgame":
		b.cmdStartGame(msg)
	case "join":
		b.cmdJoin(msg)
	case "leave":
		b.cmdLeave(msg)
	case "begin":
		b.cmdBegin(msg)
	case "endgame":
		b.cmdEndGame(msg)
	case "status":
		b.cmdStatus(msg)
	case "myrole":
		b.cmdMyRole(msg)
	case "nominate":
		b.cmdNominate(msg)
	case "second":
		b.cmdSecond(msg)
	case "host":
		b.cmdTransferHost(msg)
	case "kick":
		b.cmdKick(msg)
	case "accuse":
		b.cmdAccuse(msg)
	case "defend":
		b.cmdDefend(msg)
	case "whisper":
		b.cmdWhisper(msg)
	case "reveal":
		b.cmdReveal(msg)
	case "mafia":
		b.cmdMafiaChat(msg)
	case "ghost":
		b.cmdGhostChat(msg)
	case "graveyard":
		b.cmdGraveyard(msg)
	case "stats":
		b.cmdStats(msg)
	case "leaderboard", "top":
		b.cmdLeaderboard(msg)
	case "achievements":
		b.cmdAchievements(msg)
	case "lastgame", "recap":
		b.cmdLastGame(msg)
	case "settings":
		b.cmdSettings(msg)
	case "set":
		b.cmdSet(msg)
	case "roles":
		b.cmdRoles(msg)
	case "help":
		b.cmdHelp(msg)
	case "guide":
		b.cmdGuide(msg)
	case "start":
		if msg.Chat.IsPrivate() {
			b.handleDMStart(msg)
		}
	}
}

func (b *Bot) cmdRoles(msg *tgbotapi.Message) {
	b.sender.SendText(msg.Chat.ID, engine.FormatRoleList())
}

func (b *Bot) cmdReveal(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	ga.Send(engine.RevealEvent{PlayerID: engine.PlayerID(msg.From.ID)})
}

func (b *Bot) cmdGraveyard(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, true)
	if ga == nil {
		return
	}
	b.sender.SendText(msg.Chat.ID, engine.FormatGraveyard(ga.State()))
}

// cmdMafiaChat and cmdGhostChat are DM-only: routing them through the group
// would defeat the point.
func (b *Bot) cmdMafiaChat(msg *tgbotapi.Message) {
	b.relayPrivateChat(msg, "/mafia", func(ga *actor.GameActor, body string) {
		ga.Send(engine.MafiaChatEvent{FromID: engine.PlayerID(msg.From.ID), Message: body})
	})
}

func (b *Bot) cmdGhostChat(msg *tgbotapi.Message) {
	b.relayPrivateChat(msg, "/ghost", func(ga *actor.GameActor, body string) {
		ga.Send(engine.GhostChatEvent{FromID: engine.PlayerID(msg.From.ID), Message: body})
	})
}

// relayPrivateChat finds the game the sender is in and hands the message body
// to the reducer, which decides whether they are allowed to use that channel.
func (b *Bot) relayPrivateChat(msg *tgbotapi.Message, command string, send func(*actor.GameActor, string)) {
	if !msg.Chat.IsPrivate() {
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf("Send %s to me in a private chat, not in the group.", command))
		return
	}
	body := strings.TrimSpace(msg.CommandArguments())
	if body == "" {
		b.sender.SendDM(msg.Chat.ID, fmt.Sprintf("Usage: `%s your message`", command))
		return
	}

	ga := b.gameForPlayer(engine.PlayerID(msg.From.ID))
	if ga == nil {
		b.sender.SendDM(msg.Chat.ID, "You're not in any active game.")
		return
	}
	send(ga, body)
}

// gameForPlayer finds the active game a player belongs to. A player can only
// be in one at a time, since a game is keyed by its group chat and joining
// requires being in that chat.
func (b *Bot) gameForPlayer(playerID engine.PlayerID) *actor.GameActor {
	for _, gameID := range b.supervisor.ActiveGames() {
		ga := b.supervisor.GetGame(gameID)
		if ga == nil {
			continue
		}
		if _, ok := ga.PlayerSnapshot(playerID); ok {
			return ga
		}
	}
	return nil
}

func (b *Bot) cmdStartGame(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		b.sender.SendDM(msg.Chat.ID, "Use this command in a group chat.")
		return
	}

	gameID := gameIDForChat(msg.Chat.ID)
	if existing := b.supervisor.GetGame(gameID); existing != nil {
		b.sender.SendText(msg.Chat.ID, "A game is already in progress. Use /status to check.")
		return
	}

	cfg := b.newGameConfig(msg.Chat.ID)
	hostID := engine.PlayerID(msg.From.ID)
	state := engine.NewGameState(gameID, msg.Chat.ID, hostID, cfg)
	state.Players[hostID] = &engine.Player{
		ID:          hostID,
		Username:    msg.From.UserName,
		DisplayName: msg.From.FirstName,
		Alive:       true,
		JoinedAt:    time.Now(),
	}

	ga := b.startGame(state)

	if waitlist, err := b.store.GetWaitlist(msg.Chat.ID); err == nil && len(waitlist) > 0 {
		b.sender.SendText(msg.Chat.ID, "📢 A new game is starting! Waitlisted players: tap Join below!")
		_ = b.store.ClearWaitlist(msg.Chat.ID)
	}

	// The reducer owns the lobby deadline, its countdown, and the first card.
	ga.Send(engine.GameCreatedEvent{})
}

func (b *Bot) cmdJoin(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}

	playerID := engine.PlayerID(msg.From.ID)
	ga := b.supervisor.GetGame(gameIDForChat(msg.Chat.ID))

	if ga == nil {
		_ = b.store.AddToWaitlist(msg.Chat.ID, playerID)
		b.sender.SendText(msg.Chat.ID, "No active game. You've been added to the waitlist for the next game.")
		return
	}

	if ga.Phase() != engine.PhaseLobby {
		// Cooldown lives in the store so it survives a restart (§8a.3).
		if onCooldown, _ := b.store.HasJoinCooldown(msg.Chat.ID, playerID); onCooldown {
			return
		}
		_ = b.store.SetJoinCooldown(msg.Chat.ID, playerID)
		_ = b.store.AddToWaitlist(msg.Chat.ID, playerID)
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
			"%s, this game already started — you can't join mid-game. You'll be notified when the next one opens.",
			engine.EscapeMD(userLabel(msg.From)),
		))
		return
	}

	if confirmed, _ := b.store.IsDMConfirmed(playerID); !confirmed {
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
			"%s, please DM me and press /start first so I can send you your role.",
			engine.EscapeMD(userLabel(msg.From)),
		))
		return
	}

	ga.Send(engine.JoinEvent{
		PlayerID:    playerID,
		Username:    msg.From.UserName,
		DisplayName: msg.From.FirstName,
		Time:        time.Now(),
	})
}

func userLabel(u *tgbotapi.User) string {
	if u == nil {
		return "player"
	}
	if u.UserName != "" {
		return "@" + u.UserName
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	return fmt.Sprintf("player %d", u.ID)
}

func (b *Bot) cmdLeave(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	ga.Send(engine.LeaveEvent{PlayerID: engine.PlayerID(msg.From.ID)})
}

func (b *Bot) cmdBegin(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, true)
	if ga == nil {
		return
	}
	ga.Send(engine.BeginEvent{
		PlayerID: engine.PlayerID(msg.From.ID),
		IsAdmin:  b.isGroupAdmin(msg.Chat.ID, msg.From.ID),
	})
}

func (b *Bot) cmdNominate(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	targetID := b.extractTargetPlayer(msg, ga)
	if targetID == 0 {
		b.sender.SendText(msg.Chat.ID, "Usage: /nominate @player (or reply to their message)")
		return
	}
	ga.Send(engine.NominateEvent{
		NominatorID: engine.PlayerID(msg.From.ID),
		TargetID:    targetID,
	})
}

func (b *Bot) cmdSecond(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	targetID := b.extractTargetPlayer(msg, ga)
	if targetID == 0 {
		b.sender.SendText(msg.Chat.ID, "Usage: /second @player (the player who was nominated)")
		return
	}
	ga.Send(engine.SecondEvent{
		PlayerID:         engine.PlayerID(msg.From.ID),
		NominationTarget: targetID,
	})
}

func (b *Bot) extractTargetPlayer(msg *tgbotapi.Message, ga *actor.GameActor) engine.PlayerID {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		return engine.PlayerID(msg.ReplyToMessage.From.ID)
	}
	for _, entity := range msg.Entities {
		if entity.Type == "text_mention" && entity.User != nil {
			return engine.PlayerID(entity.User.ID)
		}
		if entity.Type == "mention" {
			username := usernameFromMention(msg.Text, entity.Offset, entity.Length)
			if username == "" {
				continue
			}
			if id := lookupPlayerByUsername(ga.State().Players, username); id != 0 {
				return id
			}
		}
	}
	return 0
}

func (b *Bot) cmdAccuse(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	targetID := b.extractTargetPlayer(msg, ga)
	if targetID == 0 {
		b.sender.SendText(msg.Chat.ID, "Usage: /accuse @player (or reply to their message)")
		return
	}
	ga.Send(engine.AccuseEvent{
		AccuserID: engine.PlayerID(msg.From.ID),
		TargetID:  targetID,
	})
}

func (b *Bot) cmdDefend(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	statement := strings.TrimSpace(msg.CommandArguments())
	if statement == "" {
		b.sender.SendText(msg.Chat.ID, "Usage: /defend I am innocent because...")
		return
	}
	ga.Send(engine.DefendEvent{
		PlayerID:  engine.PlayerID(msg.From.ID),
		Statement: statement,
	})
}

func (b *Bot) cmdWhisper(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}

	targetID := b.extractTargetPlayer(msg, ga)
	body := whisperBody(msg.CommandArguments())

	if targetID == 0 || body == "" {
		b.sender.SendText(msg.Chat.ID, "Usage: /whisper @player your secret message")
		return
	}

	ga.Send(engine.WhisperEvent{
		FromID:  engine.PlayerID(msg.From.ID),
		ToID:    targetID,
		Message: body,
	})
}

func (b *Bot) cmdTransferHost(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	targetID := b.extractTargetPlayer(msg, ga)
	if targetID == 0 {
		b.sender.SendText(msg.Chat.ID, "Usage: /host @player (reply to their message or mention them)")
		return
	}
	ga.Send(engine.HostTransferEvent{
		FromPlayerID: engine.PlayerID(msg.From.ID),
		ToPlayerID:   targetID,
		IsAdmin:      b.isGroupAdmin(msg.Chat.ID, msg.From.ID),
	})
}

func (b *Bot) cmdKick(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	targetID := b.extractTargetPlayer(msg, ga)
	if targetID == 0 {
		b.sender.SendText(msg.Chat.ID, "Usage: /kick @player")
		return
	}
	ga.Send(engine.KickEvent{
		HostID:   engine.PlayerID(msg.From.ID),
		TargetID: targetID,
		IsAdmin:  b.isGroupAdmin(msg.Chat.ID, msg.From.ID),
	})
}

func (b *Bot) cmdEndGame(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, false)
	if ga == nil {
		return
	}
	ga.Send(engine.EndGameEvent{
		PlayerID: engine.PlayerID(msg.From.ID),
		IsAdmin:  b.isGroupAdmin(msg.Chat.ID, msg.From.ID),
	})
}

func (b *Bot) cmdStatus(msg *tgbotapi.Message) {
	ga := b.gameFor(msg, true)
	if ga == nil {
		return
	}

	state := ga.State()

	if state.Phase == engine.PhaseLobby {
		b.sendLobbyCard(state)
		return
	}

	b.sender.SendText(msg.Chat.ID, b.renderStatus(state))
}

// renderStatus is the live game status card.
func (b *Bot) renderStatus(state *engine.GameState) string {
	var aliveList, deadList string
	for _, p := range sortedPlayers(state) {
		if p.Alive {
			status := "🟢"
			if p.Disconnected {
				status = "📵"
			}
			line := fmt.Sprintf("  %s %s", status, p.Label())
			// A revealed Mayor is public knowledge, so the card should say so.
			if p.RoleRevealed && p.ExtraVotes > 0 {
				line += fmt.Sprintf(" — %s (vote ×%d)", engine.RoleBadge(p.Role), p.VoteWeight())
			}
			aliveList += line + "\n"
		} else {
			role := ""
			// Only echo a role that was already revealed publicly.
			if p.RoleRevealed {
				role = fmt.Sprintf(" — %s", engine.RoleBadge(p.Role))
			}
			deadList += fmt.Sprintf("  💀 %s%s\n", p.Label(), role)
		}
	}

	hostLabel := "unknown"
	if host, ok := state.Players[state.HostID]; ok {
		hostLabel = host.Label()
	}

	remaining := ""
	if !state.PhaseDeadline.IsZero() {
		if left := int(time.Until(state.PhaseDeadline).Seconds()); left > 0 {
			total := int(state.Config.PhaseTimeout(state.Phase).Seconds())
			remaining = fmt.Sprintf("⏳ %ds left `%s`\n", left,
				engine.ProgressBar(left, maxOf(total, left), 10))
		}
	}

	text := fmt.Sprintf(
		"📊 *Game Status*\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"📍 Phase: *%s*\n"+
			"📅 Day: %d\n"+
			"👑 Host: %s\n"+
			"%s"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"*Alive (%d):*\n%s"+
			"━━━━━━━━━━━━━━━━━━━━\n",
		state.Phase, state.DayNumber, hostLabel, remaining,
		len(state.AlivePlayers()), aliveList,
	)

	if deadList != "" {
		text += fmt.Sprintf("*Dead (%d):*\n%s━━━━━━━━━━━━━━━━━━━━\n",
			len(state.Players)-len(state.AlivePlayers()), deadList)
	}
	return text
}

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sortedPlayers(state *engine.GameState) []*engine.Player {
	ordered := make([]*engine.Player, 0, len(state.Players))
	for _, p := range state.Players {
		ordered = append(ordered, p)
	}
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j].JoinedAt.Before(ordered[j-1].JoinedAt); j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	return ordered
}

func (b *Bot) cmdMyRole(msg *tgbotapi.Message) {
	if !msg.Chat.IsPrivate() {
		b.sender.SendText(msg.Chat.ID, "Use this command in DM with me!")
		return
	}

	playerID := engine.PlayerID(msg.From.ID)
	for _, gameID := range b.supervisor.ActiveGames() {
		ga := b.supervisor.GetGame(gameID)
		if ga == nil {
			continue
		}
		p, ok := ga.PlayerSnapshot(playerID)
		if !ok {
			continue
		}
		if p.Role == engine.RoleUnassigned {
			b.sender.SendDM(msg.Chat.ID, "Roles haven't been dealt yet — hold tight!")
			return
		}
		status := "Dead"
		if p.Alive {
			status = "Alive"
		}
		b.sender.SendDM(msg.Chat.ID, fmt.Sprintf(
			"Your role: *%s* (%s team)\nStatus: %s",
			p.Role, engine.RoleTeam(p.Role), status,
		))
		return
	}
	b.sender.SendDM(msg.Chat.ID, "You're not in any active game.")
}

func (b *Bot) handleCallback(cq *tgbotapi.CallbackQuery) {
	parts := strings.Split(cq.Data, ":")
	if len(parts) < 2 {
		b.answerCallback(cq, "")
		return
	}

	// The settings and reaction handlers answer the callback themselves, so
	// they can attach a toast explaining what happened.
	switch parts[0] {
	case "lobbycfg":
		b.handleLobbyConfigCallback(cq, parts)
		return
	case "react":
		b.handleReactCallback(cq, parts)
		return
	}

	b.answerCallback(cq, "")

	switch parts[0] {
	case "night":
		b.handleNightCallback(cq, parts)
	case "vote":
		b.handleVoteCallback(cq, parts)
	case "join":
		b.handleJoinCallback(cq, parts)
	case "lobby":
		b.handleLobbyCallback(cq, parts)
	case "info":
		b.handleInfoCallback(cq, parts)
	case "rematch":
		b.handleRematchCallback(cq, parts)
	case "board":
		b.handleBoardCallback(cq, parts)
	case "recap":
		b.handleRecapCallback(cq, parts)
	}
}

// handleReactCallback records a tap on the day mood bar and answers with a
// private toast, so the group chat stays quiet.
func (b *Bot) handleReactCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) < 3 {
		b.answerCallback(cq, "")
		return
	}
	ga := b.supervisor.GetGame(engine.GameID(parts[1]))
	if ga == nil {
		b.answerCallback(cq, "That game is over.")
		return
	}
	playerID := engine.PlayerID(cq.From.ID)
	if _, inGame := ga.PlayerSnapshot(playerID); !inGame {
		b.answerCallback(cq, "Only players in this game can react.")
		return
	}

	ga.Send(engine.ReactEvent{PlayerID: playerID, Emoji: parts[2]})
	b.answerCallback(cq, parts[2]+" noted")
}

// handleInfoCallback serves the read-only panels reachable from a keyboard.
// Format: info:<gameID>:<what>
func (b *Bot) handleInfoCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) < 3 {
		return
	}
	ga := b.supervisor.GetGame(engine.GameID(parts[1]))
	if ga == nil {
		return
	}
	state := ga.State()

	switch parts[2] {
	case "roles":
		b.sender.SendText(state.ChatID, engine.FormatRoleList())
	case "graveyard":
		b.sender.SendText(state.ChatID, engine.FormatGraveyard(state))
	case "status":
		b.sender.SendText(state.ChatID, b.renderStatus(state))
	case "settings":
		b.sender.SendText(state.ChatID, engine.FormatSettingsPanel(state.Config))
	case "rules":
		b.sender.SendText(state.ChatID, engine.FormatSettingsPanel(state.Config))
	}
}

// handleRematchCallback opens a fresh lobby in the same chat, hosted by
// whoever tapped the button.
func (b *Bot) handleRematchCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	gameID := gameIDForChat(chatID)
	if b.supervisor.GetGame(gameID) != nil {
		b.sender.SendText(chatID, "A game is already running here. Use /status to see it.")
		return
	}

	hostID := engine.PlayerID(cq.From.ID)
	// The host must be reachable by DM, or they cannot receive their role.
	if confirmed, _ := b.store.IsDMConfirmed(hostID); !confirmed {
		b.sender.SendText(chatID, fmt.Sprintf(
			"%s, DM me and press /start first, then you can host a rematch.",
			engine.EscapeMD(userLabel(cq.From))))
		return
	}

	state := engine.NewGameState(gameID, chatID, hostID, b.newGameConfig(chatID))
	state.Players[hostID] = &engine.Player{
		ID:          hostID,
		Username:    cq.From.UserName,
		DisplayName: cq.From.FirstName,
		Alive:       true,
		JoinedAt:    time.Now(),
	}

	ga := b.startGame(state)
	b.sender.SendText(chatID, fmt.Sprintf("🔄 *Rematch!* %s is hosting.",
		engine.EscapeMD(userLabel(cq.From))))
	ga.Send(engine.GameCreatedEvent{})
}

func (b *Bot) handleBoardCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	b.sendLeaderboard(chatID, chatID, "Top players here")
}

func (b *Bot) handleRecapCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	b.sendLastGame(chatID)
}

func (b *Bot) handleNightCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	// Format: night:<gameID>:<actionKind>:<targetID>
	if len(parts) < 4 {
		return
	}
	targetID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return
	}

	ga := b.supervisor.GetGame(engine.GameID(parts[1]))
	if ga == nil {
		return
	}

	// Anti-cheat: only living participants may act, and only at night (§8.9)
	playerID := engine.PlayerID(cq.From.ID)
	p, inGame := ga.PlayerSnapshot(playerID)
	if !inGame || !p.CanAct() || ga.Phase() != engine.PhaseNight {
		return
	}

	ga.Send(engine.NightActionEvent{
		Action: engine.NightAction{
			ActorID:     playerID,
			Kind:        parts[2],
			TargetID:    engine.PlayerID(targetID),
			SubmittedAt: time.Now(),
		},
	})
}

func (b *Bot) handleVoteCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	// Format: vote:<gameID>:<targetID>
	if len(parts) < 3 {
		return
	}
	targetID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return
	}

	ga := b.supervisor.GetGame(engine.GameID(parts[1]))
	if ga == nil {
		return
	}

	playerID := engine.PlayerID(cq.From.ID)
	p, inGame := ga.PlayerSnapshot(playerID)
	if !inGame || !p.CanAct() || ga.Phase() != engine.PhaseVoting {
		return
	}

	ga.Send(engine.VoteEvent{
		Vote: engine.Vote{
			VoterID:   playerID,
			TargetID:  engine.PlayerID(targetID),
			Timestamp: time.Now(),
		},
	})
}

func (b *Bot) handleJoinCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	ga := b.supervisor.GetGame(engine.GameID(parts[1]))
	if ga == nil {
		return
	}

	playerID := engine.PlayerID(cq.From.ID)
	if confirmed, _ := b.store.IsDMConfirmed(playerID); !confirmed {
		b.sender.SendText(ga.ChatID(), fmt.Sprintf(
			"%s, please DM me and press /start first so I can send you your role.",
			engine.EscapeMD(userLabel(cq.From)),
		))
		return
	}

	ga.Send(engine.JoinEvent{
		PlayerID:    playerID,
		Username:    cq.From.UserName,
		DisplayName: cq.From.FirstName,
		Time:        time.Now(),
	})
}

func (b *Bot) handleLobbyCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	ga := b.supervisor.GetGame(engine.GameID(parts[1]))
	if ga == nil {
		return
	}
	state := ga.State()
	if state.Phase != engine.PhaseLobby {
		b.sender.SendText(state.ChatID, "This lobby has already started.")
		return
	}
	b.sendLobbyCard(state)
}

func (b *Bot) sendLobbyCard(state *engine.GameState) {
	b.postLobbyCard(state)
}

func (b *Bot) postLobbyCard(state *engine.GameState) {
	text := formatLobbyCardText(state)
	keyboard := buildJoinButton(state.ID)
	gameID := state.ID

	if messageID, ok := b.boards.getLobby(gameID); ok {
		b.sender.EditKeyboardMessage(state.ChatID, messageID, text, keyboard)
		return
	}
	b.sender.SendTrackedKeyboard(state.ChatID, text, keyboard, func(id int) {
		b.boards.setLobby(gameID, id)
	})
}

func formatLobbyCardText(state *engine.GameState) string {
	ordered := sortedPlayers(state)
	names := make([]string, 0, len(ordered))
	for _, p := range ordered {
		names = append(names, p.PlainName())
	}
	hostName := ""
	if host, ok := state.Players[state.HostID]; ok {
		hostName = host.PlainName()
	}

	playerList := ""
	for i, name := range names {
		playerList += fmt.Sprintf("%d. %s\n", i+1, engine.EscapeMD(name))
	}
	if playerList == "" {
		playerList = "_nobody yet_\n"
	}

	minPlayers := state.Config.MinPlayers
	maxPlayers := state.Config.MaxPlayers
	readyStatus := fmt.Sprintf("❌ Need %d more player(s)", minPlayers-len(names))
	if len(names) >= minPlayers {
		readyStatus = "✅ Ready to start! Host: use /begin"
	}

	presetLabel, presetPitch := engine.PresetLabel(state.Config.PresetName)

	return fmt.Sprintf(
		"🎮 *MAFIA — Game Lobby*\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"👑 Host: %s\n"+
			"👥 Players: %d/%d  `%s`\n"+
			"⚙️ Mode: *%s* — _%s_\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"\n%s\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"%s\n"+
			"\n_Tap Join below. Host: ⚙️ Configure, /settings, or `/set night 75` before /begin._",
		engine.EscapeMD(hostName), len(names), maxPlayers,
		engine.ProgressBar(len(names), maxPlayers, 10),
		engine.EscapeMD(presetLabel), engine.EscapeMD(presetPitch),
		playerList, readyStatus,
	)
}

func (b *Bot) renderLobbyCard(chatID int64, gameID engine.GameID, hostName string, players []string, minPlayers, maxPlayers int, preset string) {
	if ga := b.supervisor.GetGame(gameID); ga != nil && ga.Phase() == engine.PhaseLobby {
		b.postLobbyCard(ga.State())
		return
	}
	state := b.lobbyStateFromEffect(chatID, gameID, hostName, players, minPlayers, maxPlayers, preset)
	b.postLobbyCard(state)
}

func (b *Bot) lobbyStateFromEffect(chatID int64, gameID engine.GameID, hostName string, players []string, minPlayers, maxPlayers int, preset string) *engine.GameState {
	state := &engine.GameState{
		ID:       gameID,
		ChatID:   chatID,
		Players:  make(map[engine.PlayerID]*engine.Player),
		Config:   engine.PresetConfig(preset),
		HostID:   1,
	}
	state.Config.MinPlayers = minPlayers
	state.Config.MaxPlayers = maxPlayers
	now := time.Now()
	for i, name := range players {
		pid := engine.PlayerID(i + 1)
		state.Players[pid] = &engine.Player{
			ID:          pid,
			DisplayName: name,
			Alive:       true,
			JoinedAt:    now.Add(time.Duration(i) * time.Millisecond),
		}
		if name == hostName || (i == 0 && hostName != "") {
			state.HostID = pid
		}
	}
	return state
}

func (b *Bot) trackDiscussionActivity(msg *tgbotapi.Message) {
	ga := b.supervisor.GetGame(gameIDForChat(msg.Chat.ID))
	if ga == nil {
		return
	}
	phase := ga.Phase()
	if phase != engine.PhaseDiscussion && phase != engine.PhaseNomination {
		return
	}
	playerID := engine.PlayerID(msg.From.ID)
	if _, inGame := ga.PlayerSnapshot(playerID); inGame {
		ga.Send(engine.PlayerSpokeEvent{PlayerID: playerID})
	}
}

func (b *Bot) isGroupAdmin(chatID int64, userID int64) bool {
	chatMember, err := b.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: chatID,
			UserID: userID,
		},
	})
	if err != nil {
		return false
	}
	return chatMember.IsAdministrator() || chatMember.IsCreator()
}

func (b *Bot) processOutbox() {
	for msg := range b.outbox {
		b.dispatchEffect(msg.Effect)
	}
}

func (b *Bot) dispatchEffect(eff engine.SideEffect) {
	switch e := eff.(type) {
	case engine.SendGroupEffect:
		b.sender.SendText(e.ChatID, e.Text)

	case engine.SendDMEffect:
		b.sender.SendDM(int64(e.PlayerID), e.Text)

	case engine.SendRoleDMEffect:
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		gid, pid, deal := e.GameID, e.PlayerID, e.Deal
		if !b.roleDelivery.track(gid, deal) {
			return // the game ended while this DM sat in the queue
		}
		b.sender.SendDMWithResult(int64(pid), e.Text, func(err error) {
			done, _ := b.roleDelivery.resolve(gid, deal, err)
			// Both sends happen on one goroutine of their own: in order, so a
			// failure reaches the actor before the event that would start the
			// night, and off this one, because a callback can run on the
			// goroutine draining the actor's outbox and a blocking send there
			// would stop the actor that fills it.
			go func() {
				if err != nil {
					b.reportRoleDMFailure(ga, pid, deal, err)
				}
				if done {
					ga.Send(engine.RolesDeliveredEvent{Deal: deal})
				}
			}()
		})

	case engine.RolesDeliveredEffect:
		// The reducer has finished emitting this deal. Night 1 starts once
		// every role DM has actually resolved; if some are still in flight,
		// the last callback fires the event instead.
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		// Dispatched asynchronously: this runs on the goroutine that drains the
		// actor's outbox, and a blocking send here could stall the actor that
		// is filling it.
		if done, _ := b.roleDelivery.seal(e.GameID, e.Deal); done {
			go ga.Send(engine.RolesDeliveredEvent{Deal: e.Deal})
		}

	case engine.SendVotingKeyboardEffect:
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		state := ga.State()
		kb := buildVotingKeyboard(e.GameID, e.Targets, state.Players, e.AllowNoLynch, state.VoteCounts())
		// A new round gets a new message; the ID is kept so every ballot cast
		// afterwards edits this one instead of adding to the chat.
		gid := e.GameID
		b.boards.clearVote(gid)
		b.sender.SendTrackedKeyboard(e.ChatID, e.Prompt, kb, func(messageID int) {
			b.boards.setVote(gid, messageID)
		})

	case engine.UpdateVoteBoardEffect:
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		state := ga.State()
		kb := buildVotingKeyboard(e.GameID, e.Targets, state.Players, e.AllowNoLynch, state.VoteCounts())
		messageID, ok := b.boards.getVote(e.GameID)
		if !ok {
			// Nothing to edit — most likely the bot restarted mid-vote — so
			// post a fresh board and track that one instead.
			gid := e.GameID
			b.sender.SendTrackedKeyboard(e.ChatID, e.Text, kb, func(id int) {
				b.boards.setVote(gid, id)
			})
			return
		}
		b.sender.EditKeyboardMessage(e.ChatID, messageID, e.Text, kb)

	case engine.ReactionBarEffect:
		b.sender.SendTextWithKeyboard(e.ChatID, e.Text, buildReactionBar(e.GameID))

	case engine.SendGroupWithRematchEffect:
		b.sender.SendTextWithKeyboard(e.ChatID, e.Text, buildRematchButton(e.ChatID))

	case engine.SendNightActionEffect:
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		state := ga.State()
		info := engine.RoleInfoFor(e.Role)
		if !info.HasNightAction() {
			return
		}
		kb := buildNightActionKeyboard(e.GameID, e.Targets, state.Players, info.ActionKind)
		b.sender.SendDMWithKeyboard(int64(e.PlayerID), info.ActionPrompt, kb)

	case engine.SendLobbyStatusEffect:
		b.renderLobbyCard(e.ChatID, e.GameID, e.HostName, e.Players, e.MinPlayers, e.MaxPlayers, e.Preset)

	case engine.LobbyConfigUpdatedEffect:
		b.persistLobbyConfig(e.ChatID, e.Config)

	case engine.GameOverEffect:
		// The actor deletes the stored game once its final write has landed;
		// the durable record of what happened is written here.
		b.boards.forget(e.GameID)
		recordResults(b.store, b.sender, e.Summary)
	}
}
