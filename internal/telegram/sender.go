package telegram

import (
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// DMFailureHandler is called when a DM cannot be delivered (user blocked bot)
type DMFailureHandler func(userID int64, err error)

// Telegram's published limits: roughly 30 messages/second overall and about
// 20 messages/minute into any one group. Exceeding either earns 429s.
const (
	globalRate     = 25
	globalBurst    = 25
	groupPerMinute = 18
	groupBurst     = 6
)

// Sender implements a rate-limited outbound message queue.
type Sender struct {
	bot         *tgbotapi.BotAPI
	queue       chan *sendRequest
	wg          sync.WaitGroup
	stopOnce    sync.Once
	quit        chan struct{}
	maxRetries  int
	global      *tokenBucket
	perChat     *chatLimiter
	OnDMFailure DMFailureHandler
}

type sendRequest struct {
	msg    tgbotapi.Chattable
	chatID int64
	isDM   bool
	text   string
	markup *tgbotapi.InlineKeyboardMarkup

	// onResult, when set, is invoked once with the final outcome. A caller
	// that supplies it takes over failure handling, so OnDMFailure is skipped.
	onResult func(error)
}

func NewSender(bot *tgbotapi.BotAPI, workers int) *Sender {
	s := &Sender{
		bot:        bot,
		queue:      make(chan *sendRequest, 512),
		quit:       make(chan struct{}),
		maxRetries: 3,
		global:     newTokenBucket(globalBurst, time.Second/globalRate),
		perChat:    newChatLimiter(groupBurst, time.Minute/groupPerMinute),
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

func (s *Sender) Stop() {
	s.stopOnce.Do(func() {
		close(s.quit)
		close(s.queue)
	})
	s.wg.Wait()
}

func (s *Sender) enqueue(req *sendRequest) {
	defer func() {
		// Stop() closes the queue; a send racing with shutdown is not worth
		// crashing the process over.
		if r := recover(); r != nil {
			log.Printf("sender: dropped message for chat %d during shutdown", req.chatID)
			report(req, errors.New("sender is shutting down"))
		}
	}()
	select {
	case s.queue <- req:
	case <-s.quit:
		report(req, errors.New("sender is shutting down"))
	default:
		log.Printf("sender: queue full, dropping message for chat %d", req.chatID)
		report(req, errors.New("sender queue is full"))
	}
}

func report(req *sendRequest, err error) {
	if req.onResult != nil {
		req.onResult(err)
	}
}

func (s *Sender) worker() {
	defer s.wg.Done()
	for req := range s.queue {
		report(req, s.deliver(req))
	}
}

// deliver retries in place and reports the final outcome. Re-queuing from a
// worker can deadlock: if the queue is full and every worker is blocked
// writing to it, nothing drains.
func (s *Sender) deliver(req *sendRequest) error {
	s.global.wait(s.quit)
	if !req.isDM {
		s.perChat.wait(req.chatID, s.quit)
	}

	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		_, err := s.bot.Send(req.msg)
		if err == nil {
			return nil
		}

		switch {
		case isBotBlocked(err):
			log.Printf("DM blocked by user %d: %v", req.chatID, err)
			// A caller tracking its own result handles the consequences.
			if s.OnDMFailure != nil && req.isDM && req.onResult == nil {
				s.OnDMFailure(req.chatID, err)
			}
			return err

		case isParseError(err):
			// A stray Markdown character would otherwise lose the message
			// entirely, so fall back to sending it unformatted.
			log.Printf("markdown parse failure for chat %d, resending as plain text: %v", req.chatID, err)
			plain := tgbotapi.NewMessage(req.chatID, req.text)
			if req.markup != nil {
				plain.ReplyMarkup = *req.markup
			}
			if _, plainErr := s.bot.Send(plain); plainErr != nil {
				log.Printf("plain-text fallback also failed for chat %d: %v", req.chatID, plainErr)
				return plainErr
			}
			return nil

		case isRateLimited(err) && attempt < s.maxRetries:
			backoff := retryAfter(err)
			if backoff <= 0 {
				backoff = time.Duration(1<<uint(attempt)) * time.Second
			}
			log.Printf("rate limited, retrying in %v (attempt %d)", backoff, attempt+1)
			select {
			case <-time.After(backoff):
			case <-s.quit:
				return err
			}

		default:
			log.Printf("telegram send error (chat %d): %v", req.chatID, err)
			return err
		}
	}
	return errors.New("exhausted retries")
}

func isBotBlocked(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "bot was blocked by the user") ||
		strings.Contains(msg, "user is deactivated") ||
		strings.Contains(msg, "chat not found") ||
		strings.Contains(msg, "bot can't initiate conversation")
}

func isRateLimited(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Too Many Requests") || strings.Contains(msg, "retry after")
}

func isParseError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "can't parse entities") ||
		strings.Contains(msg, "can't parse message text")
}

