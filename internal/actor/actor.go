package actor

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
)

type OutgoingMessage struct {
	Effect engine.SideEffect
}

// fallbackPhaseTimeout is armed when a phase change leaves no timer behind. It
// exists so a missing SetTimerEffect degrades into a slow phase rather than a
// game that is stuck forever.
const fallbackPhaseTimeout = 90 * time.Second

type GameActor struct {
	id     engine.GameID
	state  *engine.GameState
	inbox  chan engine.Event
	outbox chan OutgoingMessage
	mu     sync.Mutex
	done   chan struct{}

	// Timer state has its own lock so the watchdog invariant stays observable
	// without contending with event reduction.
	timerMu       sync.Mutex
	timer         *time.Timer
	timerPhase    engine.Phase
	timerFired    bool // the armed timeout has already been delivered
	timerEver     bool // a timeout has been armed at least once
	warningTimers []*time.Timer

	// persistQueue holds at most one pending snapshot. Persistence talks to
	// the network, so it must not run on the goroutine that reduces events.
	persistQueue chan *engine.GameState
	persistWG    sync.WaitGroup

	// OnPersist stores a snapshot. It is called from a dedicated goroutine.
	OnPersist func(*engine.GameState)
	// OnFinish runs once, after the last snapshot has been written, when the
	// game reached a terminal phase. Shutdown does not trigger it, so an
	// interrupted game stays recoverable.
	OnFinish func(engine.GameID)
}

func NewGameActor(state *engine.GameState, outbox chan OutgoingMessage) *GameActor {
	return &GameActor{
		id:           state.ID,
		state:        state,
		inbox:        make(chan engine.Event, 64),
		outbox:       outbox,
		done:         make(chan struct{}),
		persistQueue: make(chan *engine.GameState, 1),
	}
}

func (a *GameActor) Send(ev engine.Event) {
	select {
	case a.inbox <- ev:
	case <-a.done:
	}
}

// State returns a snapshot that is safe to read from another goroutine. The
// live state keeps being mutated by the actor loop, so callers must never be
// handed the original pointer.
func (a *GameActor) State() *engine.GameState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Clone()
}

// Phase reads the current phase without cloning the whole state. Used on hot
// paths like per-message activity tracking.
func (a *GameActor) Phase() engine.Phase {
	return a.currentPhase()
}

// PlayerSnapshot returns a copy of one player, avoiding a full state clone for
// the membership and liveness checks that guard every callback.
func (a *GameActor) PlayerSnapshot(id engine.PlayerID) (engine.Player, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.state.Players[id]
	if !ok {
		return engine.Player{}, false
	}
	return *p, true
}

// ChatID is immutable for the life of the game.
func (a *GameActor) ChatID() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.ChatID
}

