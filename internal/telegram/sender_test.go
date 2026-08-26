package telegram

import (
	"errors"
	"testing"
	"time"
)

// F11: without a limiter a busy game trips Telegram's per-chat cap and its
// replies start disappearing.
func TestTokenBucketLimitsRate(t *testing.T) {
	quit := make(chan struct{})
	b := newTokenBucket(2, 20*time.Millisecond)

	start := time.Now()
	for i := 0; i < 5; i++ {
		b.wait(quit)
	}
	elapsed := time.Since(start)

	// Two tokens are free; the remaining three cost ~20ms each.
	if elapsed < 50*time.Millisecond {
		t.Errorf("5 sends through a 2-burst/20ms bucket took only %v", elapsed)
	}
	if elapsed > 400*time.Millisecond {
		t.Errorf("limiter is far slower than configured: %v", elapsed)
	}
}

func TestTokenBucketBurstIsFree(t *testing.T) {
	quit := make(chan struct{})
	b := newTokenBucket(5, time.Second)

	start := time.Now()
	for i := 0; i < 5; i++ {
		b.wait(quit)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("a full burst should not be delayed, took %v", elapsed)
	}
}

func TestTokenBucketUnblocksOnQuit(t *testing.T) {
	quit := make(chan struct{})
	b := newTokenBucket(1, time.Hour)
	b.wait(quit) // consume the only token

	done := make(chan struct{})
	go func() {
		b.wait(quit)
		close(done)
	}()

	close(quit)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("limiter ignored shutdown, so Stop() would hang forever")
	}
}

func TestChatLimiterIsPerChat(t *testing.T) {
	quit := make(chan struct{})
	c := newChatLimiter(1, time.Hour)

	start := time.Now()
	c.wait(1, quit)
	c.wait(2, quit)
	c.wait(3, quit)

	// Separate chats must not consume each other's budget.
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("distinct chats shared a bucket, took %v", elapsed)
	}
}

// F10: these classifiers pick the retry strategy, so mislabelling an error
// either drops a message or retries something permanent forever.
func TestErrorClassification(t *testing.T) {
	cases := []struct {
		err                        string
		blocked, limited, parseErr bool
	}{
		{"Forbidden: bot was blocked by the user", true, false, false},
		{"Forbidden: user is deactivated", true, false, false},
		{"Bad Request: chat not found", true, false, false},
		{"Too Many Requests: retry after 12", false, true, false},
		{"Bad Request: can't parse entities: Can't find end of the entity", false, false, true},
		{"Bad Request: message is too long", false, false, false},
		{"Bad Request: message is not modified", false, false, false},
	}
	for _, tc := range cases {
		err := errors.New(tc.err)
		if got := isBotBlocked(err); got != tc.blocked {
			t.Errorf("isBotBlocked(%q) = %v, want %v", tc.err, got, tc.blocked)
		}
		if got := isRateLimited(err); got != tc.limited {
			t.Errorf("isRateLimited(%q) = %v, want %v", tc.err, got, tc.limited)
		}
		if got := isParseError(err); got != tc.parseErr {
			t.Errorf("isParseError(%q) = %v, want %v", tc.err, got, tc.parseErr)
		}
	}
}
