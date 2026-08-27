package telegram

import (
	"sync"

	"github.com/segni/mafia-bot/internal/engine"
)

// boardTracker remembers the message IDs of the messages the bot keeps
// rewriting in place — the live vote board and the settings panel.
//
// This is purely presentational state. If an ID is missing the caller posts a
// fresh message instead, so losing it after a restart costs nothing.
type boardTracker struct {
	mu       sync.Mutex
	vote     map[engine.GameID]int
	settings map[int64]int
}

func newBoardTracker() *boardTracker {
	return &boardTracker{
		vote:     make(map[engine.GameID]int),
		settings: make(map[int64]int),
	}
}

func (b *boardTracker) setVote(id engine.GameID, messageID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.vote[id] = messageID
}

func (b *boardTracker) getVote(id engine.GameID) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	messageID, ok := b.vote[id]
	return messageID, ok
}

// clearVote drops the tracked board, which is what makes the next round post a
// fresh message rather than editing yesterday's vote.
func (b *boardTracker) clearVote(id engine.GameID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.vote, id)
}

func (b *boardTracker) setSettings(chatID int64, messageID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.settings[chatID] = messageID
}

func (b *boardTracker) getSettings(chatID int64) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	messageID, ok := b.settings[chatID]
	return messageID, ok
}

func (b *boardTracker) clearSettings(chatID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.settings, chatID)
}

// forget releases everything held for a finished game.
func (b *boardTracker) forget(id engine.GameID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.vote, id)
}
