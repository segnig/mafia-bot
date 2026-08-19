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
	api           *tgbotapi.BotAPI
	sender        *Sender
	supervisor    *actor.Supervisor
	store         store.Store
	outbox        chan actor.OutgoingMessage
	joinCooldowns map[string]time.Time // "chatID:playerID" -> expiry
}

func NewBot(token string, st store.Store) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("create bot api: %w", err)
	}

	outbox := make(chan actor.OutgoingMessage, 256)
	b := &Bot{
		api:           api,
		sender:        NewSender(api, 3),
		supervisor:    actor.NewSupervisor(outbox),
		store:         st,
		outbox:        outbox,
		joinCooldowns: make(map[string]time.Time),
	}

	// Wire DM failure handler to detect blocked users (§8.2)
	b.sender.OnDMFailure = func(userID int64, err error) {
		playerID := engine.PlayerID(userID)
		for _, gid := range b.supervisor.ActiveGames() {
			ga := b.supervisor.GetGame(gid)
			if ga == nil {
				continue
			}
			state := ga.State()
			if _, ok := state.Players[playerID]; ok {
				ga.Send(engine.PlayerDisconnectedEvent{PlayerID: playerID})
			}
		}
	}

	go b.processOutbox()
	b.recoverGames()
	return b, nil
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
		if state.Phase == engine.PhaseGameOver || state.Phase == engine.PhaseIdle {
			_ = b.store.Delete(gid)
			continue
		}

		ga := b.supervisor.StartGame(state)
		ga.OnPersist = func(s *engine.GameState) {
			if err := b.store.Save(s); err != nil {
				log.Printf("persist error: %v", err)
			}
		}

		// If phase deadline already passed, fire timeout immediately
		if !state.PhaseDeadline.IsZero() && time.Now().After(state.PhaseDeadline) {
			ga.Send(engine.TimeoutEvent{Phase: state.Phase})
		}

		b.sender.SendText(state.ChatID, "🔄 The bot has restarted. Your game has been resumed!")
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
		// Detect player leaving the group (§8.7)
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
			// Track player activity during discussion (non-command messages)
			b.trackDiscussionActivity(update.Message)
		}
	}
}

func (b *Bot) Stop() {
	b.sender.Stop()
}

func (b *Bot) handleLeftChatMember(msg *tgbotapi.Message) {
	playerID := engine.PlayerID(msg.LeftChatMember.ID)
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}
	ga.Send(engine.PlayerDisconnectedEvent{PlayerID: playerID})
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
	case "start":
		if msg.Chat.IsPrivate() {
			b.handleDMStart(msg)
		}
	}
}

func (b *Bot) cmdStartGame(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		b.sender.SendText(msg.Chat.ID, "Use this command in a group chat.")
		return
	}

	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	if existing := b.supervisor.GetGame(gameID); existing != nil {
		b.sender.SendText(msg.Chat.ID, "A game is already in progress. Use /status to check.")
		return
	}

	hostID := engine.PlayerID(msg.From.ID)
	cfg := engine.DefaultConfig()
	if err := engine.ValidateConfig(cfg); err != nil {
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf("Invalid game config: %v", err))
		return
	}
	state := engine.NewGameState(gameID, msg.Chat.ID, hostID, cfg)

	// Add host as first player
	state.Players[hostID] = &engine.Player{
		ID:       hostID,
		Username: msg.From.UserName,
		Alive:    true,
		JoinedAt: time.Now(),
	}

	ga := b.supervisor.StartGame(state)
	ga.OnPersist = func(s *engine.GameState) {
		if err := b.store.Save(s); err != nil {
			log.Printf("persist error: %v", err)
		}
	}

	// Notify waitlisted players
	if waitlist, err := b.store.GetWaitlist(msg.Chat.ID); err == nil && len(waitlist) > 0 {
		b.sender.SendText(msg.Chat.ID, "📢 A new game is starting! Waitlisted players: tap Join below!")
		_ = b.store.ClearWaitlist(msg.Chat.ID)
	}

	// Show lobby card with join button
	b.sendLobbyCard(msg.Chat.ID, gameID, msg.From.UserName, []string{msg.From.UserName}, cfg.MinPlayers, cfg.MaxPlayers)
}

func (b *Bot) cmdJoin(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}

	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	playerID := engine.PlayerID(msg.From.ID)

	if ga == nil {
		_ = b.store.AddToWaitlist(msg.Chat.ID, playerID)
		b.sender.SendText(msg.Chat.ID, "No active game. You've been added to the waitlist for the next game.")
		return
	}

	// Check if game already started — apply cooldown to prevent spam (§8a.3)
	state := ga.State()
	if state.Phase != engine.PhaseLobby {
		cooldownKey := fmt.Sprintf("%d:%d", msg.Chat.ID, playerID)
		if expiry, ok := b.joinCooldowns[cooldownKey]; ok && time.Now().Before(expiry) {
			return // silently drop repeated attempts during cooldown
		}
		b.joinCooldowns[cooldownKey] = time.Now().Add(30 * time.Second)
		_ = b.store.AddToWaitlist(msg.Chat.ID, playerID)
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
			"@%s, this game already started — you can't join mid-game. You'll be notified when the next one opens.",
			msg.From.UserName,
		))
		return
	}

	// Check DM confirmation
	confirmed, _ := b.store.IsDMConfirmed(playerID)
	if !confirmed {
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
			"@%s, please DM me and press /start first so I can send you your role.",
			msg.From.UserName,
		))
		return
	}

	ga.Send(engine.JoinEvent{
		PlayerID: playerID,
		Username: msg.From.UserName,
		Time:     time.Now(),
	})
}

