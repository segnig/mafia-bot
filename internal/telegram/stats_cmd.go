package telegram

import (
	"fmt"
	"log"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/stats"
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
// Settings panel
// ---------------------------------------------------------------------------

// cmdSettings opens the configuration panel. Only a group admin or the current
// host may change the ruleset, so one player cannot rewrite the game on
// everyone else.
func (b *Bot) cmdSettings(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		b.sender.SendDM(msg.Chat.ID, "Game settings belong to a group. Run /settings there.")
		return
	}
	if !b.mayEditSettings(msg.Chat.ID, msg.From.ID) {
		b.sender.SendText(msg.Chat.ID, "Only the host or a group admin can change the settings.")
		return
	}

	cfg := b.newGameConfig(msg.Chat.ID)
	chatID := msg.Chat.ID
	b.sender.SendTrackedKeyboard(
		chatID,
		engine.FormatSettingsPanel(cfg),
		buildSettingsKeyboard(chatID, cfg),
		func(messageID int) { b.boards.setSettings(chatID, messageID) },
	)
}

// mayEditSettings allows group admins always, and the host of the game
// currently running in that chat.
func (b *Bot) mayEditSettings(chatID int64, userID int64) bool {
	if b.isGroupAdmin(chatID, userID) {
		return true
	}
	ga := b.supervisor.GetGame(gameIDForChat(chatID))
	if ga == nil {
		// With no game running there is no host, so the only authority is
		// the group's own admin list.
		return false
	}
	return ga.State().HostID == engine.PlayerID(userID)
}

// handleSettingsCallback applies one tap on the settings panel.
// Format: cfg:<chatID>:<action>:<value>
func (b *Bot) handleSettingsCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) < 4 {
		return
	}
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	if !b.mayEditSettings(chatID, cq.From.ID) {
		b.answerCallback(cq, "Only the host or a group admin can change settings.")
		return
	}

	settings, err := b.store.LoadChatSettings(chatID)
	if err != nil {
		log.Printf("settings: load failed for chat %d: %v", chatID, err)
		b.answerCallback(cq, "Couldn't load the settings.")
		return
	}
	if settings.Overrides == nil {
		settings.Overrides = make(map[string]string)
	}

	action, value := parts[2], parts[3]
	switch action {
	case "preset":
		// A preset is a fresh starting point, so the overrides layered on the
		// previous one no longer apply.
		settings.Preset = value
		settings.Overrides = make(map[string]string)

	case "set":
		setting, ok := engine.SettingByKey(value)
		if !ok {
			return
		}
		cfg := settings.Config()
		settings.Overrides[value] = setting.Next(cfg)

	case "close":
		b.boards.clearSettings(chatID)
		b.answerCallback(cq, "Settings saved.")
		b.editSettingsPanel(cq, chatID, settings, true)
		return

	default:
		return
	}

	// A combination that cannot produce a playable game is rejected rather
	// than saved, so /startgame can always trust what it reads back.
	if err := engine.ValidateConfig(settings.Config()); err != nil {
		b.answerCallback(cq, "That combination wouldn't make a playable game.")
		return
	}
	if err := b.store.SaveChatSettings(settings); err != nil {
		log.Printf("settings: save failed for chat %d: %v", chatID, err)
		b.answerCallback(cq, "Couldn't save that.")
		return
	}

	b.answerCallback(cq, "")
	b.editSettingsPanel(cq, chatID, settings, false)
}

func (b *Bot) editSettingsPanel(cq *tgbotapi.CallbackQuery, chatID int64, settings settingsSource, closed bool) {
	cfg := settings.Config()
	text := engine.FormatSettingsPanel(cfg)
	if closed {
		label, _ := engine.PresetLabel(cfg.PresetName)
		text = fmt.Sprintf("⚙️ *Settings saved* — preset *%s*.\n\nThese apply to the next game. Run /settings to change them again.",
			engine.EscapeMD(label))
		if cq.Message != nil {
			b.sender.EditKeyboardMessage(chatID, cq.Message.MessageID, text, tgbotapi.NewInlineKeyboardMarkup())
		}
		return
	}

	messageID := 0
	if tracked, ok := b.boards.getSettings(chatID); ok {
		messageID = tracked
	} else if cq.Message != nil {
		messageID = cq.Message.MessageID
	}
	if messageID == 0 {
		return
	}
	b.sender.EditKeyboardMessage(chatID, messageID, text, buildSettingsKeyboard(chatID, cfg))
}

// settingsSource is the small part of store.ChatSettings this file needs,
// which keeps the signature above readable.
type settingsSource interface {
	Config() engine.GameConfig
}

// answerCallback acknowledges a tap, optionally with a toast. Every callback
// must be answered or Telegram leaves a spinner on the button.
func (b *Bot) answerCallback(cq *tgbotapi.CallbackQuery, text string) {
	callback := tgbotapi.NewCallback(cq.ID, text)
	if _, err := b.api.Request(callback); err != nil {
		log.Printf("callback answer failed: %v", err)
	}
}
