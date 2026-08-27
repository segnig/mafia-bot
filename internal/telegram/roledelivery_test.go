package telegram

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/segni/mafia-bot/internal/engine"
)

// trackAll registers n DMs of one deal and fails the test if the game is not
// live, which every test here sets up first.
func trackAll(t *testing.T, tr *roleDeliveryTracker, gid engine.GameID, deal, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if !tr.track(gid, deal) {
			t.Fatalf("track refused a DM for live game %s", gid)
		}
	}
}

func TestBatchCompletesOnlyAfterEveryDMResolves(t *testing.T) {
	tr := newRoleDeliveryTracker()
	tr.begin("g1")
	trackAll(t, tr, "g1", 1, 3)

	if done, _ := tr.seal("g1", 1); done {
		t.Fatal("batch completed while three DMs were still in flight")
	}
	if done, _ := tr.resolve("g1", 1, nil); done {
		t.Error("batch completed after only one of three DMs")
	}
	if done, _ := tr.resolve("g1", 1, nil); done {
		t.Error("batch completed after only two of three DMs")
	}

	done, clean := tr.resolve("g1", 1, nil)
	if !done {
		t.Fatal("batch did not complete after its last DM resolved")
	}
	if !clean {
		t.Error("a batch where every DM succeeded should be clean")
	}
}

func TestBatchCompletesWhenSealArrivesLast(t *testing.T) {
	tr := newRoleDeliveryTracker()
	tr.begin("g1")

	trackAll(t, tr, "g1", 1, 1)
	if done, _ := tr.resolve("g1", 1, nil); done {
		t.Fatal("an unsealed batch must not complete; more DMs may still be coming")
	}

	done, clean := tr.seal("g1", 1)
	if !done || !clean {
		t.Errorf("seal should complete an already-drained batch, got done=%v clean=%v", done, clean)
	}
}

// The batch still completes, because the transport now needs to say so either
// way — but it reports the failure, which is what tells the reducer that the
// roles now in play are not the ones that were delivered.
func TestFailedDMMakesBatchUnclean(t *testing.T) {
	tr := newRoleDeliveryTracker()
	tr.begin("g1")

	trackAll(t, tr, "g1", 1, 2)
	tr.seal("g1", 1)
	tr.resolve("g1", 1, errors.New("bot was blocked by the user"))

	done, clean := tr.resolve("g1", 1, nil)
	if !done {
		t.Fatal("batch should still complete once all DMs resolve")
	}
	if clean {
		t.Error("a batch containing a failed DM must not be reported clean")
	}
}

// After a redeal, callbacks from the abandoned attempt must not complete the
// new batch early.
func TestStragglersFromSupersededBatchAreIgnored(t *testing.T) {
	tr := newRoleDeliveryTracker()
	tr.begin("g1")

	trackAll(t, tr, "g1", 1, 1)
	tr.seal("g1", 1)

	trackAll(t, tr, "g1", 2, 1)

	if done, _ := tr.resolve("g1", 1, nil); done {
		t.Error("a callback from the superseded deal completed the new one")
	}

	tr.seal("g1", 2)
	if done, clean := tr.resolve("g1", 2, nil); !done || !clean {
		t.Errorf("the new deal should complete on its own callback, got done=%v clean=%v", done, clean)
	}
}

// The seal of an abandoned deal arrives on the same path as its DMs and must
// not close the deal that replaced it.
func TestSealOfSupersededDealIsIgnored(t *testing.T) {
	tr := newRoleDeliveryTracker()
	tr.begin("g1")

	trackAll(t, tr, "g1", 5, 1)
	if done, _ := tr.seal("g1", 4); done {
		t.Error("the seal of an older deal completed the current one")
	}
	if done, _ := tr.resolve("g1", 5, nil); done {
		t.Error("the current deal is not sealed yet and must not complete")
	}
}

func TestSealWithNoTrackedDMsCompletes(t *testing.T) {
	tr := newRoleDeliveryTracker()
	tr.begin("g1")
	if done, clean := tr.seal("g1", 1); !done || !clean {
		t.Error("sealing a batch with nothing to wait for should complete immediately")
	}
}

