package telegram

import (
	"log"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// DMFailureHandler is called when a DM cannot be delivered (user blocked bot)
type DMFailureHandler func(userID int64, err error)

// Sender implements a rate-limited outbound message queue with exponential backoff retry.
type Sender struct {
	bot              *tgbotapi.BotAPI
	queue            chan sendRequest
	wg               sync.WaitGroup
	rateLimit        time.Duration
	maxRetries       int
	OnDMFailure      DMFailureHandler
}

type sendRequest struct {
	msg     tgbotapi.Chattable
	chatID  int64
	isDM    bool
	retries int
}

func NewSender(bot *tgbotapi.BotAPI, workers int) *Sender {
	s := &Sender{
		bot:        bot,
		queue:      make(chan sendRequest, 256),
		rateLimit:  35 * time.Millisecond,
		maxRetries: 3,
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

func (s *Sender) Send(msg tgbotapi.Chattable) {
	s.queue <- sendRequest{msg: msg}
}

func (s *Sender) Stop() {
	close(s.queue)
	s.wg.Wait()
}

func (s *Sender) worker() {
	defer s.wg.Done()
	for req := range s.queue {
		_, err := s.bot.Send(req.msg)
		if err != nil {
			if s.isBotBlocked(err) {
				// User blocked the bot — not retryable (§8.2)
				log.Printf("DM blocked by user %d: %v", req.chatID, err)
				if s.OnDMFailure != nil && req.isDM {
					s.OnDMFailure(req.chatID, err)
				}
			} else if s.isRateLimited(err) && req.retries < s.maxRetries {
				// Rate limited — exponential backoff retry (§8.2)
				backoff := time.Duration(1<<uint(req.retries)) * time.Second
				log.Printf("rate limited, retrying in %v (attempt %d)", backoff, req.retries+1)
				time.Sleep(backoff)
				req.retries++
				s.queue <- req
			} else {
				log.Printf("telegram send error (chat %d): %v", req.chatID, err)
			}
		}
		time.Sleep(s.rateLimit)
	}
}

func (s *Sender) isBotBlocked(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "bot was blocked by the user") ||
		strings.Contains(msg, "Forbidden") ||
		strings.Contains(msg, "user is deactivated")
}

func (s *Sender) isRateLimited(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Too Many Requests") ||
		strings.Contains(msg, "retry after")
}

func (s *Sender) SendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	s.queue <- sendRequest{msg: msg, chatID: chatID, isDM: false}
}

func (s *Sender) SendTextWithKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	s.queue <- sendRequest{msg: msg, chatID: chatID, isDM: false}
}

func (s *Sender) SendDM(userID int64, text string) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = "Markdown"
	s.queue <- sendRequest{msg: msg, chatID: userID, isDM: true}
}

func (s *Sender) SendDMWithKeyboard(userID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	s.queue <- sendRequest{msg: msg, chatID: userID, isDM: true}
}
