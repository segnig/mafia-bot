package telegram

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/stats"
	"github.com/segni/mafia-bot/internal/store"
)

// ---------------------------------------------------------------------------
// Board tracking
// ---------------------------------------------------------------------------

func TestBoardTrackerRemembersAndReleases(t *testing.T) {
	tracker := newBoardTracker()

	if _, ok := tracker.getVote("g1"); ok {
		t.Error("an untracked game should report no board")
	}

	tracker.setVote("g1", 42)
	if id, ok := tracker.getVote("g1"); !ok || id != 42 {
		t.Errorf("getVote = %d, %v; want 42, true", id, ok)
	}

	// Clearing is what makes tomorrow's vote post a fresh message instead of
	// editing yesterday's.
	tracker.clearVote("g1")
	if _, ok := tracker.getVote("g1"); ok {
		t.Error("a cleared board should be forgotten")
	}
}

func TestBoardTrackerKeepsGamesAndChatsSeparate(t *testing.T) {
	tracker := newBoardTracker()
	tracker.setVote("g1", 1)
	tracker.setVote("g2", 2)
	tracker.setSettings(-100, 10)
	tracker.setSettings(-200, 20)

	tracker.forget("g1")
	tracker.clearSettings(-100)

	if _, ok := tracker.getVote("g1"); ok {
		t.Error("the finished game should be forgotten")
	}
	if id, ok := tracker.getVote("g2"); !ok || id != 2 {
		t.Error("another game's board was dropped")
	}
	if _, ok := tracker.getSettings(-100); ok {
		t.Error("the closed panel should be forgotten")
	}
	if id, ok := tracker.getSettings(-200); !ok || id != 20 {
		t.Error("another chat's panel was dropped")
	}
}

// The tracker is written from the effect dispatcher and read from callback
// handling, which run concurrently.
func TestBoardTrackerIsSafeForConcurrentUse(t *testing.T) {
	tracker := newBoardTracker()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := engine.GameID(fmt.Sprintf("g%d", i%5))
			tracker.setVote(id, i)
			tracker.getVote(id)
			tracker.setSettings(int64(i%5), i)
			tracker.getSettings(int64(i % 5))
			if i%7 == 0 {
				tracker.forget(id)
				tracker.clearSettings(int64(i % 5))
			}
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Keyboards
// ---------------------------------------------------------------------------

// callbackData flattens a keyboard into the list of callback payloads it can
// produce, which is what the router has to understand.
func callbackData(markup tgbotapi.InlineKeyboardMarkup) []string {
	var out []string
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil {
				out = append(out, *btn.CallbackData)
			}
		}
	}
	return out
}

func buttonLabels(markup tgbotapi.InlineKeyboardMarkup) []string {
	var out []string
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			out = append(out, btn.Text)
		}
	}
	return out
}

// Telegram silently rejects callback data over 64 bytes, which would leave a
// dead button in the chat.
func TestEveryCallbackFitsTelegramsLimit(t *testing.T) {
	gameID := engine.GameID("chat-1001234567890-1700000000")
	players := map[engine.PlayerID]*engine.Player{
		1: {ID: 1, Username: "ann", Alive: true},
		2: {ID: 2, Username: "bob", Alive: true},
	}
	targets := []engine.PlayerID{1, 2}

	keyboards := map[string]tgbotapi.InlineKeyboardMarkup{
		"night":    buildNightActionKeyboard(gameID, targets, players, engine.ActionMafiaKill),
		"vote":     buildVotingKeyboard(gameID, targets, players, true, map[engine.PlayerID]int{1: 2}),
		"join":     buildJoinButton(gameID),
		"reaction": buildReactionBar(gameID),
		"rematch":  buildRematchButton(-1001234567890),
		"settings": buildSettingsKeyboard(-1001234567890, engine.DefaultConfig()),
	}

	for name, markup := range keyboards {
		data := callbackData(markup)
		if len(data) == 0 {
			t.Errorf("%s keyboard has no buttons", name)
		}
		for _, payload := range data {
			if len(payload) > 64 {
				t.Errorf("%s keyboard callback %q is %d bytes, over Telegram's 64-byte limit",
					name, payload, len(payload))
			}
			if !strings.Contains(payload, ":") {
				t.Errorf("%s keyboard callback %q has no route prefix", name, payload)
			}
		}
	}
}