func (a *GameActor) Run(ctx context.Context) {
	defer close(a.done)

	a.persistWG.Add(1)
	go a.persistLoop()

	defer func() {
		finished := a.currentPhase().IsTerminal()
		close(a.persistQueue)
		a.persistWG.Wait()
		a.stopTimers()
		// Runs after the last write, so cleanup can never be undone by a
		// snapshot that was still in flight.
		if finished && a.OnFinish != nil {
			a.OnFinish(a.id)
		}
	}()

	for {
		select {
		case ev := <-a.inbox:
			a.handle(ev)
			if a.currentPhase().IsTerminal() {
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

func (a *GameActor) handle(ev engine.Event) {
	a.mu.Lock()
	newState, effects := engine.Reduce(a.state, ev)
	a.state = newState
	phase := a.state.Phase
	snapshot := a.state.Clone()
	a.mu.Unlock()

	timerArmed := false
	for _, eff := range effects {
		switch e := eff.(type) {
		case engine.SetTimerEffect:
			a.resetTimer(e.Duration, e.Phase)
			timerArmed = true
		case engine.SetWarningTimerEffect:
			a.addWarningTimer(e.Duration, e.Phase, e.SecondsLeft)
		default:
			a.outbox <- OutgoingMessage{Effect: eff}
		}
	}

	if phase.IsTerminal() {
		a.stopTimers()
		return // the record is about to be deleted, so don't write it back
	}

	// Safety net: a phase with nothing scheduled cannot advance on its own.
	// A timeout that has already fired counts as nothing scheduled even though
	// timerPhase still names the phase it belonged to, which is how an event
	// that consumes a timer without arming a new one gets caught.
	//
	// The check waits until the game has armed its first timer. Until then
	// there is no invariant to violate: the creating event is what starts the
	// clock, and an event that reaches the actor ahead of it would otherwise
	// log a failure that does not exist.
	if !timerArmed && !a.hasPendingTimer(phase) {
		log.Printf("game %s: phase %s left no timer armed, using fallback", a.id, phase)
		a.resetTimer(fallbackPhaseTimeout, phase)
	}

	a.enqueuePersist(snapshot)
}

func (a *GameActor) currentPhase() engine.Phase {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Phase
}

// enqueuePersist keeps only the newest snapshot. An older one is strictly less
// useful, so replacing it costs nothing and bounds the write rate.
func (a *GameActor) enqueuePersist(snapshot *engine.GameState) {
	select {
	case a.persistQueue <- snapshot:
		return
	default:
	}
	select {
	case <-a.persistQueue:
	default:
	}
	select {
	case a.persistQueue <- snapshot:
	default:
	}
}

func (a *GameActor) persistLoop() {
	defer a.persistWG.Done()
	for snapshot := range a.persistQueue {
		if a.OnPersist != nil {
			a.OnPersist(snapshot)
		}
	}
}

// TimerPhase reports which phase the currently armed timeout belongs to, or
// the empty phase when nothing is scheduled.
func (a *GameActor) TimerPhase() engine.Phase {
	a.timerMu.Lock()
	defer a.timerMu.Unlock()
	return a.timerPhase
}

// hasPendingTimer reports whether a timeout for this phase is still waiting to
// fire. A game that has never armed one is reported as pending, because its
// clock has not started yet and the absence is expected.
func (a *GameActor) hasPendingTimer(phase engine.Phase) bool {
	a.timerMu.Lock()
	defer a.timerMu.Unlock()
	if !a.timerEver {
		return true
	}
	return a.timer != nil && !a.timerFired && a.timerPhase == phase
}

func (a *GameActor) resetTimer(d time.Duration, phase engine.Phase) {
	a.stopTimers()
	a.timerMu.Lock()
	defer a.timerMu.Unlock()
	a.timerPhase = phase
	a.timerFired = false
	a.timerEver = true
	a.timer = time.AfterFunc(d, func() {
		a.timerMu.Lock()
		a.timerFired = true
		a.timerMu.Unlock()
		a.Send(engine.TimeoutEvent{Phase: phase})
	})
}

func (a *GameActor) stopTimers() {
	a.timerMu.Lock()
	defer a.timerMu.Unlock()
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	for _, wt := range a.warningTimers {
		wt.Stop()
	}
	a.warningTimers = nil
	a.timerPhase = ""
	a.timerFired = false
}

func (a *GameActor) addWarningTimer(d time.Duration, phase engine.Phase, secondsLeft int) {
	a.timerMu.Lock()
	defer a.timerMu.Unlock()
	t := time.AfterFunc(d, func() {
		a.Send(engine.TimerWarningEvent{Phase: phase, SecondsLeft: secondsLeft})
	})
	a.warningTimers = append(a.warningTimers, t)
}

// Supervisor manages all active game actors
type Supervisor struct {
	mu     sync.RWMutex
	games  map[engine.GameID]*GameActor
	outbox chan OutgoingMessage
	cancel map[engine.GameID]context.CancelFunc
	wg     sync.WaitGroup
}

func NewSupervisor(outbox chan OutgoingMessage) *Supervisor {
	return &Supervisor{
		games:  make(map[engine.GameID]*GameActor),
		outbox: outbox,
		cancel: make(map[engine.GameID]context.CancelFunc),
	}
}

func (s *Supervisor) StartGame(state *engine.GameState) *GameActor {
	s.mu.Lock()
	defer s.mu.Unlock()

	ga := NewGameActor(state, s.outbox)
	s.games[state.ID] = ga

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel[state.ID] = cancel

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ga.Run(ctx)
		s.mu.Lock()
		// Only remove this actor. Game IDs are derived from the chat, so a
		// replacement game can already be registered under the same key by the
		// time this one finishes shutting down, and an unconditional delete
		// would unregister the live game instead.
		if s.games[state.ID] == ga {
			delete(s.games, state.ID)
			delete(s.cancel, state.ID)
		}
		s.mu.Unlock()
		log.Printf("Game %s ended", state.ID)
	}()

	return ga
}

// Shutdown stops every running game and waits for their final snapshots to be
// written. Without this a redeploy loses whatever happened since the last
// persist, because the pending snapshot only lives in memory.
func (s *Supervisor) Shutdown(timeout time.Duration) {
	s.mu.Lock()
	for _, cancel := range s.cancel {
		cancel()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		log.Printf("shutdown: timed out waiting for games to stop")
	}
}

func (s *Supervisor) GetGame(id engine.GameID) *GameActor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.games[id]
}

func (s *Supervisor) StopGame(id engine.GameID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.cancel[id]; ok {
		cancel()
	}
}

func (s *Supervisor) ActiveGames() []engine.GameID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]engine.GameID, 0, len(s.games))
	for id := range s.games {
		ids = append(ids, id)
	}
	return ids
}
