package telegram

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/segni/mafia-bot/internal/engine"
)

func TestBatchCompletesOnlyAfterEveryDMResolves(t *testing.T) {
	tr := newRoleDeliveryTracker()

	ids := []uint64{tr.track("g1"), tr.track("g1"), tr.track("g1")}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("DMs emitted together must belong to one batch")
		}
	}

	if done, _ := tr.seal("g1"); done {
		t.Fatal("batch completed while three DMs were still in flight")
	}
	if done, _ := tr.resolve("g1", ids[0], nil); done {
		t.Error("batch completed after only one of three DMs")
	}
	if done, _ := tr.resolve("g1", ids[1], nil); done {
		t.Error("batch completed after only two of three DMs")
	}

	done, clean := tr.resolve("g1", ids[2], nil)
	if !done {
		t.Fatal("batch did not complete after its last DM resolved")
	}
	if !clean {
		t.Error("a batch where every DM succeeded should be clean")
	}
}

func TestBatchCompletesWhenSealArrivesLast(t *testing.T) {
	tr := newRoleDeliveryTracker()

	id := tr.track("g1")
	if done, _ := tr.resolve("g1", id, nil); done {
		t.Fatal("an unsealed batch must not complete; more DMs may still be coming")
	}

	done, clean := tr.seal("g1")
	if !done || !clean {
		t.Errorf("seal should complete an already-drained batch, got done=%v clean=%v", done, clean)
	}
}

// A failed role DM means the reducer will redeal, so the batch must not report
// itself clean — reporting completion would start the night with a player who
// never learned their role.
func TestFailedDMMakesBatchUnclean(t *testing.T) {
	tr := newRoleDeliveryTracker()

	a, b := tr.track("g1"), tr.track("g1")
	tr.seal("g1")
	tr.resolve("g1", a, errors.New("bot was blocked by the user"))

	done, clean := tr.resolve("g1", b, nil)
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

	old := tr.track("g1")
	tr.seal("g1")

	fresh := tr.track("g1")
	if fresh == old {
		t.Fatal("a redeal must start a new batch")
	}

	if done, _ := tr.resolve("g1", old, nil); done {
		t.Error("a callback from the superseded batch completed the new one")
	}

	tr.seal("g1")
	if done, clean := tr.resolve("g1", fresh, nil); !done || !clean {
		t.Errorf("the new batch should complete on its own callback, got done=%v clean=%v", done, clean)
	}
}

func TestSealWithNoTrackedDMsCompletes(t *testing.T) {
	tr := newRoleDeliveryTracker()
	if done, clean := tr.seal("g1"); !done || !clean {
		t.Error("sealing a batch with nothing to wait for should complete immediately")
	}
}

func TestBatchesAreIndependentPerGame(t *testing.T) {
	tr := newRoleDeliveryTracker()

	g1 := tr.track("g1")
	g2 := tr.track("g2")
	tr.seal("g1")
	tr.seal("g2")

	if done, _ := tr.resolve("g1", g1, nil); !done {
		t.Error("g1 should complete on its own DM")
	}
	if done, _ := tr.resolve("g2", g2, nil); !done {
		t.Error("g2 should complete independently of g1")
	}
}

func TestForgetDropsPendingBatch(t *testing.T) {
	tr := newRoleDeliveryTracker()
	id := tr.track("g1")
	tr.forget("g1")
	if done, _ := tr.resolve("g1", id, nil); done {
		t.Error("a forgotten batch must not complete")
	}
}

// Callbacks arrive on sender worker goroutines. Run with -race.
func TestTrackerIsConcurrencySafe(t *testing.T) {
	tr := newRoleDeliveryTracker()

	var wg sync.WaitGroup
	completions := make(chan bool, 100)

	for g := 0; g < 20; g++ {
		gid := engine.GameID(fmt.Sprintf("game-%d", g))
		var ids []uint64
		for i := 0; i < 5; i++ {
			ids = append(ids, tr.track(gid))
		}
		wg.Add(1)
		go func(ids []uint64) {
			defer wg.Done()
			for _, id := range ids {
				if done, clean := tr.resolve(gid, id, nil); done {
					completions <- clean
				}
			}
		}(ids)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if done, clean := tr.seal(gid); done {
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