// Every prefix a keyboard emits must be one the router knows, or the button
// does nothing when tapped.
func TestEveryKeyboardPrefixIsRouted(t *testing.T) {
	routed := map[string]bool{
		"night": true, "vote": true, "join": true, "lobby": true,
		"info": true, "rematch": true, "board": true, "recap": true,
		"cfg": true, "react": true,
	}

	players := map[engine.PlayerID]*engine.Player{1: {ID: 1, Username: "ann", Alive: true}}
	all := []tgbotapi.InlineKeyboardMarkup{
		buildNightActionKeyboard("g1", []engine.PlayerID{1}, players, engine.ActionMafiaKill),
		buildVotingKeyboard("g1", []engine.PlayerID{1}, players, true, nil),
		buildJoinButton("g1"),
		buildReactionBar("g1"),
		buildRematchButton(-100),
		buildSettingsKeyboard(-100, engine.DefaultConfig()),
	}

	for _, markup := range all {
		for _, payload := range callbackData(markup) {
			prefix := strings.SplitN(payload, ":", 2)[0]
			if !routed[prefix] {
				t.Errorf("callback %q uses prefix %q, which handleCallback does not route", payload, prefix)
			}
		}
	}
}

func TestVotingKeyboardShowsRunningCounts(t *testing.T) {
	players := map[engine.PlayerID]*engine.Player{
		1: {ID: 1, Username: "ann", Alive: true},
		2: {ID: 2, Username: "bob", Alive: true},
	}

	markup := buildVotingKeyboard("g1", []engine.PlayerID{1, 2}, players, true,
		map[engine.PlayerID]int{1: 3})
	labels := strings.Join(buttonLabels(markup), "|")

	if !strings.Contains(labels, "ann · 3") {
		t.Errorf("a candidate with votes should show the count, got %q", labels)
	}
	if strings.Contains(labels, "bob · ") {
		t.Errorf("a candidate with no votes should show no count, got %q", labels)
	}
}

func TestVotingKeyboardOmitsSkipWhenDisallowed(t *testing.T) {
	players := map[engine.PlayerID]*engine.Player{1: {ID: 1, Username: "ann", Alive: true}}

	with := buildVotingKeyboard("g1", []engine.PlayerID{1}, players, true, nil)
	without := buildVotingKeyboard("g1", []engine.PlayerID{1}, players, false, nil)

	if !strings.Contains(strings.Join(buttonLabels(with), "|"), "Skip") {
		t.Error("the skip option should be offered when allowed")
	}
	if strings.Contains(strings.Join(buttonLabels(without), "|"), "Skip") {
		t.Error("the skip option must not appear when the group disabled it")
	}
}

// A target that is not in the player map would otherwise render a blank button.
func TestKeyboardsSkipUnknownTargets(t *testing.T) {
	players := map[engine.PlayerID]*engine.Player{1: {ID: 1, Username: "ann", Alive: true}}

	night := buildNightActionKeyboard("g1", []engine.PlayerID{1, 99}, players, engine.ActionMafiaKill)
	vote := buildVotingKeyboard("g1", []engine.PlayerID{1, 99}, players, false, nil)

	if got := len(callbackData(night)); got != 1 {
		t.Errorf("night keyboard rendered %d buttons for one known target", got)
	}
	if got := len(callbackData(vote)); got != 1 {
		t.Errorf("voting keyboard rendered %d buttons for one known target", got)
	}
}

