package telegram

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
)

// A player who has blocked the bot can never receive a role, so the roles have
// to be redealt without them.
func TestBlockedPlayerIsRedealt(t *testing.T) {
	for _, msg := range []string{
		"Forbidden: bot was blocked by the user",
		"Forbidden: user is deactivated",
		"Bad Request: chat not found",
		"Forbidden: bot can't initiate conversation with a user",
	} {
		ev := roleDMFailureEvent(7, 3, errors.New(msg))
		got, ok := ev.(engine.RoleDeliveryFailedEvent)
		if !ok {
			t.Errorf("%q should trigger a redeal, got %T", msg, ev)
			continue
		}
		if got.PlayerID != 7 || got.Deal != 3 {
			t.Errorf("%q produced %+v, want player 7 on deal 3", msg, got)
		}
	}
}

// Everything else says nothing about whether the player can be reached. Ejecting
// them would cost the game a player per redeal during any busy minute, until the
// roster fell below the minimum and the whole game dropped back to the lobby.
func TestTransientFailureDoesNotEjectThePlayer(t *testing.T) {
	for _, msg := range []string{
		"Too Many Requests: retry after 30",
		"exhausted retries",
		"internal server error",
		"sender queue is full",
		"sender is shutting down",
	} {
		ev := roleDMFailureEvent(7, 3, errors.New(msg))
		got, ok := ev.(engine.PlayerDisconnectedEvent)
		if !ok {
			t.Errorf("%q should only mark the player silent, got %T", msg, ev)
			continue
		}
		if got.PlayerID != 7 {
			t.Errorf("%q produced %+v, want player 7", msg, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Shutdown
// ---------------------------------------------------------------------------

func TestStopReleasesWaitersAndRefusesNewWork(t *testing.T) {
	s := NewSender(nil, 0) // no workers: nothing will try to reach Telegram

	s.Stop()

	select {
	case <-s.hardStop:
	default:
		t.Error("Stop should release the drain deadline once the workers are done")
	}

	// A limiter wait must not outlive the shutdown, or Stop could block for as
	// long as a full queue takes to flush.
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := newTokenBucket(1, time.Hour)
		b.wait(s.hardStop) // consumes the only token
		b.wait(s.hardStop) // would otherwise wait an hour
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a rate-limit wait ignored the shutdown deadline")
	}
}

// Callers that depend on the delivery outcome must hear back exactly once, on
// every path — including the ones that never reach Telegram.
func TestOutcomeIsReportedExactlyOnceDuringShutdown(t *testing.T) {
	s := NewSender(nil, 0)
	s.Stop()

	var mu sync.Mutex
	var results []error
	s.SendDMWithResult(42, "your role", func(err error) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, err)
	})

	mu.Lock()
	defer mu.Unlock()
	if len(results) != 1 {
		t.Fatalf("expected exactly one outcome, got %d", len(results))
	}
	if results[0] == nil {
		t.Error("a message that was never sent must be reported as failed")
	}
}

// A panic thrown by the caller's own callback must not be mistaken for a closed
// queue and answered a second time.
func TestPanickingCallbackIsNotReportedTwice(t *testing.T) {
	s := NewSender(nil, 0)
	s.Stop()

	calls := 0
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("the callback's panic should propagate to its own caller")
			}
		}()
		s.SendDMWithResult(42, "your role", func(error) {
			calls++
			panic("callback exploded")
		})
	}()

	if calls != 1 {
		t.Errorf("callback ran %d times, want 1", calls)
	}
}