// A game that is over must not be advanced by anything the tracker reports.
func TestSealAfterForgetDoesNotComplete(t *testing.T) {
	tr := newRoleDeliveryTracker()
	gen := tr.begin("g1")
	tr.forget("g1", gen)
	if done, _ := tr.seal("g1", 1); done {
		t.Error("sealing a forgotten game reported a completion")
	}
}

func TestBatchesAreIndependentPerGame(t *testing.T) {
	tr := newRoleDeliveryTracker()
	tr.begin("g1")
	tr.begin("g2")

	trackAll(t, tr, "g1", 1, 1)
	trackAll(t, tr, "g2", 1, 1)
	tr.seal("g1", 1)
	tr.seal("g2", 1)

	if done, _ := tr.resolve("g1", 1, nil); !done {
		t.Error("g1 should complete on its own DM")
	}
	if done, _ := tr.resolve("g2", 1, nil); !done {
		t.Error("g2 should complete independently of g1")
	}
}

func TestForgetDropsPendingBatch(t *testing.T) {
	tr := newRoleDeliveryTracker()
	gen := tr.begin("g1")
	trackAll(t, tr, "g1", 1, 1)
	tr.forget("g1", gen)
	if done, _ := tr.resolve("g1", 1, nil); done {
		t.Error("a forgotten batch must not complete")
	}
}

// A role DM can still be sitting in the sender queue when its game ends. Game
// IDs come from the chat, so without this the leftover DM would open a batch
// that the chat's next game inherits — starting it with a phantom outstanding
// delivery it can never resolve.
func TestLeftoverDMCannotOpenABatchForTheNextGame(t *testing.T) {
	tr := newRoleDeliveryTracker()
	old := tr.begin("g1")
	trackAll(t, tr, "g1", 1, 1)
	tr.forget("g1", old)

	if tr.track("g1", 1) {
		t.Fatal("a DM arriving after the game ended should be refused")
	}

	// The next game in the same chat starts clean.
	tr.begin("g1")
	trackAll(t, tr, "g1", 1, 1)
	if done, clean := tr.seal("g1", 1); done {
		t.Fatalf("the new game inherited a stale batch, got done=%v clean=%v", done, clean)
	}
	if done, clean := tr.resolve("g1", 1, nil); !done || !clean {
		t.Errorf("the new game should complete cleanly on its own DM, got done=%v clean=%v", done, clean)
	}
}

// Game IDs are the chat ID, so a rematch can claim the slot while the previous
// game's cleanup is still running. That cleanup must not wipe the rematch.
func TestForgetOfAnOlderGameLeavesTheRematchAlone(t *testing.T) {
	tr := newRoleDeliveryTracker()
	old := tr.begin("g1")
	tr.begin("g1")
	trackAll(t, tr, "g1", 1, 1)

	tr.forget("g1", old)

	if done, _ := tr.seal("g1", 1); done {
		t.Fatal("the rematch's DM is still in flight")
	}
	if done, clean := tr.resolve("g1", 1, nil); !done || !clean {
		t.Errorf("the rematch should still complete its own deal, got done=%v clean=%v", done, clean)
	}
}

// Callbacks arrive on sender worker goroutines. Run with -race.
func TestTrackerIsConcurrencySafe(t *testing.T) {
	tr := newRoleDeliveryTracker()

	var wg sync.WaitGroup
	completions := make(chan bool, 100)

	for g := 0; g < 20; g++ {
		gid := engine.GameID(fmt.Sprintf("game-%d", g))
		tr.begin(gid)
		trackAll(t, tr, gid, 1, 5)

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				if done, clean := tr.resolve(gid, 1, nil); done {
					completions <- clean
				}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if done, clean := tr.seal(gid, 1); done {
				completions <- clean
			}
		}()
	}

	wg.Wait()
	close(completions)

	count := 0
	for clean := range completions {
		count++
		if !clean {
			t.Error("no DM failed, so every completion should be clean")
		}
	}
	if count != 20 {
		t.Errorf("expected exactly one completion per game, got %d", count)
	}
}
