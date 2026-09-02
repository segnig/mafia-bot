package telegram

import (
	"fmt"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/actor"
	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/stats"
	"github.com/segni/mafia-bot/internal/store"
)

// leaderboardSize is how many players a leaderboard shows.
const leaderboardSize = 10

// cmdStats shows a player's lifetime record. With no argument it reports the
// sender's own; a mention or reply reports that player's instead.
func (b *Bot) cmdStats(msg *tgbotapi.Message) {
	target := engine.PlayerID(msg.From.ID)
	if mentioned := b.mentionedPlayer(msg); mentioned != 0 {
		target = mentioned
	}

	playerStats, err := b.store.LoadPlayerStats(target)
	if err != nil {
		log.Printf("stats: load failed for %d: %v", target, err)
		b.sender.SendText(msg.Chat.ID, "Couldn't read the records right now. Try again in a moment.")
		return
	}
	// A record with no games has no name attached yet, so borrow the one from
	// the message rather than showing a bare numeric ID.
	if playerStats != nil && playerStats.Username == "" && target == engine.PlayerID(msg.From.ID) {
		playerStats.Username = msg.From.UserName
		playerStats.Name = msg.From.FirstName
	}
	b.sender.SendText(msg.Chat.ID, stats.FormatPlayerCard(playerStats))
}

// cmdLeaderboard shows the best players. In a group it is scoped to that
// group; in a DM it is the global board.
func (b *Bot) cmdLeaderboard(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	title := "Top players here"
	if msg.Chat.IsPrivate() {
		chatID = 0
		title = "Top players overall"
	}
	// "global" asks for the worldwide board from inside a group.
	if isGlobalLeaderboard(msg.CommandArguments()) {
		chatID = 0
		title = "Top players overall"
	}

	b.sendLeaderboard(msg.Chat.ID, chatID, title)
}

func (b *Bot) sendLeaderboard(replyTo int64, scopeChatID int64, title string) {
	top, err := b.store.TopPlayers(scopeChatID, leaderboardSize)
	if err != nil {
		log.Printf("leaderboard: query failed: %v", err)
		b.sender.SendText(replyTo, "Couldn't read the leaderboard right now. Try again in a moment.")
		return
	}
	b.sender.SendText(replyTo, stats.FormatLeaderboard(title, top, leaderboardSize))
}

func (b *Bot) cmdAchievements(msg *tgbotapi.Message) {
	playerStats, err := b.store.LoadPlayerStats(engine.PlayerID(msg.From.ID))
	if err != nil {
		log.Printf("achievements: load failed: %v", err)
		b.sender.SendText(msg.Chat.ID, "Couldn't read your achievements right now.")
		return
	}
	b.sender.SendText(msg.Chat.ID, stats.FormatAchievements(playerStats))
}

func (b *Bot) cmdLastGame(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		b.sender.SendDM(msg.Chat.ID, "Ask for the recap in the group whose game you want to see.")
		return
	}
	b.sendLastGame(msg.Chat.ID)
}

func (b *Bot) sendLastGame(chatID int64) {
	record, err := b.store.LastGameRecord(chatID)
	if err != nil {
		log.Printf("lastgame: query failed: %v", err)
		b.sender.SendText(chatID, "Couldn't read the game history right now.")
		return
	}
	b.sender.SendText(chatID, stats.FormatGameRecap(record))
}

// mentionedPlayer resolves a @mention or a reply to a user ID. Unlike
// extractTargetPlayer it does not need a running game, so it works for /stats
// between games.
func (b *Bot) mentionedPlayer(msg *tgbotapi.Message) engine.PlayerID {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		return engine.PlayerID(msg.ReplyToMessage.From.ID)
	}
	for _, entity := range msg.Entities {
		if entity.Type == "text_mention" && entity.User != nil {
			return engine.PlayerID(entity.User.ID)
		}
	}
	// A plain @username mention carries no user ID, so it can only be
	// resolved against the players of a running game.
	if ga := b.supervisor.GetGame(gameIDForChat(msg.Chat.ID)); ga != nil {
		return b.extractTargetPlayer(msg, ga)
	}
	return 0
}

// ---------------------------------------------------------------------------
// Lobby settings (configured during game creation)
// ---------------------------------------------------------------------------

// cmdSettings opens the lobby configuration panel while a lobby is open.
func (b *Bot) cmdSettings(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		b.sender.SendDM(msg.Chat.ID, "Configure the game in your group lobby with /settings or tap ⚙️ Configure on the lobby card.")
		return
	}

	ga := b.supervisor.GetGame(gameIDForChat(msg.Chat.ID))
	if ga == nil {
		b.sender.SendText(msg.Chat.ID, "No lobby is open. Run /startgame first, then configure the rules before /begin.")
		return
	}
	if ga.Phase() != engine.PhaseLobby {
		b.sender.SendText(msg.Chat.ID, "Settings are locked once the game starts. Change them in the lobby before /begin.")
		return
	}
	if !b.mayEditLobbyConfig(ga, msg.From.ID) {
		b.sender.SendText(msg.Chat.ID, "Only the host or a group admin can configure this game.")
		return
	}
	b.openLobbySettingsPanel(ga.State())
}

