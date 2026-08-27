package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
)

// won builds a finished-game summary containing a single result, which is all
// most of these tests need.
func won(role engine.Role, winner bool, adjust ...func(*engine.PlayerResult)) (engine.GameSummary, engine.PlayerResult) {
	result := engine.PlayerResult{
		ID:       7,
		Username: "player",
		Name:     "Player",
		Role:     role,
		Team:     engine.RoleTeam(role),
		Survived: winner,
		Won:      winner,
	}
	for _, fn := range adjust {
		fn(&result)
	}
	summary := engine.GameSummary{
		GameID:  "g1",
		ChatID:  -100,
		EndedAt: time.Now(),
		Days:    3,
		Winner:  engine.RoleTeam(role),
		Players: []engine.PlayerResult{result},
	}
	return summary, result
}

// ---------------------------------------------------------------------------
// Totals and streaks
// ---------------------------------------------------------------------------

func TestApplyCountsWinsLossesAndStreaks(t *testing.T) {
	s := NewPlayerStats(7)

	for i := 0; i < 3; i++ {
		summary, result := won(engine.RoleVillager, true)
		s.Apply(summary, result)
	}
	if s.Wins != 3 || s.CurrentStreak != 3 || s.BestStreak != 3 {
		t.Fatalf("after three wins: wins=%d streak=%d best=%d", s.Wins, s.CurrentStreak, s.BestStreak)
	}

	summary, result := won(engine.RoleVillager, false)
	s.Apply(summary, result)

	if s.Losses != 1 {
		t.Errorf("losses = %d, want 1", s.Losses)
	}
	if s.CurrentStreak != 0 {
		t.Errorf("a loss should reset the streak, got %d", s.CurrentStreak)
	}
	if s.BestStreak != 3 {
		t.Errorf("the best streak should be remembered, got %d", s.BestStreak)
	}
	if s.GamesPlayed != 4 {
		t.Errorf("games played = %d, want 4", s.GamesPlayed)
	}
}

// A game cancelled by the host had no result, so it must not count either way.
// Otherwise a player could protect a streak by abandoning a losing game.
func TestAbortedGameIsNeitherWinNorLoss(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleVillager, true)
	s.Apply(summary, result)

	aborted, abortedResult := won(engine.RoleVillager, false)
	aborted.Aborted = true
	aborted.Winner = ""
	s.Apply(aborted, abortedResult)

	if s.Wins != 1 || s.Losses != 0 {
		t.Errorf("aborted game changed the record: wins=%d losses=%d", s.Wins, s.Losses)
	}
	if s.CurrentStreak != 1 {
		t.Errorf("an aborted game should leave the streak alone, got %d", s.CurrentStreak)
	}
	if s.GamesPlayed != 2 {
		t.Errorf("an aborted game still counts as played, got %d", s.GamesPlayed)
	}
	if record := s.Roles[string(engine.RoleVillager)]; record.Won != 1 {
		t.Errorf("aborted game credited a role win: %+v", record)
	}
}

func TestWinRateIgnoresAbortedGames(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleVillager, true)
	s.Apply(summary, result)
	aborted, abortedResult := won(engine.RoleVillager, false)
	aborted.Aborted = true
	s.Apply(aborted, abortedResult)

	if got := s.WinRate(); got != 1 {
		t.Errorf("win rate = %v, want 1 (the aborted game is not a decided game)", got)
	}
}

func TestRatesAreZeroWithNoGames(t *testing.T) {
	s := NewPlayerStats(7)

	if s.WinRate() != 0 || s.SurvivalRate() != 0 {
		t.Error("an empty record should report zero rather than divide by zero")
	}
	if _, _, ok := s.FavouriteRole(); ok {
		t.Error("an empty record has no favourite role")
	}
}

