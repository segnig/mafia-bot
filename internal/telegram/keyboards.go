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
		p := players[pid]
		callbackData := fmt.Sprintf("night:%s:%s:%d", gameID, actionKind, pid)
		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%d) %s", i+1, p.Username),
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

func buildVotingKeyboard(gameID engine.GameID, targets []engine.PlayerID, players map[engine.PlayerID]*engine.Player, allowNoLynch bool) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	currentRow := []tgbotapi.InlineKeyboardButton{}
	for i, pid := range targets {
		p := players[pid]
		callbackData := fmt.Sprintf("vote:%s:%d", gameID, pid)
		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%d) %s", i+1, p.Username),
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
			tgbotapi.NewInlineKeyboardButtonData("🕊️ Skip Today (No Lynch)", callbackData),
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
	)
}
