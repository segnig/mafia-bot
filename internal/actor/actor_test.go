package actor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
)

func newTestGame(t *testing.T, n int) *engine.GameState {
	t.Helper()
	gs := engine.NewGameState("test", 123, 1, engine.DefaultConfig())
	base := time.Now()
	for i := engine.PlayerID(1); i <= engine.PlayerID(n); i++ {
		gs.Players[i] = &engine.Player{
			ID:       i,
			Username: "player",
			Alive:    true,
			JoinedAt: base.Add(time.Duration(i) * time.Millisecond),
		}
	}
	return gs
}

type harness struct {
	actor  *GameActor
	cancel context.CancelFunc
	exited chan struct{}

	mu      sync.Mutex
	effects []engine.SideEffect
}

// start runs the actor with a drained outbox. The outbox is never closed while
// the actor lives, mirroring production where it outlives every game.
func start(t *testing.T, gs *engine.GameState, configure func(*GameActor)) *harness {
	t.Helper()

	outbox := make(chan OutgoingMessage, 4096)
	h := &harness{exited: make(chan struct{})}
	h.actor = NewGameActor(gs, outbox)
	if configure != nil {
		configure(h.actor)
	}

	go func() {
		for msg := range outbox {
			h.mu.Lock()
			h.effects = append(h.effects, msg.Effect)
			h.mu.Unlock()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		h.actor.Run(ctx)
		close(h.exited)
	}()

	t.Cleanup(h.shutdown)
	return h
}

func (h *harness) shutdown() {
	h.cancel()
	select {
	case <-h.exited:
	case <-time.After(2 * time.Second):
	}
}

func (h *harness) waitForPhase(t *testing.T, phase engine.Phase) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.actor.Phase() == phase {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for phase %s, still in %s", phase, h.actor.Phase())
}

// waitForTimer blocks until the actor has a timeout armed for phase. Polling
// the phase alone would race, since the game often starts in the phase under
// test.
func (h *harness) waitForTimer(t *testing.T, phase engine.Phase) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.actor.TimerPhase() == phase {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no timer armed for phase %s (phase is %s, timer is for %q)",
		phase, h.actor.Phase(), h.actor.TimerPhase())
}

// F6: handing out the live pointer let other goroutines read maps the actor
// was concurrently writing, which is an unrecoverable runtime throw.
func TestStateSnapshotIsIsolated(t *testing.T) {
	h := start(t, newTestGame(t, 5), nil)

	snap := h.actor.State()
	snap.Players[1].Username = "tampered"
	snap.Phase = engine.PhaseGameOver
	delete(snap.Players, 2)

	fresh := h.actor.State()
	if fresh.Players[1].Username == "tampered" {
		t.Error("mutating a snapshot changed the actor's state")
	}
	if fresh.Phase == engine.PhaseGameOver {
		t.Error("snapshot shares the top-level struct with the actor")
	}
	if _, ok := fresh.Players[2]; !ok {
		t.Error("snapshot shares the Players map with the actor")
	}
}

// Run with -race: this is the pattern that used to crash the bot.
func TestConcurrentStateAccess(t *testing.T) {
	h := start(t, newTestGame(t, 6), nil)

	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				state := h.actor.State()
				for _, p := range state.Players {
					_ = p.Username
				}
				_ = h.actor.Phase()
				_, _ = h.actor.PlayerSnapshot(1)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			h.actor.Send(engine.JoinEvent{
				PlayerID: engine.PlayerID(100 + i),
				Username: "late",
				Time:     time.Now(),
			})
		}
	}()

	wg.Wait()
}

// F15: persistence talks to the network, so it must not sit on the event loop.
func TestPersistDoesNotBlockEventLoop(t *testing.T) {
	var mu sync.Mutex
	persists := 0

	gs := newTestGame(t, 5)
	gs.Config.MaxPlayers = 100 // the default cap would reject most of the joins

	h := start(t, gs, func(a *GameActor) {
		a.OnPersist = func(*engine.GameState) {
			mu.Lock()
			persists++
			mu.Unlock()
			time.Sleep(20 * time.Millisecond) // a slow database
		}
	})

	start := time.Now()
	for i := 0; i < 40; i++ {
		h.actor.Send(engine.JoinEvent{PlayerID: engine.PlayerID(200 + i), Username: "p", Time: time.Now()})
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.actor.State().Players) >= 45 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	elapsed := time.Since(start)

	if got := len(h.actor.State().Players); got < 45 {
		t.Fatalf("only %d players were applied; events did not get processed", got)
	}
	// 40 events x 20ms of blocking persistence would be 800ms.
	if elapsed > 500*time.Millisecond {
		t.Errorf("event processing took %v, persistence appears to be on the hot path", elapsed)
	}

	mu.Lock()
	got := persists
	mu.Unlock()
	if got == 0 {
		t.Error("nothing was persisted at all")
	}
	if got >= 40 {
		t.Errorf("expected snapshots to coalesce, got %d writes for 40 events", got)
	}
}

// A finished game should clean itself up, and only after its last write.
func TestFinishedGameTriggersCleanup(t *testing.T) {
	gs := newTestGame(t, 5)
	gs.Phase = engine.PhaseDiscussion
	for _, p := range gs.Players {
		p.Role = engine.RoleVillager
	}
	gs.Players[1].Role = engine.RoleMafia

	var mu sync.Mutex
	var order []string
	finished := make(chan engine.GameID, 1)

	h := start(t, gs, func(a *GameActor) {
		a.OnPersist = func(*engine.GameState) {
			mu.Lock()
			order = append(order, "persist")
			mu.Unlock()
		}
		a.OnFinish = func(id engine.GameID) {
			mu.Lock()
			order = append(order, "finish")
			mu.Unlock()
			finished <- id
		}
	})

	h.actor.Send(engine.EndGameEvent{PlayerID: 1})

	select {
	case id := <-finished:
		if id != "test" {
			t.Errorf("cleanup got game id %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finished game never triggered cleanup")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) == 0 || order[len(order)-1] != "finish" {
		t.Errorf("cleanup must run after the last write, got %v", order)
	}
}

// Shutdown is not the same as finishing: an interrupted game stays recoverable.
func TestShutdownDoesNotCleanUp(t *testing.T) {
	cleaned := make(chan struct{}, 1)
	h := start(t, newTestGame(t, 5), func(a *GameActor) {
		a.OnFinish = func(engine.GameID) { cleaned <- struct{}{} }
	})

	h.cancel()
	select {
	case <-h.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("actor did not shut down")
	}

	select {
	case <-cleaned:
		t.Error("a game interrupted by shutdown must not be deleted")
	default:
	}
}

// F1/F4/F14: whatever phase the game is in, something must be scheduled to
// move it along.
func TestEveryLivePhaseKeepsATimer(t *testing.T) {
	h := start(t, newTestGame(t, 7), nil)

	h.actor.Send(engine.GameCreatedEvent{})
	h.waitForTimer(t, engine.PhaseLobby)

	h.actor.Send(engine.BeginEvent{PlayerID: 1})
	h.waitForTimer(t, engine.PhaseRoleAssign)
	h.waitForPhase(t, engine.PhaseRoleAssign)

	// The transport is what converts the delivery effect into an event.
	h.actor.Send(engine.RolesDeliveredEvent{})
	h.waitForTimer(t, engine.PhaseNight)
	h.waitForPhase(t, engine.PhaseNight)
}