func TestPerRoleRecordTracksPlayedAndWon(t *testing.T) {
	s := NewPlayerStats(7)

	summary, result := won(engine.RoleDetective, true)
	s.Apply(summary, result)
	summary, result = won(engine.RoleDetective, false)
	s.Apply(summary, result)
	summary, result = won(engine.RoleMafia, true)
	s.Apply(summary, result)

	detective := s.Roles[string(engine.RoleDetective)]
	if detective.Played != 2 || detective.Won != 1 {
		t.Errorf("detective record = %+v, want 2 played 1 won", detective)
	}
	role, record, ok := s.FavouriteRole()
	if !ok || role != string(engine.RoleDetective) || record.Played != 2 {
		t.Errorf("favourite role = %q %+v", role, record)
	}
}

// An undealt role must not create a phantom entry, or a lobby that never
// started would show up as a role the player has "played".
func TestUnassignedRoleIsNotRecorded(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleUnassigned, false)
	summary.Aborted = true

	s.Apply(summary, result)

	if len(s.Roles) != 0 {
		t.Errorf("an unassigned role should not be recorded, got %v", s.Roles)
	}
}

func TestApplyAccumulatesLifetimeCounters(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleDoctor, true, func(r *engine.PlayerResult) {
		r.Stats = engine.PlayerGameStats{
			Saves: 2, Kills: 1, CorrectChecks: 3, Whispers: 4, VotesOnEvil: 5,
		}
	})

	s.Apply(summary, result)
	s.Apply(summary, result)

	if s.TotalSaves != 4 || s.TotalKills != 2 || s.TotalCorrectChecks != 6 ||
		s.TotalWhispers != 8 || s.TotalVotesOnEvil != 10 {
		t.Errorf("counters did not accumulate: %+v", s)
	}
}

func TestAwardsAreCountedForTheRightPlayer(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleVillager, true)
	summary.Awards = []engine.Award{
		{Key: "guardian", PlayerID: 7},
		{Key: "reaper", PlayerID: 8},
	}

	s.Apply(summary, result)

	if s.Awards["guardian"] != 1 {
		t.Error("the player's own award should be counted")
	}
	if s.Awards["reaper"] != 0 {
		t.Error("another player's award must not be counted")
	}
}

// A record stored before a field existed decodes with nil maps, and writing to
// a nil map panics.
func TestRecordLoadedWithNilMapsIsSafeToUse(t *testing.T) {
	s := &PlayerStats{PlayerID: 7}
	summary, result := won(engine.RoleVillager, true)

	s.Apply(summary, result)

	if s.Roles == nil || s.Awards == nil || s.Achievements == nil {
		t.Fatal("Apply should have initialised the maps")
	}
	if _, _, ok := s.FavouriteRole(); !ok {
		t.Error("the role should have been recorded")
	}
	if unlocked, _ := s.UnlockedList(); len(unlocked) == 0 {
		t.Error("achievements should still unlock on a migrated record")
	}
}

func TestStatsSurviveAJSONRoundTrip(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleMayor, true)
	s.Apply(summary, result)

	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PlayerStats
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.Wins != s.Wins || decoded.GamesPlayed != s.GamesPlayed {
		t.Errorf("totals changed across the round trip: %+v", decoded)
	}
	if len(decoded.Achievements) != len(s.Achievements) {
		t.Errorf("achievements changed: %d vs %d", len(decoded.Achievements), len(s.Achievements))
	}
	if decoded.Roles[string(engine.RoleMayor)].Played != 1 {
		t.Error("the role record did not survive")
	}
}

// ---------------------------------------------------------------------------
// Achievements
// ---------------------------------------------------------------------------

func TestAchievementCatalogueIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Achievements() {
		if a.ID == "" || a.Title == "" || a.Detail == "" || a.Emoji == "" {
			t.Errorf("achievement %q is incomplete: %+v", a.ID, a)
		}
		if a.Earned == nil {
			t.Errorf("achievement %q has no unlock condition", a.ID)
		}
		if seen[a.ID] {
			t.Errorf("duplicate achievement ID %q", a.ID)
		}
		seen[a.ID] = true

		if found, ok := AchievementByID(a.ID); !ok || found.Title != a.Title {
			t.Errorf("AchievementByID(%q) did not round trip", a.ID)
		}
	}
	if len(seen) == 0 {
		t.Fatal("the catalogue is empty")
	}
}

