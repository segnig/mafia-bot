package telegram

import (
	"fmt"
	"log"
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

func (b *Bot) attachHooks(ga *actor.GameActor, gameID engine.GameID) {
	ga.OnPersist = func(s *engine.GameState) {
		if err := b.store.Save(s); err != nil {
			log.Printf("persist error: %v", err)
		}
	}
	ga.OnFinish = func(id engine.GameID) {
		b.roleDelivery.forget(id)
		if err := b.store.Delete(id); err != nil {
			log.Printf("cleanup error for game %s: %v", id, err)
		}
	}
	_ = gameID
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

		ga := b.supervisor.StartGame(state)
		b.attachHooks(ga, gid)

		b.sender.SendText(state.ChatID, "🔄 The bot has restarted. Your game has been resumed!")
		// Timers only ever existed in memory, so without this the restored
		// game would sit in its current phase forever.
		ga.Send(engine.ResumeEvent{})
		log.Printf("recovery: resumed game %s (phase: %s, day: %d)", gid, state.Phase, state.DayNumber)
	}
}

func (b *Bot) Start() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	log.Printf("Bot started as @%s", b.api.Self.UserName)

	for update := range updates {
		if update.CallbackQuery != nil {
			b.handleCallback(update.CallbackQuery)
			continue
		}
		if update.Message == nil {
			continue
		}
		if update.Message.LeftChatMember != nil {
			b.handleLeftChatMember(update.Message)
			continue
		}
		if update.Message.IsCommand() {
			b.handleCommand(update.Message)
			continue
		}
		if update.Message.Chat.IsPrivate() {
			b.handleDMStart(update.Message)
		} else {
			b.trackDiscussionActivity(update.Message)
		}
	}
}

// Stop drains running games before shutting the sender down, so a redeploy
// leaves every game in a state that recovery can pick up.
func (b *Bot) Stop() {
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
	case "help":
		b.cmdHelp(msg)
	case "start":
		if msg.Chat.IsPrivate() {
			b.handleDMStart(msg)
		}
	}
}

