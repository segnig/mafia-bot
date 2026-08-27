package store

import (
	"sync"
	"testing"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/stats"
)

// MemoryStore must satisfy the whole interface, so a missing method is caught
// at compile time rather than when the bot boots without Mongo.
var _ Store = (*MemoryStore)(nil)

func TestSaveAndLoadGameState(t *testing.T) {
	s := NewMemoryStore()
	gs := engine.NewGameState("g1", -100, 1, engine.DefaultConfig())
	gs.Players[1] = &engine.Player{ID: 1, Username: "ann", Alive: true}

	gs.DealNumber = 3
	gs.Phase = engine.PhaseRoleAssign

	if err := s.Save(gs); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load("g1")
	if err != nil {
		t.Fatal(err)
	}

	if loaded.ID != gs.ID || len(loaded.Players) != 1 {
		t.Errorf("loaded state does not match: %+v", loaded)
	}
	if loaded.DealNumber != 3 {
		t.Errorf("DealNumber did not round-trip through storage, got %d", loaded.DealNumber)
	}
	if loaded.Phase != engine.PhaseRoleAssign {
		t.Errorf("Phase did not round-trip, got %s", loaded.Phase)
	}
	// A loaded state must be an independent copy, otherwise a live game and
	// its snapshot would share player pointers.
	loaded.Players[1].Username = "changed"
	again, _ := s.Load("g1")
	if again.Players[1].Username != "ann" {
		t.Error("mutating a loaded state changed the stored one")
	}
}

func TestListActiveSkipsFinishedGames(t *testing.T) {
	s := NewMemoryStore()
	live := engine.NewGameState("live", -100, 1, engine.DefaultConfig())
	done := engine.NewGameState("done", -101, 1, engine.DefaultConfig())
	done.Phase = engine.PhaseGameOver

	if err := s.Save(live); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(done); err != nil {
		t.Fatal(err)
	}

	active, err := s.ListActive()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0] != "live" {
		t.Errorf("expected only the live game, got %v", active)
	}
}

func TestJoinCooldownExpires(t *testing.T) {
	s := NewMemoryStore()
	if err := s.SetJoinCooldown(-100, 1); err != nil {
		t.Fatal(err)
	}

	held, err := s.HasJoinCooldown(-100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("the cooldown should be active immediately after being set")
	}

	// Without expiry a rejected player would be muted for the whole session.
	s.mu.Lock()
	s.cooldowns[cooldownKey(-100, 1)] = time.Now().Add(-time.Second)
	s.mu.Unlock()

	if held, _ := s.HasJoinCooldown(-100, 1); held {
		t.Error("an expired cooldown should no longer apply")
	}
}

// ---------------------------------------------------------------------------
// Player records
// ---------------------------------------------------------------------------

// A player with no history reads as an empty record rather than an error, so
// /stats works before the first game.
func TestLoadPlayerStatsReturnsAnEmptyRecord(t *testing.T) {
	s := NewMemoryStore()

	record, err := s.LoadPlayerStats(42)
	if err != nil {
		t.Fatal(err)
	}
	if record == nil {
		t.Fatal("expected an empty record, got nil")
	}
	if record.PlayerID != 42 || record.GamesPlayed != 0 {
		t.Errorf("unexpected empty record: %+v", record)
	}
	if record.Roles == nil || record.Achievements == nil {
		t.Error("an empty record should have usable maps")
	}
}

func TestSavedPlayerStatsAreIndependentCopies(t *testing.T) {
	s := NewMemoryStore()
	record := stats.NewPlayerStats(42)
	record.Wins = 3
	if err := s.SavePlayerStats(record); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.LoadPlayerStats(42)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Wins = 99

	again, _ := s.LoadPlayerStats(42)
	if again.Wins != 3 {
		t.Errorf("the stored record was mutated through a loaded copy: %d wins", again.Wins)
	}
}

func TestTopPlayersRanksAndLimits(t *testing.T) {
	s := NewMemoryStore()
	for i := 1; i <= 5; i++ {
		record := stats.NewPlayerStats(engine.PlayerID(i))
		record.Wins, record.GamesPlayed = i*2, i*3
		if err := s.SavePlayerStats(record); err != nil {
			t.Fatal(err)
		}
	}

	top, err := s.TopPlayers(0, 3)
	if err != nil {
		t.Fatal(err)
	}

	if len(top) != 3 {
		t.Fatalf("expected 3 records, got %d", len(top))
	}
	if top[0].PlayerID != 5 {
		t.Errorf("the strongest record should rank first, got player %d", top[0].PlayerID)
	}
	for i := 1; i < len(top); i++ {
		if top[i-1].Score() < top[i].Score() {
			t.Error("results are not ordered by score")
		}
	}
}

