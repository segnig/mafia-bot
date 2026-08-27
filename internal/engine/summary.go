package engine

import (
	"fmt"
	"sort"
	"time"
)

// GameSummary is the durable record of a finished game. Everything outside the
// engine — history, player records, achievements — is derived from this, so the
// engine never needs to know how any of that is stored.
type GameSummary struct {
	GameID     GameID
	ChatID     int64
	StartedAt  time.Time
	EndedAt    time.Time
	Days       int
	Winner     Team
	WinnerDesc string
	// Aborted marks a game that ended without a real result: cancelled by the
	// host, or voided because everyone left. These do not count as losses.
	Aborted  bool
	Preset   string
	Players  []PlayerResult
	Awards   []Award
	Timeline []TimelineEntry
}

// PlayerResult is one player's outcome, plus the counters that feed awards and
// achievements.
type PlayerResult struct {
	ID         PlayerID
	Username   string
	Name       string
	Role       Role
	Team       Team
	Survived   bool
	Won        bool
	DiedOnDay  int
	DiedFirst  bool
	DeathCause string
	Stats      PlayerGameStats
}

// Death causes recorded on a PlayerResult.
const (
	CauseMafia        = "mafia"
	CauseSerialKiller = "serial_killer"
	CauseVigilante    = "vigilante"
	CauseLynch        = "lynch"
	CauseGrief        = "grief"
	CauseBodyguard    = "bodyguard"
	CauseKicked       = "kicked"
)

// Award is one end-of-game accolade.
type Award struct {
	Key        string
	Emoji      string
	Title      string
	PlayerID   PlayerID
	PlayerName string
	Detail     string
}

// Duration is how long the game actually ran, measured from the deal rather
// than from when the lobby opened.
func (s GameSummary) Duration() time.Duration {
	if s.StartedAt.IsZero() || s.EndedAt.IsZero() || s.EndedAt.Before(s.StartedAt) {
		return 0
	}
	return s.EndedAt.Sub(s.StartedAt)
}

// WonFor reports whether a given player is among the winners.
func (s GameSummary) WonFor(id PlayerID) bool {
	for _, p := range s.Players {
		if p.ID == id {
			return p.Won
		}
	}
	return false
}

// Result looks up one player's outcome.
func (s GameSummary) Result(id PlayerID) (PlayerResult, bool) {
	for _, p := range s.Players {
		if p.ID == id {
			return p, true
		}
	}
	return PlayerResult{}, false
}

// BuildGameSummary snapshots a finished game. It is a pure read of the state,
// so it can be called from the reducer without side effects.
func BuildGameSummary(gs *GameState) GameSummary {
	winner := Team("")
	desc := "The game ended without a winner."
	if gs.Winner != nil {
		winner = gs.Winner.Winner
		desc = gs.Winner.Description
	}

	// A game with no faction winner produced no real result, so nobody should
	// carry a loss for it.
	aborted := winner == ""

	firstDead := PlayerID(0)
	if len(gs.DeathOrder) > 0 {
		firstDead = gs.DeathOrder[0]
	}

	summary := GameSummary{
		GameID:     gs.ID,
		ChatID:     gs.ChatID,
		StartedAt:  gs.StartedAt,
		EndedAt:    time.Now(),
		Days:       gs.DayNumber,
		Winner:     winner,
		WinnerDesc: desc,
		Aborted:    aborted,
		Preset:     gs.Config.PresetName,
		Timeline:   append([]TimelineEntry(nil), gs.Timeline...),
	}
	if summary.StartedAt.IsZero() {
		summary.StartedAt = gs.CreatedAt
	}

	for _, p := range playersByJoinTime(gs) {
		summary.Players = append(summary.Players, PlayerResult{
			ID:         p.ID,
			Username:   p.Username,
			Name:       p.PlainName(),
			Role:       p.Role,
			Team:       RoleTeam(p.Role),
			Survived:   p.Alive,
			Won:        playerWon(gs, p, winner),
			DiedOnDay:  p.DiedOnDay,
			DiedFirst:  p.ID == firstDead && firstDead != 0,
			DeathCause: p.DeathCause,
			Stats:      p.Stats,
		})
	}

	summary.Awards = buildAwards(gs, summary)
	return summary
}