func (b *Bot) cmdLeave(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}
	ga.Send(engine.LeaveEvent{PlayerID: engine.PlayerID(msg.From.ID)})
}

func (b *Bot) cmdBegin(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		b.sender.SendText(msg.Chat.ID, "No active game. Use /startgame first.")
		return
	}
	ga.Send(engine.BeginEvent{PlayerID: engine.PlayerID(msg.From.ID)})
}

func (b *Bot) cmdNominate(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}
	// Parse mentioned user from command args or reply
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
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
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
	// Check if replying to someone
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		return engine.PlayerID(msg.ReplyToMessage.From.ID)
	}
	// Check for mentioned entities
	if msg.Entities != nil {
		for _, entity := range msg.Entities {
			if entity.Type == "text_mention" && entity.User != nil {
				return engine.PlayerID(entity.User.ID)
			}
			if entity.Type == "mention" {
				// Extract username from text and find in players
				username := msg.Text[entity.Offset+1 : entity.Offset+entity.Length] // skip @
				state := ga.State()
				for _, p := range state.Players {
					if p.Username == username {
						return p.ID
					}
				}
			}
		}
	}
	return 0
}

func (b *Bot) cmdAccuse(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
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
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}
	statement := msg.CommandArguments()
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
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}

	// Extract target and message from args: /whisper @player message
	targetID := b.extractTargetPlayer(msg, ga)
	if targetID == 0 {
		b.sender.SendText(msg.Chat.ID, "Usage: /whisper @player your secret message")
		return
	}

	// Get message text after the mention
	args := msg.CommandArguments()
	// Remove the @mention from the args to get the message body
	whisperMsg := args
	if msg.Entities != nil {
		for _, entity := range msg.Entities {
			if entity.Type == "mention" || entity.Type == "text_mention" {
				endPos := entity.Offset + entity.Length - len("/whisper ") 
				if endPos > 0 && endPos < len(args) {
					whisperMsg = strings.TrimSpace(args[endPos:])
				}
			}
		}
	}
	if whisperMsg == "" || whisperMsg == args {
		// Fallback: split by space, first word is username, rest is message
		parts := strings.SplitN(args, " ", 2)
		if len(parts) < 2 {
			b.sender.SendText(msg.Chat.ID, "Usage: /whisper @player your secret message")
			return
		}
		whisperMsg = parts[1]
	}

	ga.Send(engine.WhisperEvent{
		FromID:  engine.PlayerID(msg.From.ID),
		ToID:    targetID,
		Message: whisperMsg,
	})
}

func (b *Bot) cmdTransferHost(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
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
	})
}

func (b *Bot) cmdKick(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
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
	})
}

func (b *Bot) cmdEndGame(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}
	ga.Send(engine.EndGameEvent{
		PlayerID: engine.PlayerID(msg.From.ID),
		IsAdmin:  b.isGroupAdmin(msg.Chat.ID, msg.From.ID),
	})
}

func (b *Bot) cmdStatus(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		b.sender.SendText(msg.Chat.ID, "No active game.")
		return
	}

	state := ga.State()
	alive := state.AlivePlayers()
	dead := 0
	for _, p := range state.Players {
		if !p.Alive {
			dead++
		}
	}

	b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
		"📊 *Game Status*\nPhase: %s (Day %d)\nAlive: %d | Dead: %d",
		state.Phase, state.DayNumber, len(alive), dead,
	))
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
		state := ga.State()
		if p, ok := state.Players[playerID]; ok {
			b.sender.SendDM(msg.Chat.ID, fmt.Sprintf(
				"Your role: *%s* (%s team)\nGame: %s\nStatus: %s",
				p.Role, engine.RoleTeam(p.Role), gameID,
				map[bool]string{true: "Alive", false: "Dead"}[p.Alive],
			))
			return
		}
	}
	b.sender.SendDM(msg.Chat.ID, "You're not in any active game.")
}

func (b *Bot) handleCallback(cq *tgbotapi.CallbackQuery) {
	// Acknowledge callback immediately
	callback := tgbotapi.NewCallback(cq.ID, "")
	b.api.Send(callback)

	data := cq.Data
	parts := strings.Split(data, ":")

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
	}
}

