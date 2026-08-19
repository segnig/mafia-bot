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

type GameActor struct {
	state         *engine.GameState
	inbox         chan engine.Event
	outbox        chan OutgoingMessage
	timer         *time.Timer
	warningTimers []*time.Timer
	mu            sync.Mutex
	done          chan struct{}

	OnPersist func(*engine.GameState)
}

func NewGameActor(state *engine.GameState, outbox chan OutgoingMessage) *GameActor {
	return &GameActor{
		state:  state,
		inbox:  make(chan engine.Event, 64),
		outbox: outbox,
		done:   make(chan struct{}),
	}
}

func (a *GameActor) Send(ev engine.Event) {
	select {
	case a.inbox <- ev:
	case <-a.done:
	}
}

func (a *GameActor) State() *engine.GameState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *GameActor) Run(ctx context.Context) {
	defer close(a.done)

	for {
		select {
		case ev := <-a.inbox:
			a.mu.Lock()
			newState, effects := engine.Reduce(a.state, ev)
			a.state = newState
			a.mu.Unlock()

			if a.OnPersist != nil {
				a.OnPersist(a.state)
			}

			for _, eff := range effects {
				switch e := eff.(type) {
				case engine.SetTimerEffect:
					a.resetTimer(e.Duration, e.Phase)
				case engine.SetWarningTimerEffect:
					a.addWarningTimer(e.Duration, e.Phase, e.SecondsLeft)
				default:
					a.outbox <- OutgoingMessage{Effect: eff}
				}
			}

			if a.state.Phase == engine.PhaseGameOver {
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

func (a *GameActor) resetTimer(d time.Duration, phase engine.Phase) {
	if a.timer != nil {
		a.timer.Stop()
	}
	for _, wt := range a.warningTimers {
		wt.Stop()
	}
	a.warningTimers = nil
	a.timer = time.AfterFunc(d, func() {
		a.Send(engine.TimeoutEvent{Phase: phase})
	})
}

func (a *GameActor) addWarningTimer(d time.Duration, phase engine.Phase, secondsLeft int) {
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

	actor := NewGameActor(state, s.outbox)
	s.games[state.ID] = actor

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel[state.ID] = cancel

	go func() {
		actor.Run(ctx)
		s.mu.Lock()
		delete(s.games, state.ID)
		delete(s.cancel, state.ID)
		s.mu.Unlock()
		log.Printf("Game %s ended", state.ID)
	}()

	return actor
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
