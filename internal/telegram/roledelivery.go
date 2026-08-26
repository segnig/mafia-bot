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
// So the transport counts the outstanding DMs of each batch and only reports
// completion once they have all resolved.
type roleDeliveryTracker struct {
	mu      sync.Mutex
	batches map[engine.GameID]*roleBatch
	nextID  uint64
}

type roleBatch struct {
	id      uint64
	pending int
	sealed  bool // every DM of this batch has been dispatched
	failed  bool // at least one player never received their role
}

func newRoleDeliveryTracker() *roleDeliveryTracker {
	return &roleDeliveryTracker{batches: make(map[engine.GameID]*roleBatch)}
}

// track registers one outstanding role DM and returns the batch it belongs to.
// A redeal starts a fresh batch, so late callbacks from the previous attempt
// can be told apart and ignored.
func (t *roleDeliveryTracker) track(gid engine.GameID) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	batch, ok := t.batches[gid]
	if !ok || batch.sealed {
		t.nextID++
		batch = &roleBatch{id: t.nextID}
		t.batches[gid] = batch
	}
	batch.pending++
	return batch.id
}

// resolve records the outcome of one role DM. It reports true when this was the
// last outstanding message of a sealed batch, along with whether the batch
// completed cleanly.
func (t *roleDeliveryTracker) resolve(gid engine.GameID, id uint64, err error) (done, clean bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	batch, ok := t.batches[gid]
	if !ok || batch.id != id {
		return false, false // a straggler from a superseded batch
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

// seal marks that the reducer has finished emitting this batch. If the DMs have
// already all resolved, the batch completes here.
func (t *roleDeliveryTracker) seal(gid engine.GameID) (done, clean bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	batch, ok := t.batches[gid]
	if !ok {
		// No role DMs were tracked at all; nothing to wait for.
		return true, true
	}
	batch.sealed = true
	if batch.pending <= 0 {
		delete(t.batches, gid)
		return true, !batch.failed
	}
	return false, false
}

func (t *roleDeliveryTracker) forget(gid engine.GameID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.batches, gid)
}