func (b *Bot) cmdHelp(msg *tgbotapi.Message) {
	b.sender.SendText(msg.Chat.ID, `🎭 *Mafia Bot*

*Setup*
/startgame — open a lobby
/join — join the lobby
/leave — leave the lobby
/begin — host starts the game

*During the day*
/accuse @player — publicly accuse someone
/defend [statement] — make your case (once per day)
/whisper @player [text] — private message (the group sees that it happened)
/nominate @player and /second @player — trial mode only

*Anytime*
/status — current game state
/myrole — DM me to see your role
/host @player — hand over hosting
/kick @player — host or admin removes a player
/endgame — host or admin ends the game`)
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

	cfg := engine.DefaultConfig()
	if err := engine.ValidateConfig(cfg); err != nil {
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf("Invalid game config: %v", err))
		return
	}

	hostID := engine.PlayerID(msg.From.ID)
	state := engine.NewGameState(gameID, msg.Chat.ID, hostID, cfg)
	state.Players[hostID] = &engine.Player{
		ID:          hostID,
		Username:    msg.From.UserName,
		DisplayName: msg.From.FirstName,
		Alive:       true,
		JoinedAt:    time.Now(),
	}

	ga := b.supervisor.StartGame(state)
	b.attachHooks(ga, gameID)

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
			runes := []rune(msg.Text)
			if entity.Offset+entity.Length > len(runes) {
				continue
			}
			username := string(runes[entity.Offset+1 : entity.Offset+entity.Length])
			state := ga.State()
			for _, p := range state.Players {
				if strings.EqualFold(p.Username, username) {
					return p.ID
				}
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
	args := strings.TrimSpace(msg.CommandArguments())

	// Strip the leading @mention, whatever form it took, to get the body.
	body := args
	if strings.HasPrefix(body, "@") {
		parts := strings.SplitN(body, " ", 2)
		if len(parts) == 2 {
			body = strings.TrimSpace(parts[1])
		} else {
			body = ""
		}
	}

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

	var aliveList, deadList string
	for _, p := range sortedPlayers(state) {
		if p.Alive {
			status := "🟢"
			if p.Disconnected {
				status = "📵"
			}
			aliveList += fmt.Sprintf("  %s %s\n", status, p.Label())
		} else {
			role := ""
			// Only echo a role that was already revealed publicly.
			if p.RoleRevealed {
				role = fmt.Sprintf(" (%s)", p.Role)
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
			remaining = fmt.Sprintf("⏳ %ds left\n", left)
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
		text += fmt.Sprintf("*Dead:*\n%s━━━━━━━━━━━━━━━━━━━━\n", deadList)
	}

	b.sender.SendText(msg.Chat.ID, text)
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
	b.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	parts := strings.Split(cq.Data, ":")
	if len(parts) < 2 {
		return
	}

	switch parts[0] {
	case "night":
		b.handleNightCallback(cq, parts)
	case "vote":
		b.handleVoteCallback(cq, parts)
	case "join":
		b.handleJoinCallback(cq, parts)
	case "lobby":
		b.handleLobbyCallback(cq, parts)
	}
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
	ordered := sortedPlayers(state)
	names := make([]string, 0, len(ordered))
	for _, p := range ordered {
		names = append(names, p.PlainName())
	}
	hostName := ""
	if host, ok := state.Players[state.HostID]; ok {
		hostName = host.PlainName()
	}
	b.renderLobbyCard(state.ChatID, state.ID, hostName, names, state.Config.MinPlayers, state.Config.MaxPlayers)
}

func (b *Bot) renderLobbyCard(chatID int64, gameID engine.GameID, hostName string, players []string, minPlayers, maxPlayers int) {
	playerList := ""
	for i, name := range players {
		playerList += fmt.Sprintf("%d. %s\n", i+1, engine.EscapeMD(name))
	}
	if playerList == "" {
		playerList = "_nobody yet_\n"
	}

	readyStatus := fmt.Sprintf("❌ Need %d more player(s)", minPlayers-len(players))
	if len(players) >= minPlayers {
		readyStatus = "✅ Ready to start! Host: use /begin"
	}

	text := fmt.Sprintf(
		"🎮 *MAFIA — Game Lobby*\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"👑 Host: %s\n"+
			"👥 Players: %d/%d\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"\n%s\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"%s\n"+
			"\n_Tap the button below to join!_",
		engine.EscapeMD(hostName), len(players), maxPlayers, playerList, readyStatus,
	)

	b.sender.SendTextWithKeyboard(chatID, text, buildJoinButton(gameID))
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
		gid, pid := e.GameID, e.PlayerID
		batch := b.roleDelivery.track(gid)
		b.sender.SendDMWithResult(int64(pid), e.Text, func(err error) {
			if err != nil {
				// This player never learned their role, so the reducer has to
				// remove them and redeal rather than start the night.
				go ga.Send(engine.RoleDeliveryFailedEvent{PlayerID: pid})
			}
			if done, clean := b.roleDelivery.resolve(gid, batch, err); done && clean {
				go ga.Send(engine.RolesDeliveredEvent{})
			}
		})

	case engine.RolesDeliveredEffect:
		// The reducer has finished emitting this batch. Night 1 starts once
		// every role DM has actually been accepted by Telegram; if some are
		// still in flight, the last callback fires the event instead.
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		if done, clean := b.roleDelivery.seal(e.GameID); done && clean {
			go ga.Send(engine.RolesDeliveredEvent{})
		}

	case engine.SendVotingKeyboardEffect:
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		state := ga.State()
		kb := buildVotingKeyboard(e.GameID, e.Targets, state.Players, e.AllowNoLynch)
		b.sender.SendTextWithKeyboard(e.ChatID, e.Prompt, kb)

	case engine.SendNightActionEffect:
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		state := ga.State()
		var actionKind, prompt string
		switch e.Role {
		case engine.RoleMafia, engine.RoleGodfather:
			actionKind = engine.ActionMafiaKill
			prompt = "🔪 *Mafia Action*\nChoose a target to eliminate:"
		case engine.RoleDetective:
			actionKind = engine.ActionDetectiveCheck
			prompt = "🔍 *Detective Action*\nChoose one player to investigate:"
		case engine.RoleDoctor:
			actionKind = engine.ActionDoctorProtect
			prompt = "💊 *Doctor Action*\nChoose one player to protect:"
		case engine.RoleVigilante:
			actionKind = engine.ActionVigilanteKill
			prompt = "🔫 *Vigilante Action*\nChoose one player to shoot (one-time ability):"
		default:
			return
		}
		kb := buildNightActionKeyboard(e.GameID, e.Targets, state.Players, actionKind)
		b.sender.SendDMWithKeyboard(int64(e.PlayerID), prompt, kb)

	case engine.SendLobbyStatusEffect:
		b.renderLobbyCard(e.ChatID, e.GameID, e.HostName, e.Players, e.MinPlayers, e.MaxPlayers)

	case engine.GameOverEffect:
		// The actor deletes the stored game once its final write has landed.
	}
}
