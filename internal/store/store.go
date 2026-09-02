package store

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/stats"
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

	// Player records survive individual games and drive /stats, the
	// leaderboards, and achievements.
	LoadPlayerStats(playerID engine.PlayerID) (*stats.PlayerStats, error)
	SavePlayerStats(s *stats.PlayerStats) error
	// TopPlayers returns the best records overall, or within one chat when
	// chatID is non-zero.
	TopPlayers(chatID int64, limit int) ([]*stats.PlayerStats, error)

	// Finished games are archived for /lastgame and the chat leaderboard.
	SaveGameRecord(record *stats.GameRecord) error
	LastGameRecord(chatID int64) (*stats.GameRecord, error)

	// Per-chat game settings, applied when a new lobby opens.
	LoadChatSettings(chatID int64) (*ChatSettings, error)
	SaveChatSettings(s *ChatSettings) error

	// Scheduled lobbies (one per chat).
	SaveScheduledGame(sg *ScheduledGame) error
	GetScheduledGame(chatID int64) (*ScheduledGame, error)
	DeleteScheduledGame(chatID int64) error
	ListDueScheduledGames(before time.Time) ([]*ScheduledGame, error)
	ListUpcomingScheduledGames(after time.Time) ([]*ScheduledGame, error)
}

// ChatSettings is a group's saved game configuration. Preset names the base
// ruleset and Overrides holds the individual toggles changed on top of it, so
// a future change to a preset's defaults still reaches groups that only
// tweaked one thing.
type ChatSettings struct {
	ChatID    int64             `json:"chat_id"`
	Preset    string            `json:"preset"`
	Overrides map[string]string `json:"overrides"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// NewChatSettings returns the default settings for a chat.
func NewChatSettings(chatID int64) *ChatSettings {
	return &ChatSettings{
		ChatID:    chatID,
		Preset:    engine.PresetClassic,
		Overrides: make(map[string]string),
	}
}

// Config builds the game config these settings describe.
func (s *ChatSettings) Config() engine.GameConfig {
	if s == nil {
		return engine.DefaultConfig()
	}
	cfg := engine.PresetConfig(s.Preset)
	for key, value := range s.Overrides {
		engine.ApplySetting(&cfg, key, value)
	}
	// A stored override could describe a game that cannot be played, so the
	// result is only used when it still validates.
	if engine.ValidateConfig(cfg) != nil {
		return engine.PresetConfig(s.Preset)
	}
	return cfg
}

// FromConfig builds stored chat settings from a resolved game config, keeping
// only the overrides that differ from the named preset's defaults.
func FromConfig(chatID int64, cfg engine.GameConfig) *ChatSettings {
	s := NewChatSettings(chatID)
	s.Preset = cfg.PresetName
	if s.Preset == "" {
		s.Preset = engine.PresetClassic
	}
	base := engine.PresetConfig(s.Preset)
	for _, setting := range engine.Settings() {
		cur := setting.Get(cfg)
		if cur != setting.Get(base) {
			s.Overrides[setting.Key] = cur
		}
	}
	s.UpdatedAt = time.Now()
	return s
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
	playerStats map[engine.PlayerID][]byte
	// chatPlayers records which players have finished a game in which chat,
	// so a per-chat leaderboard can be assembled.
	chatPlayers map[int64]map[engine.PlayerID]bool
	lastGames   map[int64][]byte
	settings    map[int64]*ChatSettings
	scheduled   map[int64]*ScheduledGame
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		games:       make(map[engine.GameID][]byte),
		waitlists:   make(map[int64][]engine.PlayerID),
		dmConfirmed: make(map[engine.PlayerID]bool),
		cooldowns:   make(map[string]time.Time),
		playerStats: make(map[engine.PlayerID][]byte),
		chatPlayers: make(map[int64]map[engine.PlayerID]bool),
		lastGames:   make(map[int64][]byte),
		settings:    make(map[int64]*ChatSettings),
		scheduled:   make(map[int64]*ScheduledGame),
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

// Records are stored as JSON so the in-memory store hands out independent
// copies, exactly as the Mongo store does. Sharing a pointer would let a
// caller mutate the stored record by accident.

func (m *MemoryStore) LoadPlayerStats(playerID engine.PlayerID) (*stats.PlayerStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.playerStats[playerID]
	if !ok {
		return stats.NewPlayerStats(playerID), nil
	}
	var s stats.PlayerStats
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal player stats: %w", err)
	}
	return &s, nil
}

func (m *MemoryStore) SavePlayerStats(s *stats.PlayerStats) error {
	if s == nil {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal player stats: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.playerStats[s.PlayerID] = data
	return nil
}

func (m *MemoryStore) TopPlayers(chatID int64, limit int) ([]*stats.PlayerStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []*stats.PlayerStats
	for id, data := range m.playerStats {
		if chatID != 0 && !m.chatPlayers[chatID][id] {
			continue
		}
		var s stats.PlayerStats
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		all = append(all, &s)
	}

	ranked := stats.Leaderboard(all)
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

func (m *MemoryStore) SaveGameRecord(record *stats.GameRecord) error {
	if record == nil {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal game record: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastGames[record.ChatID] = data
	if m.chatPlayers[record.ChatID] == nil {
		m.chatPlayers[record.ChatID] = make(map[engine.PlayerID]bool)
	}
	for _, p := range record.Players {
		m.chatPlayers[record.ChatID][p.ID] = true
	}
	return nil
}

func (m *MemoryStore) LastGameRecord(chatID int64) (*stats.GameRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.lastGames[chatID]
	if !ok {
		return nil, nil
	}
	var record stats.GameRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("unmarshal game record: %w", err)
	}
	return &record, nil
}

func (m *MemoryStore) LoadChatSettings(chatID int64) (*ChatSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.settings[chatID]
	if !ok {
		return NewChatSettings(chatID), nil
	}
	copied := *s
	copied.Overrides = make(map[string]string, len(s.Overrides))
	for k, v := range s.Overrides {
		copied.Overrides[k] = v
	}
	return &copied, nil
}

func (m *MemoryStore) SaveChatSettings(s *ChatSettings) error {
	if s == nil {
		return nil
	}
	copied := *s
	copied.UpdatedAt = time.Now()
	copied.Overrides = make(map[string]string, len(s.Overrides))
	for k, v := range s.Overrides {
		copied.Overrides[k] = v
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[s.ChatID] = &copied
	return nil
}

func (m *MemoryStore) SaveScheduledGame(sg *ScheduledGame) error {
	if sg == nil {
		return nil
	}
	copied := *sg
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduled[sg.ChatID] = &copied
	return nil
}

func (m *MemoryStore) GetScheduledGame(chatID int64) (*ScheduledGame, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sg, ok := m.scheduled[chatID]
	if !ok {
		return nil, nil
	}
	copied := *sg
	return &copied, nil
}

func (m *MemoryStore) DeleteScheduledGame(chatID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.scheduled, chatID)
	return nil
}

func (m *MemoryStore) ListDueScheduledGames(before time.Time) ([]*ScheduledGame, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var due []*ScheduledGame
	for _, sg := range m.scheduled {
		if !sg.ScheduledAt.After(before) {
			copied := *sg
			due = append(due, &copied)
		}
	}
	return due, nil
}

func (m *MemoryStore) ListUpcomingScheduledGames(after time.Time) ([]*ScheduledGame, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var upcoming []*ScheduledGame
	for _, sg := range m.scheduled {
		if sg.ScheduledAt.After(after) {
			copied := *sg
			upcoming = append(upcoming, &copied)
		}
	}
	return upcoming, nil
}
