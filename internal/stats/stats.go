// Package stats holds the durable player record that survives individual
// games: totals, streaks, per-role history, and unlocked achievements.
//
// Everything here is a pure transformation of an engine.GameSummary, so the
// numbers can be recomputed and tested without a database.
package stats

import (
	"fmt"
	"sort"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
)

// PlayerStats is one player's lifetime record across every game and group.
type PlayerStats struct {
	PlayerID engine.PlayerID `json:"player_id"`
	Username string          `json:"username"`
	Name     string          `json:"name"`

	GamesPlayed int `json:"games_played"`
	Wins        int `json:"wins"`
	Losses      int `json:"losses"`
	// Survivals counts games the player was still alive at the end of.
	Survivals int `json:"survivals"`
	Deaths    int `json:"deaths"`

	// CurrentStreak is consecutive wins; it resets on a loss but is left
	// alone by an aborted game, which had no result either way.
	CurrentStreak int `json:"current_streak"`
	BestStreak    int `json:"best_streak"`

	// Roles records how often each role was drawn and won with.
	Roles map[string]RoleRecord `json:"roles"`
	// Awards counts each end-of-game accolade the player has collected.
	Awards map[string]int `json:"awards"`
	// Achievements are permanent unlocks, keyed by achievement ID.
	Achievements map[string]time.Time `json:"achievements"`

	// Lifetime totals behind the achievements.
	TotalVotesOnEvil   int `json:"total_votes_on_evil"`
	TotalSaves         int `json:"total_saves"`
	TotalKills         int `json:"total_kills"`
	TotalCorrectChecks int `json:"total_correct_checks"`
	TotalWhispers      int `json:"total_whispers"`

	FirstPlayed time.Time `json:"first_played"`
	LastPlayed  time.Time `json:"last_played"`
}

// RoleRecord is how a player has fared in one particular role.
type RoleRecord struct {
	Played int `json:"played"`
	Won    int `json:"won"`
}

// NewPlayerStats returns an empty record with its maps ready to use.
func NewPlayerStats(id engine.PlayerID) *PlayerStats {
	return &PlayerStats{
		PlayerID:     id,
		Roles:        make(map[string]RoleRecord),
		Awards:       make(map[string]int),
		Achievements: make(map[string]time.Time),
	}
}

// ensureMaps makes a record loaded from storage safe to write to. A document
// stored before a field existed decodes as a nil map.
func (s *PlayerStats) ensureMaps() {
	if s.Roles == nil {
		s.Roles = make(map[string]RoleRecord)
	}
	if s.Awards == nil {
		s.Awards = make(map[string]int)
	}
	if s.Achievements == nil {
		s.Achievements = make(map[string]time.Time)
	}
}

// WinRate is the share of decided games the player has won, 0 to 1.
func (s *PlayerStats) WinRate() float64 {
	decided := s.Wins + s.Losses
	if decided == 0 {
		return 0
	}
	return float64(s.Wins) / float64(decided)
}

// SurvivalRate is the share of games the player finished alive, 0 to 1.
func (s *PlayerStats) SurvivalRate() float64 {
	if s.GamesPlayed == 0 {
		return 0
	}
	return float64(s.Survivals) / float64(s.GamesPlayed)
}

// FavouriteRole is the role the player has drawn most often. Names are visited
// in sorted order so a tie resolves the same way on every call.
func (s *PlayerStats) FavouriteRole() (string, RoleRecord, bool) {
	names := make([]string, 0, len(s.Roles))
	for name := range s.Roles {
		names = append(names, name)
	}
	sort.Strings(names)

	best, bestRecord := "", RoleRecord{}
	for _, name := range names {
		if record := s.Roles[name]; record.Played > bestRecord.Played {
			best, bestRecord = name, record
		}
	}
	if bestRecord.Played == 0 {
		return "", RoleRecord{}, false
	}
	return best, bestRecord, true
}

