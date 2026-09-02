package telegram

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/engine"
)

func (b *Bot) cmdHelp(msg *tgbotapi.Message) {
	text := engine.ResolveHelpTopic(msg.CommandArguments())
	b.sender.SendText(msg.Chat.ID, text)
}

func (b *Bot) cmdGuide(msg *tgbotapi.Message) {
	b.sender.SendText(msg.Chat.ID, engine.FormatGuideMessage())
}