func TestFirstGameAndFirstWinUnlockOnce(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleVillager, true)

	earned := s.Apply(summary, result)

	ids := map[string]bool{}
	for _, a := range earned {
		ids[a.ID] = true
	}
	if !ids["first_game"] || !ids["first_win"] {
		t.Errorf("expected the opening achievements, got %v", ids)
	}

	// A second game must not re-award what is already unlocked.
	again := s.Apply(summary, result)
	for _, a := range again {
		if a.ID == "first_game" || a.ID == "first_win" {
			t.Errorf("%q was awarded twice", a.ID)
		}
	}
}

func TestSecretAchievementsUnlockOnTheirCause(t *testing.T) {
	tests := []struct {
		id    string
		cause string
	}{
		{"star_crossed", engine.CauseGrief},
		{"martyr", engine.CauseBodyguard},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			s := NewPlayerStats(7)
			summary, result := won(engine.RoleBodyguard, false, func(r *engine.PlayerResult) {
				r.Survived = false
				r.DeathCause = tt.cause
			})

			earned := s.Apply(summary, result)

			found := false
			for _, a := range earned {
				if a.ID == tt.id {
					found = true
				}
			}
			if !found {
				t.Errorf("dying of %q should unlock %q", tt.cause, tt.id)
			}
		})
	}
}

// A different death must not unlock a cause-specific achievement.
func TestSecretAchievementsDoNotUnlockOnAnyDeath(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleVillager, false, func(r *engine.PlayerResult) {
		r.Survived = false
		r.DeathCause = engine.CauseMafia
	})

	earned := s.Apply(summary, result)

	for _, a := range earned {
		if a.ID == "star_crossed" || a.ID == "martyr" {
			t.Errorf("a plain mafia kill unlocked %q", a.ID)
		}
	}
}

func TestSoleSurvivorNeedsToBeTheOnlyOneAlive(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleSurvivor, true)
	summary.Players = []engine.PlayerResult{
		result,
		{ID: 8, Role: engine.RoleMafia, Survived: false},
	}

	earned := s.Apply(summary, result)

	found := false
	for _, a := range earned {
		if a.ID == "sole_survivor" {
			found = true
		}
	}
	if !found {
		t.Error("being the last one breathing should unlock sole_survivor")
	}
}

func TestSecretAchievementsStayHiddenUntilEarned(t *testing.T) {
	s := NewPlayerStats(7)

	unlocked, locked := s.UnlockedList()

	if len(unlocked) != 0 {
		t.Errorf("a fresh record has nothing unlocked, got %d", len(unlocked))
	}
	for _, a := range locked {
		if a.Secret {
			t.Errorf("secret achievement %q should not be listed while locked", a.ID)
		}
	}
}

func TestTeamWinsCountsAcrossRolesOfTheSameFaction(t *testing.T) {
	s := NewPlayerStats(7)
	s.Roles = map[string]RoleRecord{
		string(engine.RoleMafia):     {Played: 3, Won: 2},
		string(engine.RoleGodfather): {Played: 2, Won: 2},
		string(engine.RoleVillager):  {Played: 5, Won: 4},
	}

	if got := teamWins(s, engine.TeamMafia); got != 4 {
		t.Errorf("mafia wins = %d, want 4", got)
	}
	if got := teamWins(s, engine.TeamTown); got != 4 {
		t.Errorf("town wins = %d, want 4", got)
	}
}

// ---------------------------------------------------------------------------
// Ranking
// ---------------------------------------------------------------------------

// A single lucky game must not top the board over a long, strong record.
func TestLeaderboardDiscountsUnprovenRecords(t *testing.T) {
	lucky := NewPlayerStats(1)
	lucky.Wins, lucky.GamesPlayed = 1, 1
	proven := NewPlayerStats(2)
	proven.Wins, proven.Losses, proven.GamesPlayed = 8, 4, 12

	ranked := Leaderboard([]*PlayerStats{lucky, proven})

	if ranked[0].PlayerID != 2 {
		t.Errorf("the proven player should rank first, got player %d", ranked[0].PlayerID)
	}
}