func (b *Bot) handleNightCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	// Format: night:<gameID>:<actionKind>:<targetID>
	if len(parts) < 4 {
		return
	}
	gameID := engine.GameID(parts[1])
	actionKind := parts[2]
	targetID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return
	}

	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}

	// Anti-cheat: validate player is in game, alive, and phase is correct (§8.9)
	playerID := engine.PlayerID(cq.From.ID)
	state := ga.State()
	p, inGame := state.Players[playerID]
	if !inGame || !p.Alive {
		return
	}
	if state.Phase != engine.PhaseNight {
		return
	}

	ga.Send(engine.NightActionEvent{
		Action: engine.NightAction{
			ActorID:     playerID,
			Kind:        actionKind,
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
	gameID := engine.GameID(parts[1])
	targetID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return
	}

	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}

	// Anti-cheat: validate player is in game, alive, and phase is voting (§8.9)
	playerID := engine.PlayerID(cq.From.ID)
	state := ga.State()
	p, inGame := state.Players[playerID]
	if !inGame || !p.Alive {
		return
	}
	if state.Phase != engine.PhaseVoting {
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
	if len(parts) < 2 {
		return
	}
	gameID := engine.GameID(parts[1])
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}

	playerID := engine.PlayerID(cq.From.ID)
	confirmed, _ := b.store.IsDMConfirmed(playerID)
	if !confirmed {
		return
	}

	ga.Send(engine.JoinEvent{
		PlayerID: playerID,
		Username: cq.From.UserName,
		Time:     time.Now(),
	})
}

func (b *Bot) sendLobbyCard(chatID int64, gameID engine.GameID, hostName string, players []string, minPlayers, maxPlayers int) {
	playerList := ""
	for i, name := range players {
		playerList += fmt.Sprintf("%d. @%s\n", i+1, name)
	}

	readyStatus := "❌ Not enough players"
	if len(players) >= minPlayers {
		readyStatus = "✅ Ready to start! Host: use /begin"
	}

	text := fmt.Sprintf(
		"🎮 *MAFIA — Game Lobby*\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"👑 Host: @%s\n"+
			"👥 Players: %d/%d\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"\n%s\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"%s\n"+
			"\n_Tap the button below to join!_",
		hostName, len(players), maxPlayers, playerList, readyStatus,
	)

	kb := buildJoinButton(gameID)
	b.sender.SendTextWithKeyboard(chatID, text, kb)
}

func (b *Bot) trackDiscussionActivity(msg *tgbotapi.Message) {
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}
	state := ga.State()
	if state.Phase != engine.PhaseDiscussion {
		return
	}
	playerID := engine.PlayerID(msg.From.ID)
	if _, inGame := state.Players[playerID]; inGame {
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

func (b *Bot) getUsername(playerID engine.PlayerID) string {
	for _, gid := range b.supervisor.ActiveGames() {
		ga := b.supervisor.GetGame(gid)
		if ga == nil {
			continue
		}
		state := ga.State()
		if p, ok := state.Players[playerID]; ok {
			return p.Username
		}
	}
	return "unknown"
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

	case engine.SendVotingKeyboardEffect:
		for _, gid := range b.supervisor.ActiveGames() {
			ga := b.supervisor.GetGame(gid)
			if ga == nil {
				continue
			}
			state := ga.State()
			if state.ChatID == e.ChatID {
				kb := buildVotingKeyboard(state.ID, e.Targets, state.Players, e.AllowNoLynch)
				b.sender.SendTextWithKeyboard(e.ChatID, "Vote for who to lynch:", kb)
				return
			}
		}

	case engine.SendNightActionEffect:
		ga := b.supervisor.GetGame(e.GameID)
		if ga == nil {
			return
		}
		state := ga.State()
		var actionKind string
		var prompt string
		switch e.Role {
		case engine.RoleMafia, engine.RoleGodfather:
			actionKind = engine.ActionMafiaKill
			prompt = "🔪 Choose a player to eliminate tonight:"
		case engine.RoleDetective:
			actionKind = engine.ActionDetectiveCheck
			prompt = "🔍 Choose a player to investigate:"
		case engine.RoleDoctor:
			actionKind = engine.ActionDoctorProtect
			prompt = "💊 Choose a player to protect tonight:"
		case engine.RoleVigilante:
			actionKind = engine.ActionVigilanteKill
			prompt = "🔫 Choose a player to shoot (one-time use!):"
		}
		kb := buildNightActionKeyboard(e.GameID, e.Targets, state.Players, actionKind)
		b.sender.SendDMWithKeyboard(int64(e.PlayerID), prompt, kb)

	case engine.SendLobbyStatusEffect:
		b.sendLobbyCard(e.ChatID, e.GameID, e.HostName, e.Players, e.MinPlayers, e.MaxPlayers)

	case engine.SendLastWordsEffect:
		b.sender.SendText(e.ChatID, fmt.Sprintf("🎤 @%s has the floor for last words...", b.getUsername(e.PlayerID)))

	case engine.SendWhisperEffect:
		fromName := b.getUsername(e.FromID)
		b.sender.SendDM(int64(e.ToID), fmt.Sprintf("🤫 *Whisper from @%s:* %s", fromName, e.Message))

	case engine.SendNominationKeyboardEffect:
		// Handled via /nominate command, no keyboard needed

	case engine.GameOverEffect:
		// Already handled via SendGroupEffect in the reducer
	}
}
