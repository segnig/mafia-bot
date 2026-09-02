package telegram

import (
	"sync"

	"github.com/segni/mafia-bot/internal/engine"
)

// boardTracker remembers the message IDs of the messages the bot keeps
// rewriting in place — the live vote board, lobby card, and lobby settings
// panel.
//
// This is purely presentational state. If an ID is missing the caller posts a
// fresh message instead, so losing it after a restart costs nothing.
type boardTracker struct {
	mu             sync.Mutex
	vote           map[engine.GameID]int
	lobby          map[engine.GameID]int
	lobbySettings  map[engine.GameID]int
}

func newBoardTracker() *boardTracker {
	return &boardTracker{
		vote:          make(map[engine.GameID]int),
		lobby:         make(map[engine.GameID]int),
		lobbySettings: make(map[engine.GameID]int),
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

func (b *boardTracker) setLobby(id engine.GameID, messageID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lobby[id] = messageID
}

func (b *boardTracker) getLobby(id engine.GameID) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	messageID, ok := b.lobby[id]
	return messageID, ok
}

func (b *boardTracker) setLobbySettings(id engine.GameID, messageID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lobbySettings[id] = messageID
}

func (b *boardTracker) getLobbySettings(id engine.GameID) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	messageID, ok := b.lobbySettings[id]
	return messageID, ok
}

func (b *boardTracker) clearLobbySettings(id engine.GameID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.lobbySettings, id)
}

// forget releases everything held for a finished game.
func (b *boardTracker) forget(id engine.GameID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.vote, id)
	delete(b.lobby, id)
	delete(b.lobbySettings, id)
}