// The panel is generated from the settings registry, so every knob must be
// reachable and the active preset must be marked.
func TestSettingsPanelCoversEverySettingAndPreset(t *testing.T) {
	cfg := engine.PresetConfig(engine.PresetChaos)

	markup := buildSettingsKeyboard(-100, cfg)
	data := strings.Join(callbackData(markup), "|")
	labels := strings.Join(buttonLabels(markup), "|")

	for _, setting := range engine.Settings() {
		if !strings.Contains(data, ":set:"+setting.Key) {
			t.Errorf("the panel has no button for setting %q", setting.Key)
		}
		if !strings.Contains(labels, setting.Label) {
			t.Errorf("the panel does not label setting %q", setting.Key)
		}
	}
	for _, name := range engine.PresetNames() {
		if !strings.Contains(data, ":preset:"+name) {
			t.Errorf("the panel has no button for preset %q", name)
		}
	}
	if !strings.Contains(data, ":close:") {
		t.Error("the panel needs a way to close it")
	}

	activeLabel, _ := engine.PresetLabel(engine.PresetChaos)
	if !strings.Contains(labels, "▸ "+activeLabel) {
		t.Errorf("the active preset should be marked, got %q", labels)
	}
}

// Every toggle rendered on the panel must read back as on or off, so a button
// can never show an empty value.
func TestSettingsPanelRendersEveryValue(t *testing.T) {
	for _, name := range engine.PresetNames() {
		cfg := engine.PresetConfig(name)
		for _, label := range buttonLabels(buildSettingsKeyboard(-100, cfg)) {
			if strings.HasSuffix(label, "— ") {
				t.Errorf("preset %q left a settings button with no value: %q", name, label)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Edits
// ---------------------------------------------------------------------------

// Re-rendering a board that has not changed is a normal outcome of a live UI,
// and Telegram rejects it. Treating that as an error would fill the log with
// noise and trigger pointless retries.
func TestUnchangedEditIsNotTreatedAsAFailure(t *testing.T) {
	cases := []struct {
		err  string
		want bool
	}{
		{"Bad Request: message is not modified", true},
		{"Bad Request: message is not modified: specified new message content and reply markup are exactly the same", true},
		{"Bad Request: message to edit not found", false},
		{"Too Many Requests: retry after 3", false},
	}
	for _, tc := range cases {
		if got := isUnchangedEdit(errors.New(tc.err)); got != tc.want {
			t.Errorf("isUnchangedEdit(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// The classifiers are cheap to call and easy to reach with a nil error from a
// new code path, so none of them may panic on one.
func TestErrorClassifiersTolerateANilError(t *testing.T) {
	checks := map[string]func(error) bool{
		"isBotBlocked":    isBotBlocked,
		"isRateLimited":   isRateLimited,
		"isParseError":    isParseError,
		"isUnchangedEdit": isUnchangedEdit,
	}
	for name, fn := range checks {
		if fn(nil) {
			t.Errorf("%s(nil) should be false", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Recording results
// ---------------------------------------------------------------------------

func finishedSummary() engine.GameSummary {
	return engine.GameSummary{
		GameID:     "g1",
		ChatID:     -100,
		StartedAt:  time.Now().Add(-15 * time.Minute),
		EndedAt:    time.Now(),
		Days:       3,
		Winner:     engine.TeamTown,
		WinnerDesc: "Town wins",
		Players: []engine.PlayerResult{
			{ID: 1, Name: "Ann", Role: engine.RoleDetective, Team: engine.TeamTown, Survived: true, Won: true},
			{ID: 2, Name: "Bob", Role: engine.RoleMafia, Team: engine.TeamMafia},
			{ID: 3, Name: "Cid", Role: engine.RoleDoctor, Team: engine.TeamTown, Won: true},
		},
		Awards: []engine.Award{{Key: "bloodhound", Emoji: "🔍", Title: "Bloodhound", PlayerID: 1, PlayerName: "Ann"}},
	}
}

func TestRecordResultsArchivesTheGameAndUpdatesEveryone(t *testing.T) {
	st := store.NewMemoryStore()

	recordResults(st, nil, finishedSummary())

	record, err := st.LastGameRecord(-100)
	if err != nil {
		t.Fatal(err)
	}
	if record == nil || record.GameID != "g1" {
		t.Fatalf("the game was not archived: %+v", record)
	}
	if len(record.Players) != 3 {
		t.Errorf("the archive should hold every player, got %d", len(record.Players))
	}

	winner, err := st.LoadPlayerStats(1)
	if err != nil {
		t.Fatal(err)
	}
	if winner.Wins != 1 || winner.GamesPlayed != 1 {
		t.Errorf("the winner's record is wrong: %+v", winner)
	}
	if winner.Awards["bloodhound"] != 1 {
		t.Error("the award was not credited")
	}

	loser, err := st.LoadPlayerStats(2)
	if err != nil {
		t.Fatal(err)
	}
	if loser.Losses != 1 || loser.Wins != 0 {
		t.Errorf("the loser's record is wrong: %+v", loser)
	}
}

// A lobby that was cancelled before roles were dealt has nothing worth
// recording, and writing it would give everyone a phantom game.
func TestRecordResultsIgnoresAGameThatNeverStarted(t *testing.T) {
	st := store.NewMemoryStore()
	summary := finishedSummary()
	summary.Days = 0

	recordResults(st, nil, summary)

	if record, _ := st.LastGameRecord(-100); record != nil {
		t.Error("an unstarted game should not be archived")
	}
	if s, _ := st.LoadPlayerStats(1); s.GamesPlayed != 0 {
		t.Error("an unstarted game should not count towards anyone's record")
	}
}

func TestRecordResultsSkipsPlayersWithoutARole(t *testing.T) {
	st := store.NewMemoryStore()
	summary := finishedSummary()
	summary.Players = append(summary.Players, engine.PlayerResult{ID: 9, Name: "Late"})

	recordResults(st, nil, summary)

	if s, _ := st.LoadPlayerStats(9); s.GamesPlayed != 0 {
		t.Error("a player who never got a role should not be recorded")
	}
}

// The bot must survive a database that is refusing writes: the group's next
// game matters more than the record.
func TestRecordResultsSurvivesAFailingStore(t *testing.T) {
	st := &failingStore{MemoryStore: store.NewMemoryStore()}

	recordResults(st, nil, finishedSummary())

	if st.recordAttempts == 0 || st.statAttempts == 0 {
		t.Error("expected the failing store to have been called")
	}
}

// A game recorded twice — say a duplicated effect — must not double anyone's
// totals for the same result twice in a row through the same call path.
func TestRecordResultsIsAdditivePerCall(t *testing.T) {
	st := store.NewMemoryStore()
	summary := finishedSummary()

	recordResults(st, nil, summary)
	recordResults(st, nil, summary)

	s, _ := st.LoadPlayerStats(1)
	if s.GamesPlayed != 2 {
		t.Errorf("each call folds the game in once: games = %d, want 2", s.GamesPlayed)
	}
	if s.BestStreak != 2 {
		t.Errorf("consecutive wins should extend the streak, got %d", s.BestStreak)
	}
}

func TestLeaderboardReflectsRecordedGames(t *testing.T) {
	st := store.NewMemoryStore()

	recordResults(st, nil, finishedSummary())

	top, err := st.TopPlayers(-100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 3 {
		t.Fatalf("expected the three participants on the chat board, got %d", len(top))
	}
	if top[0].Wins == 0 {
		t.Error("a winner should rank above a loser")
	}
	rendered := stats.FormatLeaderboard("Top players here", top, 10)
	if !strings.Contains(rendered, "Ann") {
		t.Errorf("the rendered board should name the players:\n%s", rendered)
	}
}

// failingStore rejects every write, which is how a database outage looks from
// inside recordResults.
type failingStore struct {
	*store.MemoryStore
	recordAttempts int
	statAttempts   int
}

func (f *failingStore) SaveGameRecord(*stats.GameRecord) error {
	f.recordAttempts++
	return errors.New("database unavailable")
}

func (f *failingStore) SavePlayerStats(*stats.PlayerStats) error {
	f.statAttempts++
	return errors.New("database unavailable")
}