func (b *Bot) mayEditLobbyConfig(ga *actor.GameActor, userID int64) bool {
	if b.isGroupAdmin(ga.ChatID(), userID) {
		return true
	}
	return ga.State().HostID == engine.PlayerID(userID)
}

func (b *Bot) openLobbySettingsPanel(state *engine.GameState) {
	gameID := state.ID
	b.sender.SendTrackedKeyboard(
		state.ChatID,
		engine.FormatLobbySettingsPanel(state.Config),
		buildLobbySettingsKeyboard(gameID, state.Config),
		func(messageID int) { b.boards.setLobbySettings(gameID, messageID) },
	)
}

func (b *Bot) persistLobbyConfig(chatID int64, cfg engine.GameConfig) {
	if err := b.store.SaveChatSettings(store.FromConfig(chatID, cfg)); err != nil {
		log.Printf("settings: save failed for chat %d: %v", chatID, err)
	}
}

// handleLobbyConfigCallback applies one tap on the lobby settings panel.
// Format: lobbycfg:<gameID>:<action>:<value>
func (b *Bot) handleLobbyConfigCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) < 3 {
		return
	}
	gameID := engine.GameID(parts[1])
	ga := b.supervisor.GetGame(gameID)
	if ga == nil {
		b.answerCallback(cq, "That lobby is gone.")
		return
	}
	state := ga.State()
	if state.Phase != engine.PhaseLobby {
		b.answerCallback(cq, "Settings are locked — the game already started.")
		return
	}

	action := parts[2]
	if action == "open" {
		if !b.mayEditLobbyConfig(ga, cq.From.ID) {
			b.answerCallback(cq, "")
			b.sender.SendText(state.ChatID, engine.FormatSettingsPanel(state.Config))
			return
		}
		b.answerCallback(cq, "")
		b.openLobbySettingsPanel(state)
		return
	}

	if !b.mayEditLobbyConfig(ga, cq.From.ID) {
		b.answerCallback(cq, "Only the host or a group admin can change settings.")
		return
	}

	playerID := engine.PlayerID(cq.From.ID)
	isAdmin := b.isGroupAdmin(state.ChatID, cq.From.ID)

	switch action {
	case "close":
		b.boards.clearLobbySettings(gameID)
		b.answerCallback(cq, "Settings saved.")
		label, _ := engine.PresetLabel(state.Config.PresetName)
		text := fmt.Sprintf("⚙️ *Settings saved* — *%s*.\n\nRun /begin when everyone has joined.",
			engine.EscapeMD(label))
		if cq.Message != nil {
			b.sender.EditKeyboardMessage(state.ChatID, cq.Message.MessageID, text, tgbotapi.NewInlineKeyboardMarkup())
		}
		return

	case "preset":
		if len(parts) < 4 {
			return
		}
		cfg := engine.PresetConfig(parts[3])
		if err := engine.ValidateConfig(cfg); err != nil {
			b.answerCallback(cq, "That preset wouldn't make a playable game.")
			return
		}
		ga.Send(engine.ConfigPresetEvent{PlayerID: playerID, IsAdmin: isAdmin, Preset: parts[3]})
		b.answerCallback(cq, "")
		b.editLobbySettingsPanelConfig(cq, state.ChatID, gameID, cfg)

	case "set":
		if len(parts) < 4 {
			return
		}
		cfg := state.Config
		if !engine.CycleSetting(&cfg, parts[3]) {
			return
		}
		if err := engine.ValidateConfig(cfg); err != nil {
			b.answerCallback(cq, "That combination wouldn't make a playable game.")
			return
		}
		ga.Send(engine.ConfigSettingEvent{PlayerID: playerID, IsAdmin: isAdmin, Key: parts[3]})
		b.answerCallback(cq, "")
		b.editLobbySettingsPanelConfig(cq, state.ChatID, gameID, cfg)

	default:
		return
	}
}

func (b *Bot) editLobbySettingsPanelConfig(cq *tgbotapi.CallbackQuery, chatID int64, gameID engine.GameID, cfg engine.GameConfig) {
	text := engine.FormatLobbySettingsPanel(cfg)
	keyboard := buildLobbySettingsKeyboard(gameID, cfg)

	messageID := 0
	if tracked, ok := b.boards.getLobbySettings(gameID); ok {
		messageID = tracked
	} else if cq.Message != nil {
		messageID = cq.Message.MessageID
	}
	if messageID == 0 {
		return
	}
	b.sender.EditKeyboardMessage(chatID, messageID, text, keyboard)
}

// answerCallback acknowledges a tap, optionally with a toast. Every callback
// must be answered or Telegram leaves a spinner on the button.
func (b *Bot) answerCallback(cq *tgbotapi.CallbackQuery, text string) {
	callback := tgbotapi.NewCallback(cq.ID, text)
	if _, err := b.api.Request(callback); err != nil {
		log.Printf("callback answer failed: %v", err)
	}
}
