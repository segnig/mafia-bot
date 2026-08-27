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

// shutdownDrain is how long the workers keep delivering already-queued messages
// after Stop is called. Without a window, a redeploy swallows the final
// messages of every game in progress; without a bound, Stop can hang for as
// long as a full queue takes to flush through the rate limiter.
const shutdownDrain = 5 * time.Second

// Sender implements a rate-limited outbound message queue.
type Sender struct {
	bot          *tgbotapi.BotAPI
	queue        chan *sendRequest
	wg           sync.WaitGroup
	stopOnce     sync.Once
	hardStopOnce sync.Once
	quit         chan struct{}
	// hardStop closes once the drain window has elapsed. Waits inside a
	// delivery watch this rather than quit, so rate limits are still honoured
	// while the queue drains and are not simply abandoned in a burst.
	hardStop    chan struct{}
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
	// onSent, when set, receives the message ID Telegram assigned. Used by
	// callers that need to edit the message later, such as the vote board.
	onSent func(messageID int)
	// isEdit marks a request that modifies an existing message. A failed edit
	// is not worth retrying or falling back on: the content is transient and
	// the next update will supersede it.
	isEdit bool
}

func NewSender(bot *tgbotapi.BotAPI, workers int) *Sender {
	s := &Sender{
		bot:        bot,
		queue:      make(chan *sendRequest, 512),
		quit:       make(chan struct{}),
		hardStop:   make(chan struct{}),
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

// Stop refuses new messages, flushes what is already queued within
// shutdownDrain, and waits for the workers to exit.
func (s *Sender) Stop() {
	s.stopOnce.Do(func() {
		close(s.quit)
		close(s.queue)
		time.AfterFunc(shutdownDrain, func() {
			s.hardStopOnce.Do(func() { close(s.hardStop) })
		})
	})
	s.wg.Wait()
	// Nothing is left to deliver, so release the drain deadline rather than
	// leaving a timer holding a reference to the sender.
	s.hardStopOnce.Do(func() { close(s.hardStop) })
}

// enqueue hands a request to the workers, reporting the outcome itself when it
// cannot.
func (s *Sender) enqueue(req *sendRequest) {
	if err := s.offer(req); err != nil {
		report(req, err)
	}
}

// offer is separated from enqueue so the recover below covers only the channel
// send. A panic raised by a caller's onResult must not be mistaken for a closed
// queue and reported a second time.
func (s *Sender) offer(req *sendRequest) (err error) {
	defer func() {
		// Stop() closes the queue; a send racing with shutdown is not worth
		// crashing the process over.
		if r := recover(); r != nil {
			log.Printf("sender: dropped message for chat %d during shutdown", req.chatID)
			err = errors.New("sender is shutting down")
		}
	}()
	select {
	case s.queue <- req:
		return nil
	case <-s.quit:
		return errors.New("sender is shutting down")
	default:
		log.Printf("sender: queue full, dropping message for chat %d", req.chatID)
		return errors.New("sender queue is full")
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
		// Past the drain deadline the remaining backlog is abandoned, so that
		// shutdown cannot be held open by a queue that is still full.
		select {
		case <-s.hardStop:
			report(req, errors.New("sender is shutting down"))
			continue
		default:
		}
		report(req, s.deliver(req))
	}
}

// deliver retries in place and reports the final outcome. Re-queuing from a
// worker can deadlock: if the queue is full and every worker is blocked
// writing to it, nothing drains.
func (s *Sender) deliver(req *sendRequest) error {
	s.global.wait(s.hardStop)
	if !req.isDM {
		s.perChat.wait(req.chatID, s.hardStop)
	}

	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		sent, err := s.bot.Send(req.msg)
		if err == nil {
			if req.onSent != nil && sent.MessageID != 0 {
				req.onSent(sent.MessageID)
			}
			return nil
		}

		// Telegram rejects an edit whose content is unchanged. That is a
		// no-op, not a failure, and retrying it would only waste quota.
		if req.isEdit && isUnchangedEdit(err) {
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
			if req.isEdit {
				// The board will be rewritten on the next update anyway.
				return err
			}
			plain := tgbotapi.NewMessage(req.chatID, req.text)
			if req.markup != nil {
				plain.ReplyMarkup = *req.markup
			}
			sent, plainErr := s.bot.Send(plain)
			if plainErr != nil {
				log.Printf("plain-text fallback also failed for chat %d: %v", req.chatID, plainErr)
				return plainErr
			}
			if req.onSent != nil && sent.MessageID != 0 {
				req.onSent(sent.MessageID)
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
			case <-s.hardStop:
				return err
			}

		default:
			log.Printf("telegram send error (chat %d): %v", req.chatID, err)
			return err
		}
	}
	return errors.New("exhausted retries")
}

// errorText is the message to classify. A nil error has no text, so every
// classifier below reports false for it rather than panicking on a path where
// the success case was not filtered out first.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func isBotBlocked(err error) bool {
	msg := errorText(err)
	return strings.Contains(msg, "bot was blocked by the user") ||
		strings.Contains(msg, "user is deactivated") ||
		strings.Contains(msg, "chat not found") ||
		strings.Contains(msg, "bot can't initiate conversation")
}

func isRateLimited(err error) bool {
	msg := errorText(err)
	return strings.Contains(msg, "Too Many Requests") || strings.Contains(msg, "retry after")
}

func isParseError(err error) bool {
	msg := errorText(err)
	return strings.Contains(msg, "can't parse entities") ||
		strings.Contains(msg, "can't parse message text")
}

// isUnchangedEdit recognises Telegram's complaint that an edit would not
// change anything, which happens whenever a board is refreshed without new
// content.
func isUnchangedEdit(err error) bool {
	return strings.Contains(errorText(err), "message is not modified")
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

// SendTrackedKeyboard posts a keyboard message and reports the message ID, so
// the caller can keep editing it in place instead of posting a new one.
func (s *Sender) SendTrackedKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup, onSent func(messageID int)) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = keyboard
	s.enqueue(&sendRequest{
		msg: msg, chatID: chatID, text: text, markup: &keyboard, onSent: onSent,
	})
}

// EditKeyboardMessage rewrites a previously sent message and its keyboard.
// Editing is how the live vote board and the settings panel stay current
// without adding a message to the chat every time something changes.
func (s *Sender) EditKeyboardMessage(chatID int64, messageID int, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, text, keyboard)
	edit.ParseMode = tgbotapi.ModeMarkdown
	edit.DisableWebPagePreview = true
	s.enqueue(&sendRequest{
		msg: edit, chatID: chatID, text: text, markup: &keyboard, isEdit: true,
	})
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