// A group's leaderboard should only contain players who have actually played
// there, which is what makes it a group leaderboard.
func TestTopPlayersScopedToOneChat(t *testing.T) {
	s := NewMemoryStore()
	for _, id := range []engine.PlayerID{1, 2, 3} {
		record := stats.NewPlayerStats(id)
		record.Wins, record.GamesPlayed = 5, 5
		if err := s.SavePlayerStats(record); err != nil {
			t.Fatal(err)
		}
	}
	err := s.SaveGameRecord(&stats.GameRecord{
		GameID: "g1", ChatID: -100,
		Players: []stats.RecordPlayer{{ID: 1}, {ID: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}

	scoped, err := s.TopPlayers(-100, 10)
	if err != nil {
		t.Fatal(err)
	}
	global, err := s.TopPlayers(0, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(scoped) != 2 {
		t.Errorf("the chat leaderboard should hold its two players, got %d", len(scoped))
	}
	if len(global) != 3 {
		t.Errorf("the global leaderboard should hold all three, got %d", len(global))
	}
	for _, record := range scoped {
		if record.PlayerID == 3 {
			t.Error("a player who never played here appeared in the chat leaderboard")
		}
	}
}

func TestLastGameRecordPerChat(t *testing.T) {
	s := NewMemoryStore()

	missing, err := s.LastGameRecord(-100)
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Error("a chat with no history should report no record, without an error")
	}

	first := &stats.GameRecord{GameID: "g1", ChatID: -100, WinnerDesc: "town"}
	second := &stats.GameRecord{GameID: "g2", ChatID: -100, WinnerDesc: "mafia"}
	other := &stats.GameRecord{GameID: "g3", ChatID: -200, WinnerDesc: "elsewhere"}
	for _, record := range []*stats.GameRecord{first, second, other} {
		if err := s.SaveGameRecord(record); err != nil {
			t.Fatal(err)
		}
	}

	latest, err := s.LastGameRecord(-100)
	if err != nil {
		t.Fatal(err)
	}
	if latest.GameID != "g2" {
		t.Errorf("expected the most recent game, got %q", latest.GameID)
	}
	elsewhere, _ := s.LastGameRecord(-200)
	if elsewhere.GameID != "g3" {
		t.Errorf("chats must not share history, got %q", elsewhere.GameID)
	}
}

// ---------------------------------------------------------------------------
// Chat settings
// ---------------------------------------------------------------------------

func TestChatSettingsDefaultToClassic(t *testing.T) {
	s := NewMemoryStore()

	settings, err := s.LoadChatSettings(-100)
	if err != nil {
		t.Fatal(err)
	}

	if settings.Preset != engine.PresetClassic {
		t.Errorf("default preset = %q, want %q", settings.Preset, engine.PresetClassic)
	}
	if settings.Overrides == nil {
		t.Error("overrides should be a usable map")
	}
	if err := engine.ValidateConfig(settings.Config()); err != nil {
		t.Errorf("the default config must be playable: %v", err)
	}
}

func TestChatSettingsRoundTripWithOverrides(t *testing.T) {
	s := NewMemoryStore()
	settings := NewChatSettings(-100)
	settings.Preset = engine.PresetSpeed
	settings.Overrides["lovers"] = "true"

	if err := s.SaveChatSettings(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadChatSettings(-100)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Preset != engine.PresetSpeed {
		t.Errorf("preset = %q, want %q", loaded.Preset, engine.PresetSpeed)
	}
	cfg := loaded.Config()
	if !cfg.EnableLovers {
		t.Error("the stored override was not applied")
	}
	if cfg.NightTimeoutSec != engine.PresetConfig(engine.PresetSpeed).NightTimeoutSec {
		t.Error("the preset's own timings were lost")
	}

	// Mutating a loaded copy must not reach into the store.
	loaded.Overrides["lovers"] = "false"
	again, _ := s.LoadChatSettings(-100)
	if again.Overrides["lovers"] != "true" {
		t.Error("the stored overrides were mutated through a loaded copy")
	}
}

// An override left over from an older build must be ignored rather than
// producing a config that cannot deal a game.
func TestChatSettingsIgnoreUnplayableOverrides(t *testing.T) {
	settings := NewChatSettings(-100)
	settings.Overrides["no_such_key"] = "true"
	settings.Overrides["night"] = "0"

	cfg := settings.Config()

	if err := engine.ValidateConfig(cfg); err != nil {
		t.Errorf("the resulting config must still be playable: %v", err)
	}
}

func TestNilChatSettingsYieldTheDefaultConfig(t *testing.T) {
	var settings *ChatSettings

	if err := engine.ValidateConfig(settings.Config()); err != nil {
		t.Errorf("nil settings should fall back to a playable default: %v", err)
	}
}

// The store is shared by the update loop and every game actor, so concurrent
// access must not race.
func TestMemoryStoreIsSafeForConcurrentUse(t *testing.T) {
	s := NewMemoryStore()
	var wg sync.WaitGroup

	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := engine.PlayerID(i)
			record := stats.NewPlayerStats(id)
			record.Wins = i
			_ = s.SavePlayerStats(record)
			_, _ = s.LoadPlayerStats(id)
			_, _ = s.TopPlayers(0, 5)
			_ = s.SaveChatSettings(NewChatSettings(int64(i)))
			_, _ = s.LoadChatSettings(int64(i))
			_ = s.SaveGameRecord(&stats.GameRecord{
				GameID: engine.GameID("g"), ChatID: int64(i),
				Players: []stats.RecordPlayer{{ID: id}},
			})
			_, _ = s.LastGameRecord(int64(i))
		}(i)
	}
	wg.Wait()

	top, err := s.TopPlayers(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 20 {
		t.Errorf("expected 20 records to survive, got %d", len(top))
	}
}
