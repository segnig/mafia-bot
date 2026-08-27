package telegram

import (
	"errors"
	"fmt"
	"testing"

	"github.com/segni/mafia-bot/internal/engine"
)

// deliveryLoop is the transport in miniature: it tracks each role DM the
// reducer emits, and feeds outcomes back in the same order the real handler
// does (failure first, then the batch-complete event).
type deliveryLoop struct {
	t       *testing.T
	gs      *engine.GameState
	tr      *roleDeliveryTracker
	pending []pendingDM
}

type pendingDM struct {
	gid  engine.GameID
	pid  engine.PlayerID
	deal int
}

func newDeliveryLoop(t *testing.T, n int) *deliveryLoop {
	t.Helper()
	gs := engine.NewGameState("g1", -100, 1, engine.DefaultConfig())
	for i := engine.PlayerID(1); i <= engine.PlayerID(n); i++ {
		gs.Players[i] = &engine.Player{
			ID: i, Username: fmt.Sprintf("p%d", i), Alive: true,
		}
	}
	gs, _ = engine.Reduce(gs, engine.GameCreatedEvent{})
	gs, effects := engine.Reduce(gs, engine.BeginEvent{PlayerID: 1})
	if gs.Phase != engine.PhaseRoleAssign {
		t.Fatalf("expected role_assign, got %s", gs.Phase)
	}

	l := &deliveryLoop{t: t, gs: gs, tr: newRoleDeliveryTracker()}
	l.tr.begin(gs.ID)
	l.apply(effects)
	return l
}

func (l *deliveryLoop) apply(effects []engine.SideEffect) {
	for _, eff := range effects {
		switch e := eff.(type) {
		case engine.SendRoleDMEffect:
			if l.tr.track(e.GameID, e.Deal) {
				l.pending = append(l.pending, pendingDM{e.GameID, e.PlayerID, e.Deal})
			}
		case engine.RolesDeliveredEffect:
			if done, _ := l.tr.seal(e.GameID, e.Deal); done {
				l.feed(engine.RolesDeliveredEvent{Deal: e.Deal})
			}
		}
	}
}

func (l *deliveryLoop) feed(ev engine.Event) {
	var effects []engine.SideEffect
	l.gs, effects = engine.Reduce(l.gs, ev)
	l.apply(effects)
}

// resolveOldest reports the oldest outstanding DM for pid, matching how two
// failures of the same player (old deal, then new deal) drain in send order.
func (l *deliveryLoop) resolveOldest(pid engine.PlayerID, err error) {
	l.t.Helper()
	for i, dm := range l.pending {
		if dm.pid != pid {
			continue
		}
		l.pending = append(l.pending[:i], l.pending[i+1:]...)
		if err != nil {
			l.feed(roleDMFailureEvent(pid, dm.deal, err))
		}
		if done, _ := l.tr.resolve(dm.gid, dm.deal, err); done {
			l.feed(engine.RolesDeliveredEvent{Deal: dm.deal})
		}
		return
	}
	l.t.Fatalf("no outstanding role DM for player %d", pid)
}

func (l *deliveryLoop) succeedAll() {
	for len(l.pending) > 0 {
		dm := l.pending[0]
		l.resolveOldest(dm.pid, nil)
	}
}

func blocked() error {
	return errors.New("Forbidden: bot was blocked by the user")
}

func queueFull() error {
	return errors.New("sender queue is full")
}

// Two players blocking the bot in one batch is the case that used to start
// Night 1 on the abandoned deal while the redeal's DMs were still in flight.
func TestTwoBlockedPlayersDoNotAdvanceOnTheAbandonedDeal(t *testing.T) {
	l := newDeliveryLoop(t, 7)
	firstDeal := l.gs.DealNumber

	l.resolveOldest(3, blocked())
	if _, still := l.gs.Players[3]; still {
		t.Fatal("blocked player 3 should have been removed and the roles redealt")
	}
	if l.gs.DealNumber == firstDeal {
		t.Fatal("a redeal should start a new deal")
	}
	if l.gs.Phase != engine.PhaseRoleAssign {
		t.Fatalf("still dealing, got %s", l.gs.Phase)
	}

	l.resolveOldest(5, blocked())
	if _, still := l.gs.Players[5]; !still {
		t.Error("a stale failure removed a player who had already been sent a fresh role")
	}

	l.succeedAll()
	if l.gs.Phase != engine.PhaseNight {
		t.Errorf("the current deal should start the night once its DMs land, got %s", l.gs.Phase)
	}
	if _, gone := l.gs.Players[3]; gone {
		t.Error("player 3 should stay out — they blocked the bot")
	}
	if _, ok := l.gs.Players[5]; !ok {
		t.Error("player 5 should still be in the game")
	}
}

func TestQueueFullDuringDealMarksThePlayerSilentAndStartsTheNight(t *testing.T) {
	l := newDeliveryLoop(t, 7)

	l.resolveOldest(4, queueFull())
	if l.gs.Phase != engine.PhaseRoleAssign {
		t.Fatalf("a transient failure should not eject anyone mid-deal, phase=%s", l.gs.Phase)
	}
	if !l.gs.Players[4].Disconnected {
		t.Error("a queue-full role DM should mark the player silent")
	}
	if _, ok := l.gs.Players[4]; !ok {
		t.Error("they must keep their seat — the failure did not prove them unreachable")
	}

	l.succeedAll()
	if l.gs.Phase != engine.PhaseNight {
		t.Errorf("the deal should still close, got %s", l.gs.Phase)
	}
	if l.gs.Players[4].CanAct() {
		t.Error("the silent player must not count toward night actions")
	}
}

func TestCleanDeliveryStartsTheNightForEveryone(t *testing.T) {
	l := newDeliveryLoop(t, 6)
	n := len(l.gs.Players)
	l.succeedAll()
	if l.gs.Phase != engine.PhaseNight {
		t.Errorf("got %s, want night", l.gs.Phase)
	}
	if len(l.gs.Players) != n {
		t.Errorf("nobody should have been removed, roster %d -> %d", n, len(l.gs.Players))
	}
}

func TestBlockedPlayerBelowMinimumReturnsToTheLobby(t *testing.T) {
	l := newDeliveryLoop(t, 5)
	l.resolveOldest(5, blocked())
	if l.gs.Phase != engine.PhaseLobby {
		t.Errorf("dropping below the minimum should return to the lobby, got %s", l.gs.Phase)
	}
	l.succeedAll()
	if l.gs.Phase != engine.PhaseLobby {
		t.Errorf("stragglers from the abandoned deal must not restart the night, got %s", l.gs.Phase)
	}
}