func retryAfter(err error) time.Duration {
	if tgErr, ok := err.(*tgbotapi.Error); ok && tgErr.RetryAfter > 0 {
		return time.Duration(tgErr.RetryAfter) * time.Second
	}
	return 0
}

func (s *Sender) SendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	s.enqueue(&sendRequest{msg: msg, chatID: chatID, text: text})
}

func (s *Sender) SendTextWithKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = keyboard
	s.enqueue(&sendRequest{msg: msg, chatID: chatID, text: text, markup: &keyboard})
}

func (s *Sender) SendDM(userID int64, text string) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	s.enqueue(&sendRequest{msg: msg, chatID: userID, isDM: true, text: text})
}

// SendDMWithResult reports the delivery outcome to onResult exactly once. Used
// for messages whose delivery the game logic depends on, such as role
// assignments.
func (s *Sender) SendDMWithResult(userID int64, text string, onResult func(error)) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	s.enqueue(&sendRequest{msg: msg, chatID: userID, isDM: true, text: text, onResult: onResult})
}

func (s *Sender) SendDMWithKeyboard(userID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = keyboard
	s.enqueue(&sendRequest{msg: msg, chatID: userID, isDM: true, text: text, markup: &keyboard})
}

// tokenBucket is a simple refilling rate limiter.
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	capacity float64
	interval time.Duration
	last     time.Time
}

func newTokenBucket(capacity int, interval time.Duration) *tokenBucket {
	return &tokenBucket{
		tokens:   float64(capacity),
		capacity: float64(capacity),
		interval: interval,
		last:     time.Now(),
	}
}

// wait blocks until a token is available, or until quit is closed.
func (b *tokenBucket) wait(quit <-chan struct{}) {
	for {
		b.mu.Lock()
		now := time.Now()
		b.tokens += now.Sub(b.last).Seconds() / b.interval.Seconds()
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return
		}
		deficit := time.Duration((1 - b.tokens) * float64(b.interval))
		b.mu.Unlock()

		select {
		case <-time.After(deficit):
		case <-quit:
			return
		}
	}
}

// chatLimiter applies a separate bucket per chat, since Telegram enforces a
// much tighter limit within a single group than it does globally.
type chatLimiter struct {
	mu       sync.Mutex
	buckets  map[int64]*tokenBucket
	capacity int
	interval time.Duration
}

func newChatLimiter(capacity int, interval time.Duration) *chatLimiter {
	return &chatLimiter{
		buckets:  make(map[int64]*tokenBucket),
		capacity: capacity,
		interval: interval,
	}
}

func (c *chatLimiter) wait(chatID int64, quit <-chan struct{}) {
	c.mu.Lock()
	b, ok := c.buckets[chatID]
	if !ok {
		b = newTokenBucket(c.capacity, c.interval)
		c.buckets[chatID] = b
	}
	c.mu.Unlock()
	b.wait(quit)
}
