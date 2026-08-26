package store

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
)

// Store provides persistence for game states.
// This interface allows swapping between in-memory, Redis, or Postgres backends.
type Store interface {
	Save(state *engine.GameState) error
	Load(id engine.GameID) (*engine.GameState, error)
	Delete(id engine.GameID) error
	ListActive() ([]engine.GameID, error)

	// Waitlist management (per-chat, survives game boundaries)
	AddToWaitlist(chatID int64, playerID engine.PlayerID) error
	GetWaitlist(chatID int64) ([]engine.PlayerID, error)
	ClearWaitlist(chatID int64) error

	// DM confirmation tracking
	SetDMConfirmed(playerID engine.PlayerID) error
	IsDMConfirmed(playerID engine.PlayerID) (bool, error)

	// Join cooldown (per chat+player, for spam prevention)
	SetJoinCooldown(chatID int64, playerID engine.PlayerID) error
	HasJoinCooldown(chatID int64, playerID engine.PlayerID) (bool, error)
}

// joinCooldownTTL matches the TTL index used by the Mongo store.
const joinCooldownTTL = 30 * time.Second

// MemoryStore is an in-memory implementation for development and testing.
type MemoryStore struct {
	mu          sync.RWMutex
	games       map[engine.GameID][]byte
	waitlists   map[int64][]engine.PlayerID
	dmConfirmed map[engine.PlayerID]bool
	cooldowns   map[string]time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		games:       make(map[engine.GameID][]byte),
		waitlists:   make(map[int64][]engine.PlayerID),
		dmConfirmed: make(map[engine.PlayerID]bool),
		cooldowns:   make(map[string]time.Time),
	}
}

func (m *MemoryStore) Save(state *engine.GameState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal game state: %w", err)
	}
	m.games[state.ID] = data
	return nil
}

func (m *MemoryStore) Load(id engine.GameID) (*engine.GameState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.games[id]
	if !ok {
		return nil, fmt.Errorf("game %s not found", id)
	}
	var state engine.GameState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal game state: %w", err)
	}
	return &state, nil
}

func (m *MemoryStore) Delete(id engine.GameID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.games, id)
	return nil
}

func (m *MemoryStore) ListActive() ([]engine.GameID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]engine.GameID, 0, len(m.games))
	for id, data := range m.games {
		var state engine.GameState
		if err := json.Unmarshal(data, &state); err != nil {
			continue
		}
		if state.Phase.IsTerminal() {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *MemoryStore) AddToWaitlist(chatID int64, playerID engine.PlayerID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.waitlists[chatID] {
		if p == playerID {
			return nil
		}
	}
	m.waitlists[chatID] = append(m.waitlists[chatID], playerID)
	return nil
}

func (m *MemoryStore) GetWaitlist(chatID int64) ([]engine.PlayerID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.waitlists[chatID], nil
}

func (m *MemoryStore) ClearWaitlist(chatID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.waitlists, chatID)
	return nil
}

func (m *MemoryStore) SetDMConfirmed(playerID engine.PlayerID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dmConfirmed[playerID] = true
	return nil
}

func (m *MemoryStore) IsDMConfirmed(playerID engine.PlayerID) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dmConfirmed[playerID], nil
}

func (m *MemoryStore) SetJoinCooldown(chatID int64, playerID engine.PlayerID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cooldowns[cooldownKey(chatID, playerID)] = time.Now().Add(joinCooldownTTL)
	return nil
}

func (m *MemoryStore) HasJoinCooldown(chatID int64, playerID engine.PlayerID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := cooldownKey(chatID, playerID)
	expiry, ok := m.cooldowns[key]
	if !ok {
		return false, nil
	}
	// Without expiry a rejected player would be muted for the whole session.
	if time.Now().After(expiry) {
		delete(m.cooldowns, key)
		return false, nil
	}
	return true, nil
}

func cooldownKey(chatID int64, playerID engine.PlayerID) string {
	return fmt.Sprintf("%d:%d", chatID, playerID)
}
