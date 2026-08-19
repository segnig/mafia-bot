package telegram

import (
	"log"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Sender implements a rate-limited outbound message queue to respect Telegram's flood limits.
type Sender struct {
	bot       *tgbotapi.BotAPI
	queue     chan tgbotapi.Chattable
	wg        sync.WaitGroup
	rateLimit time.Duration
}

func NewSender(bot *tgbotapi.BotAPI, workers int) *Sender {
	s := &Sender{
		bot:       bot,
		queue:     make(chan tgbotapi.Chattable, 256),
		rateLimit: 35 * time.Millisecond, // ~28 msgs/sec, under Telegram's 30/sec global limit
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

func (s *Sender) Send(msg tgbotapi.Chattable) {
	s.queue <- msg
}

func (s *Sender) Stop() {
	close(s.queue)
	s.wg.Wait()
}

func (s *Sender) worker() {
	defer s.wg.Done()
	for msg := range s.queue {
		_, err := s.bot.Send(msg)
		if err != nil {
			log.Printf("telegram send error: %v", err)
		}
		time.Sleep(s.rateLimit)
	}
}

func (s *Sender) SendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	s.Send(msg)
}

func (s *Sender) SendTextWithKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	s.Send(msg)
}

func (s *Sender) SendDM(userID int64, text string) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = "Markdown"
	s.Send(msg)
}

func (s *Sender) SendDMWithKeyboard(userID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	s.Send(msg)
}
