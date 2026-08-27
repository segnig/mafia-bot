package telegram

import (
	"sync"

	"github.com/segni/mafia-bot/internal/engine"
)

// roleDeliveryTracker decides when the role-assignment phase is finished.
//
// The reducer cannot know this: it emits the role DMs and then a
// RolesDeliveredEffect, but handing a message to the sender is not the same as
// Telegram accepting it. Acknowledging on enqueue would advance the game to
// Night 1 before any DM was attempted, which in turn means a failed role DM
// arrives too late to be recognised as a role-delivery failure and gets
// misfiled as an ordinary disconnect.
//
// So the transport counts the outstanding DMs of each deal and only reports
// completion once they have all resolved. Batches are keyed by the engine's
// deal number rather than an identifier of our own, so what the tracker
// considers one batch is exactly what the reducer considers one deal.
type roleDeliveryTracker struct {
	mu      sync.Mutex
	batches map[engine.GameID]*roleBatch
	// live is the generation currently allowed to deal roles in this chat.
	// Zero means the game has ended. A queued role DM that arrives after
	// forget must not open a batch the next game would inherit.
	live map[engine.GameID]uint64
	next uint64
}

type roleBatch struct {
	deal    int
	pending int
	sealed  bool // every DM of this deal has been dispatched
	failed  bool // at least one player never received their role
}

func newRoleDeliveryTracker() *roleDeliveryTracker {
	return &roleDeliveryTracker{
		batches: make(map[engine.GameID]*roleBatch),
		live:    make(map[engine.GameID]uint64),
	}
}

// begin registers a game as able to deal roles, discarding anything left over
// from a previous game in the same chat. The returned generation is what
// forget must present to actually release the slot — an older game's cleanup
// must not wipe a rematch that has already claimed it.
func (t *roleDeliveryTracker) begin(gid engine.GameID) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	gen := t.next
	delete(t.batches, gid)
	t.live[gid] = gen
	return gen
}

// track registers one outstanding role DM of the given deal. It reports false
// when the game is no longer running, in which case the DM is not worth sending.
func (t *roleDeliveryTracker) track(gid engine.GameID, deal int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.live[gid] == 0 {
		return false
	}

	batch, ok := t.batches[gid]
	if !ok || batch.deal != deal {
		batch = &roleBatch{deal: deal}
		t.batches[gid] = batch
	}
	batch.pending++
	return true
}

// resolve records the outcome of one role DM. It reports true when this was the
// last outstanding message of a sealed deal, along with whether every DM in it
// was delivered.
func (t *roleDeliveryTracker) resolve(gid engine.GameID, deal int, err error) (done, clean bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	batch, ok := t.batches[gid]
	if !ok || batch.deal != deal {
		return false, false // a straggler from a superseded deal
	}
	batch.pending--
	if err != nil {
		batch.failed = true
	}
	if batch.sealed && batch.pending <= 0 {
		delete(t.batches, gid)
		return true, !batch.failed
	}
	return false, false
}

// seal marks that the reducer has finished emitting this deal. If the DMs have
// already all resolved, the deal completes here.
func (t *roleDeliveryTracker) seal(gid engine.GameID, deal int) (done, clean bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	batch, ok := t.batches[gid]
	if !ok {
		// No role DMs were tracked at all; nothing to wait for.
		return t.live[gid] != 0, true
	}
	if batch.deal != deal {
		return false, false // the seal of a deal that has been superseded
	}
	batch.sealed = true
	if batch.pending <= 0 {
		delete(t.batches, gid)
		return true, !batch.failed
	}
	return false, false
}

func (t *roleDeliveryTracker) forget(gid engine.GameID, gen uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.live[gid] != gen {
		return
	}
	delete(t.batches, gid)
	delete(t.live, gid)
}