// playerWon decides whether a player shares in the result. Neutrals win on
// their own terms and are recorded in JesterWon as they qualify, so they are
// checked independently of the faction result.
func playerWon(gs *GameState, p *Player, winner Team) bool {
	for _, id := range gs.JesterWon {
		if id == p.ID {
			return true
		}
	}
	if winner == "" {
		return false
	}
	if RoleTeam(p.Role) == TeamNeutral {
		// A neutral who did not meet their own condition did not win, even if
		// their nominal team matched.
		return false
	}
	return RoleTeam(p.Role) == winner
}

// buildAwards picks the end-of-game accolades. Each award needs a clear winner
// to be handed out: ties and empty categories are skipped rather than resolved
// arbitrarily, because a made-up winner reads worse than no award.
func buildAwards(gs *GameState, summary GameSummary) []Award {
	var awards []Award

	add := func(key, emoji, title string, id PlayerID, detail string) {
		p, ok := gs.Players[id]
		if !ok {
			return
		}
		awards = append(awards, Award{
			Key: key, Emoji: emoji, Title: title,
			PlayerID: id, PlayerName: p.PlainName(), Detail: detail,
		})
	}

	if id, value, ok := topByStat(gs, func(s PlayerGameStats) int { return s.VotesOnEvil }); ok {
		add("sharpest_eye", "🎯", "Sharpest Eye", id,
			fmt.Sprintf("Voted to lynch a genuine threat %d time(s).", value))
	}
	if id, value, ok := topByStat(gs, func(s PlayerGameStats) int { return s.Saves }); ok {
		add("guardian", "🛡️", "Guardian Angel", id,
			fmt.Sprintf("Stopped %d killing(s).", value))
	}
	if id, value, ok := topByStat(gs, func(s PlayerGameStats) int { return s.Kills }); ok {
		add("reaper", "☠️", "The Reaper", id,
			fmt.Sprintf("Personally ended %d player(s).", value))
	}
	if id, value, ok := topByStat(gs, func(s PlayerGameStats) int { return s.Messages }); ok {
		add("loudest", "🗣️", "Loudest Voice", id,
			fmt.Sprintf("Sent %d messages while the town argued.", value))
	}
	if id, value, ok := topByStat(gs, func(s PlayerGameStats) int { return s.Whispers }); ok {
		add("schemer", "🤫", "The Schemer", id,
			fmt.Sprintf("Sent %d private whisper(s).", value))
	}
	if id, value, ok := topByStat(gs, func(s PlayerGameStats) int { return s.CorrectChecks }); ok {
		add("bloodhound", "🔍", "Bloodhound", id,
			fmt.Sprintf("Correctly identified %d threat(s).", value))
	}

	if len(gs.DeathOrder) > 0 {
		add("first_blood", "🩸", "First Blood", gs.DeathOrder[0], "The very first to fall.")
	}

	// A lone survivor is a better story than any counter.
	if alive := gs.AlivePlayers(); len(alive) == 1 {
		add("last_standing", "🕯️", "Last One Standing", alive[0].ID, "The only player still breathing.")
	}

	// Silence is its own kind of play, and worth naming.
	if id, ok := quietestSurvivor(gs, summary); ok {
		add("ghost", "😶", "Silent Type", id, "Barely said a word and lived to tell about it.")
	}

	return awards
}

// topByStat finds the single highest scorer for a counter. A tie or an all-zero
// field returns no winner.
func topByStat(gs *GameState, pick func(PlayerGameStats) int) (PlayerID, int, bool) {
	best, bestValue, ties := PlayerID(0), 0, 0
	for _, p := range playersByID(gs) {
		v := pick(p.Stats)
		if v <= 0 {
			continue
		}
		switch {
		case v > bestValue:
			best, bestValue, ties = p.ID, v, 1
		case v == bestValue:
			ties++
		}
	}
	if bestValue == 0 || ties != 1 {
		return 0, 0, false
	}
	return best, bestValue, true
}

// quietestSurvivor is the surviving player who said the least, provided they
// really were quiet and are uniquely so.
func quietestSurvivor(gs *GameState, summary GameSummary) (PlayerID, bool) {
	best, bestValue, ties := PlayerID(0), -1, 0
	for _, r := range summary.Players {
		if !r.Survived {
			continue
		}
		v := r.Stats.Messages
		switch {
		case bestValue < 0 || v < bestValue:
			best, bestValue, ties = r.ID, v, 1
		case v == bestValue:
			ties++
		}
	}
	if bestValue < 0 || bestValue > 2 || ties != 1 {
		return 0, false
	}
	return best, true
}

// SortedAwards returns awards in a stable display order.
func SortedAwards(awards []Award) []Award {
	out := append([]Award(nil), awards...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
