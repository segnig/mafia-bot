package telegram

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/engine"
)

func buildNightActionKeyboard(gameID engine.GameID, targets []engine.PlayerID, players map[engine.PlayerID]*engine.Player, actionKind string) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	// Use 2-column layout for faster scanning on mobile.
	currentRow := []tgbotapi.InlineKeyboardButton{}
	for i, pid := range targets {
		p, ok := players[pid]
		if !ok {
			continue
		}
		callbackData := fmt.Sprintf("night:%s:%s:%d", gameID, actionKind, pid)
		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%d) %s", i+1, p.PlainName()),
			callbackData,
		)
		currentRow = append(currentRow, btn)
		if len(currentRow) == 2 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(currentRow...))
			currentRow = []tgbotapi.InlineKeyboardButton{}
		}
	}
	if len(currentRow) > 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(currentRow...))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// buildVotingKeyboard renders the ballot. Each button carries its current
// count so a player can see the state of the vote without leaving the keyboard.
func buildVotingKeyboard(gameID engine.GameID, targets []engine.PlayerID, players map[engine.PlayerID]*engine.Player, allowNoLynch bool, counts map[engine.PlayerID]int) tgbotapi.InlineKeyboardMarkup {
	label := func(name string, pid engine.PlayerID) string {
		if n := counts[pid]; n > 0 {
			return fmt.Sprintf("%s · %d", name, n)
		}
		return name
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	currentRow := []tgbotapi.InlineKeyboardButton{}
	for i, pid := range targets {
		p, ok := players[pid]
		if !ok {
			continue
		}
		callbackData := fmt.Sprintf("vote:%s:%d", gameID, pid)
		btn := tgbotapi.NewInlineKeyboardButtonData(
			label(fmt.Sprintf("%d) %s", i+1, p.PlainName()), pid),
			callbackData,
		)
		currentRow = append(currentRow, btn)
		if len(currentRow) == 2 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(currentRow...))
			currentRow = []tgbotapi.InlineKeyboardButton{}
		}
	}
	if len(currentRow) > 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(currentRow...))
	}
	if allowNoLynch {
		callbackData := fmt.Sprintf("vote:%s:0", gameID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				label("🕊️ Skip Today", engine.NoLynchTarget), callbackData),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func buildJoinButton(gameID engine.GameID) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Join Lobby", fmt.Sprintf("join:%s", gameID)),
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ Lobby Status", fmt.Sprintf("lobby:%s", gameID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🎭 Roles", fmt.Sprintf("info:%s:roles", gameID)),
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Configure", fmt.Sprintf("lobbycfg:%s:open", gameID)),
		),
	)
}

// buildLobbySettingsKeyboard renders the editable lobby settings panel.
// Format: lobbycfg:<gameID>:<action>:<value>
func buildLobbySettingsKeyboard(gameID engine.GameID, cfg engine.GameConfig) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton

	var presetRow []tgbotapi.InlineKeyboardButton
	for _, name := range engine.PresetNames() {
		label, _ := engine.PresetLabel(name)
		if name == cfg.PresetName {
			label = "▸ " + label
		}
		presetRow = append(presetRow, tgbotapi.NewInlineKeyboardButtonData(
			label, fmt.Sprintf("lobbycfg:%s:preset:%s", gameID, name)))
		if len(presetRow) == 2 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(presetRow...))
			presetRow = nil
		}
	}
	if len(presetRow) > 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(presetRow...))
	}

	for _, s := range engine.Settings() {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s — %s", s.Label, s.Display(cfg)),
				fmt.Sprintf("lobbycfg:%s:set:%s", gameID, s.Key)),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("✅ Done", fmt.Sprintf("lobbycfg:%s:close:x", gameID)),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// buildReactionBar is the day's one-tap mood bar.
func buildReactionBar(gameID engine.GameID) tgbotapi.InlineKeyboardMarkup {
	var row []tgbotapi.InlineKeyboardButton
	for _, emoji := range engine.MoodEmojis() {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(
			emoji, fmt.Sprintf("react:%s:%s", gameID, emoji)))
	}
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(row...),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚰️ Graveyard", fmt.Sprintf("info:%s:graveyard", gameID)),
			tgbotapi.NewInlineKeyboardButtonData("📊 Status", fmt.Sprintf("info:%s:status", gameID)),
		),
	)
}

// buildRematchButton is attached to the final recap so a group can start
// another game without typing anything.
func buildRematchButton(chatID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Rematch", fmt.Sprintf("rematch:%d", chatID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏆 Leaderboard", fmt.Sprintf("board:%d", chatID)),
			tgbotapi.NewInlineKeyboardButtonData("📜 Recap", fmt.Sprintf("recap:%d", chatID)),
		),
	)
}

// buildScheduleJoinKeyboard is the signup card shown while waiting for a
// scheduled start time. Format: schedjoin:<chatID> · schedinfo:<chatID>
func buildScheduleJoinKeyboard(chatID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Join", fmt.Sprintf("schedjoin:%d", chatID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", fmt.Sprintf("schedinfo:%d", chatID)),
		),
	)
}
