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
	api        *tgbotapi.BotAPI
	sender     *Sender
	supervisor *actor.Supervisor
	store      store.Store
	outbox     chan actor.OutgoingMessage
}

func NewBot(token string, st store.Store) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("create bot api: %w", err)
	}

	outbox := make(chan actor.OutgoingMessage, 256)
	b := &Bot{
		api:        api,
		sender:     NewSender(api, 3),
		supervisor: actor.NewSupervisor(outbox),
		store:      st,
		outbox:     outbox,
	}

	go b.processOutbox()
	return b, nil
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
		if update.Message.IsCommand() {
			b.handleCommand(update.Message)
			continue
		}
		// DM /start confirmation
		if update.Message.Chat.IsPrivate() {
			b.handleDMStart(update.Message)
		}
	}
}

func (b *Bot) Stop() {
	b.sender.Stop()
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
		b.sender.SendText(msg.Chat.ID, "A new game is starting! Waitlisted players: use /join now!")
		_ = b.store.ClearWaitlist(msg.Chat.ID)
	}

	b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
		"🎮 *Mafia Game Started!*\nHost: @%s\nUse /join to enter (min %d, max %d players).\nHost: use /begin when ready.",
		msg.From.UserName, cfg.MinPlayers, cfg.MaxPlayers,
	))
}

func (b *Bot) cmdJoin(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}

	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	playerID := engine.PlayerID(msg.From.ID)

	if ga == nil {
		// No active game — add to waitlist
		_ = b.store.AddToWaitlist(msg.Chat.ID, playerID)
		b.sender.SendText(msg.Chat.ID, "No active game. You've been added to the waitlist for the next game.")
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

func (b *Bot) cmdEndGame(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		return
	}
	gameID := engine.GameID(fmt.Sprintf("%d", msg.Chat.ID))
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		return
	}
	ga.Send(engine.EndGameEvent{PlayerID: engine.PlayerID(msg.From.ID)})
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

	ga.Send(engine.NightActionEvent{
		Action: engine.NightAction{
			ActorID:     engine.PlayerID(cq.From.ID),
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

	ga.Send(engine.VoteEvent{
		Vote: engine.Vote{
			VoterID:   engine.PlayerID(cq.From.ID),
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

	case engine.GameOverEffect:
		// Already handled via SendGroupEffect in the reducer
	}
}