func TestLeaderboardIsStableForIdenticalRecords(t *testing.T) {
	a := NewPlayerStats(5)
	a.Wins, a.GamesPlayed = 3, 5
	b := NewPlayerStats(3)
	b.Wins, b.GamesPlayed = 3, 5

	first := Leaderboard([]*PlayerStats{a, b})
	second := Leaderboard([]*PlayerStats{b, a})

	if first[0].PlayerID != second[0].PlayerID {
		t.Error("identical records should rank in a deterministic order")
	}
	if first[0].PlayerID != 3 {
		t.Errorf("the tiebreak should be the lower player ID, got %d", first[0].PlayerID)
	}
}

func TestLeaderboardDoesNotMutateItsInput(t *testing.T) {
	a := NewPlayerStats(1)
	a.Wins = 1
	b := NewPlayerStats(2)
	b.Wins = 10
	input := []*PlayerStats{a, b}

	Leaderboard(input)

	if input[0].PlayerID != 1 {
		t.Error("Leaderboard should sort a copy, not the caller's slice")
	}
}

func TestRankRisesWithWins(t *testing.T) {
	previous := ""
	for _, wins := range []int{0, 3, 10, 25, 50, 100} {
		s := NewPlayerStats(1)
		s.Wins = wins
		emoji, rank := s.Rank()
		if emoji == "" || rank == "" {
			t.Fatalf("%d wins produced no rank", wins)
		}
		if rank == previous {
			t.Errorf("%d wins did not advance past %q", wins, previous)
		}
		previous = rank
	}
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

func TestFormatPlayerCardHandlesAnEmptyRecord(t *testing.T) {
	if !strings.Contains(FormatPlayerCard(nil), "No games played yet") {
		t.Error("a nil record should render an invitation, not a crash")
	}
	if !strings.Contains(FormatPlayerCard(NewPlayerStats(7)), "No games played yet") {
		t.Error("a record with no games should render the same")
	}
}

func TestFormatPlayerCardShowsTheHeadlineNumbers(t *testing.T) {
	s := NewPlayerStats(7)
	for i := 0; i < 4; i++ {
		summary, result := won(engine.RoleDetective, i < 3)
		s.Apply(summary, result)
	}

	card := FormatPlayerCard(s)

	for _, want := range []string{"Games", "Wins", "Win rate", "Achievements", "Detective"} {
		if !strings.Contains(card, want) {
			t.Errorf("the stats card is missing %q:\n%s", want, card)
		}
	}
}

// Display names come from Telegram, so they can contain Markdown control
// characters that would otherwise break the message.
func TestFormatPlayerCardEscapesTheDisplayName(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleVillager, true)
	result.Username = ""
	result.Name = "*bold* _italic_ [x](y)"
	s.Apply(summary, result)

	card := FormatPlayerCard(s)

	if strings.Contains(card, "*bold*") {
		t.Errorf("the display name should be escaped:\n%s", card)
	}
}

func TestFormatLeaderboardHandlesAnEmptyBoard(t *testing.T) {
	if !strings.Contains(FormatLeaderboard("Top", nil, 10), "No games have been recorded") {
		t.Error("an empty leaderboard should say so")
	}
}

func TestFormatLeaderboardRespectsTheLimit(t *testing.T) {
	var players []*PlayerStats
	for i := 1; i <= 20; i++ {
		s := NewPlayerStats(engine.PlayerID(i))
		s.Name, s.Wins, s.GamesPlayed = "P", i, i
		players = append(players, s)
	}

	board := FormatLeaderboard("Top", players, 3)

	if got := strings.Count(board, "\n"); got > 8 {
		t.Errorf("a limit of 3 produced too many lines:\n%s", board)
	}
	if !strings.Contains(board, "🥇") {
		t.Error("the board should medal the top three")
	}
}

func TestFormatAchievementsListsBothStates(t *testing.T) {
	s := NewPlayerStats(7)
	summary, result := won(engine.RoleVillager, true)
	s.Apply(summary, result)

	text := FormatAchievements(s)

	if !strings.Contains(text, "Welcome to Town") {
		t.Error("an unlocked achievement should be listed")
	}
	if !strings.Contains(text, "Still to earn") {
		t.Error("locked achievements should be listed too")
	}
	if !strings.Contains(FormatAchievements(nil), "Nothing unlocked yet") {
		t.Error("a nil record should still render")
	}
}