// Apply folds one finished game into the player's record and returns the
// achievements newly unlocked by it.
//
// An aborted game — cancelled by the host, or voided because everybody left —
// counts as neither a win nor a loss and leaves the streak untouched, so
// abandoning a losing position cannot be used to protect a record.
func (s *PlayerStats) Apply(summary engine.GameSummary, result engine.PlayerResult) []Achievement {
	s.ensureMaps()

	if result.Username != "" {
		s.Username = result.Username
	}
	if result.Name != "" {
		s.Name = result.Name
	}
	if s.FirstPlayed.IsZero() {
		s.FirstPlayed = summary.EndedAt
	}
	s.LastPlayed = summary.EndedAt

	s.GamesPlayed++
	if result.Survived {
		s.Survivals++
	} else {
		s.Deaths++
	}

	if !summary.Aborted {
		if result.Won {
			s.Wins++
			s.CurrentStreak++
			if s.CurrentStreak > s.BestStreak {
				s.BestStreak = s.CurrentStreak
			}
		} else {
			s.Losses++
			s.CurrentStreak = 0
		}
	}

	if result.Role != engine.RoleUnassigned {
		record := s.Roles[string(result.Role)]
		record.Played++
		if result.Won && !summary.Aborted {
			record.Won++
		}
		s.Roles[string(result.Role)] = record
	}

	for _, award := range summary.Awards {
		if award.PlayerID == result.ID {
			s.Awards[award.Key]++
		}
	}

	s.TotalVotesOnEvil += result.Stats.VotesOnEvil
	s.TotalSaves += result.Stats.Saves
	s.TotalKills += result.Stats.Kills
	s.TotalCorrectChecks += result.Stats.CorrectChecks
	s.TotalWhispers += result.Stats.Whispers

	return s.unlock(summary, result)
}

// unlock evaluates every achievement and records the ones newly earned.
func (s *PlayerStats) unlock(summary engine.GameSummary, result engine.PlayerResult) []Achievement {
	var earned []Achievement
	for _, a := range Achievements() {
		if _, already := s.Achievements[a.ID]; already {
			continue
		}
		if a.Earned(s, summary, result) {
			s.Achievements[a.ID] = summary.EndedAt
			earned = append(earned, a)
		}
	}
	sort.Slice(earned, func(i, j int) bool { return earned[i].ID < earned[j].ID })
	return earned
}

// Rank is a coarse title derived from wins, so a leaderboard has something
// more evocative than a number.
func (s *PlayerStats) Rank() (string, string) {
	switch {
	case s.Wins >= 100:
		return "👑", "Legend"
	case s.Wins >= 50:
		return "💎", "Mastermind"
	case s.Wins >= 25:
		return "🏆", "Veteran"
	case s.Wins >= 10:
		return "⭐", "Regular"
	case s.Wins >= 3:
		return "🌱", "Apprentice"
	default:
		return "🔰", "Newcomer"
	}
}

// Score ranks players on a leaderboard. Wins drive it, win rate breaks ties
// among similar records, and a minimum game count keeps a single lucky game
// from topping the board.
func (s *PlayerStats) Score() float64 {
	const provisionalGames = 5
	confidence := float64(s.GamesPlayed) / float64(provisionalGames)
	if confidence > 1 {
		confidence = 1
	}
	return float64(s.Wins) + s.WinRate()*confidence*5 + float64(s.BestStreak)*0.5
}

// Leaderboard sorts players by score, highest first.
func Leaderboard(players []*PlayerStats) []*PlayerStats {
	out := append([]*PlayerStats(nil), players...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score() != out[j].Score() {
			return out[i].Score() > out[j].Score()
		}
		if out[i].Wins != out[j].Wins {
			return out[i].Wins > out[j].Wins
		}
		return out[i].PlayerID < out[j].PlayerID
	})
	return out
}

// DisplayName is the best label available for this player.
func (s *PlayerStats) DisplayName() string {
	if s.Username != "" {
		return "@" + s.Username
	}
	if s.Name != "" {
		return s.Name
	}
	return fmt.Sprintf("player %d", s.PlayerID)
}
