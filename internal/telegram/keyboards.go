package telegram

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/engine"
)

func buildNightActionKeyboard(gameID engine.GameID, targets []engine.PlayerID, players map[engine.PlayerID]*engine.Player, actionKind string) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, pid := range targets {
		p := players[pid]
		callbackData := fmt.Sprintf("night:%s:%s:%d", gameID, actionKind, pid)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("🎯 %s", p.Username),
				callbackData,
			),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func buildVotingKeyboard(gameID engine.GameID, targets []engine.PlayerID, players map[engine.PlayerID]*engine.Player, allowNoLynch bool) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, pid := range targets {
		p := players[pid]
		callbackData := fmt.Sprintf("vote:%s:%d", gameID, pid)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("⚖️ %s", p.Username),
				callbackData,
			),
		))
	}
	if allowNoLynch {
		callbackData := fmt.Sprintf("vote:%s:0", gameID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🕊️ No Lynch", callbackData),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func buildJoinButton(gameID engine.GameID) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✋ Join Game", fmt.Sprintf("join:%s", gameID)),
		),
	)
}