func TestFormatUnlockDMScalesWithTheNumberEarned(t *testing.T) {
	if FormatUnlockDM(nil) != "" {
		t.Error("no unlocks means no message to send")
	}

	one, _ := AchievementByID("first_game")
	two, _ := AchievementByID("first_win")

	single := FormatUnlockDM([]Achievement{one})
	if !strings.Contains(single, "Achievement unlocked!") {
		t.Errorf("unexpected single-unlock message: %q", single)
	}
	multiple := FormatUnlockDM([]Achievement{one, two})
	if !strings.Contains(multiple, "2 achievements unlocked!") {
		t.Errorf("unexpected multi-unlock message: %q", multiple)
	}
}

func TestGameRecapRendersAStoredGame(t *testing.T) {
	if !strings.Contains(FormatGameRecap(nil), "No finished game on record") {
		t.Error("a chat with no history should say so")
	}

	summary := engine.GameSummary{
		GameID:     "g1",
		ChatID:     -100,
		StartedAt:  time.Now().Add(-20 * time.Minute),
		EndedAt:    time.Now(),
		Days:       4,
		Winner:     engine.TeamTown,
		WinnerDesc: "Town wins",
		Players: []engine.PlayerResult{
			{ID: 1, Name: "Ann", Role: engine.RoleDetective, Survived: true, Won: true},
			{ID: 2, Name: "*Bob*", Role: engine.RoleMafia, Survived: false},
		},
		Awards: []engine.Award{{Key: "bloodhound", Emoji: "🔍", Title: "Bloodhound", PlayerName: "Ann"}},
	}

	recap := FormatGameRecap(RecordFromSummary(summary))

	for _, want := range []string{"Town wins", "Ann", "Detective", "Bloodhound", "4 day(s)"} {
		if !strings.Contains(recap, want) {
			t.Errorf("the recap is missing %q:\n%s", want, recap)
		}
	}
	if strings.Contains(recap, "*Bob*") {
		t.Errorf("player names in a recap must be escaped:\n%s", recap)
	}
}

func TestRecordFromSummaryKeepsEveryPlayer(t *testing.T) {
	summary := engine.GameSummary{
		GameID: "g1",
		Days:   2,
		Players: []engine.PlayerResult{
			{ID: 1, Role: engine.RoleVillager, Won: true, Survived: true},
			{ID: 2, Role: engine.RoleMafia},
			{ID: 3, Role: engine.RoleDoctor},
		},
	}

	record := RecordFromSummary(summary)

	if len(record.Players) != 3 {
		t.Fatalf("expected 3 players in the record, got %d", len(record.Players))
	}
	if !record.Players[0].Won || record.Players[1].Won {
		t.Error("the win flags did not survive flattening")
	}
}

func TestGameRecordSurvivesAJSONRoundTrip(t *testing.T) {
	summary := engine.GameSummary{
		GameID:     "g1",
		ChatID:     -100,
		StartedAt:  time.Now().Add(-10 * time.Minute),
		EndedAt:    time.Now(),
		Days:       3,
		Winner:     engine.TeamMafia,
		WinnerDesc: "Mafia wins",
		Players:    []engine.PlayerResult{{ID: 1, Name: "Ann", Role: engine.RoleMafia, Won: true}},
		Timeline:   []engine.TimelineEntry{{Day: 1, Icon: "💀", Text: "Ann died"}},
	}

	encoded, err := json.Marshal(RecordFromSummary(summary))
	if err != nil {
		t.Fatal(err)
	}
	var decoded GameRecord
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.WinnerDesc != "Mafia wins" || decoded.Days != 3 {
		t.Errorf("headline fields changed: %+v", decoded)
	}
	if len(decoded.Timeline) != 1 || decoded.Timeline[0].Text != "Ann died" {
		t.Errorf("the timeline did not survive: %+v", decoded.Timeline)
	}
	if decoded.Duration <= 0 {
		t.Error("the stored duration should be preserved")
	}
}
