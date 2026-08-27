package stats

import (
	"sort"

	"github.com/segni/mafia-bot/internal/engine"
)

// Achievement is a permanent unlock. Earned is evaluated after a game has
// already been folded into the player's record, so it can read either the
// lifetime totals or the single game that just finished.
type Achievement struct {
	ID     string
	Emoji  string
	Title  string
	Detail string
	// Secret achievements are hidden from the list until unlocked, so there
	// is something left to discover.
	Secret bool
	Earned func(s *PlayerStats, summary engine.GameSummary, result engine.PlayerResult) bool
}

// achievementList is the full catalogue. Order here only affects the display
// order in /achievements.
var achievementList = []Achievement{
	{
		ID: "first_game", Emoji: "🎬", Title: "Welcome to Town",
		Detail: "Finish your first game.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.GamesPlayed >= 1
		},
	},
	{
		ID: "first_win", Emoji: "🥇", Title: "First Blood on the Board",
		Detail: "Win a game.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.Wins >= 1
		},
	},
	{
		ID: "hat_trick", Emoji: "🎩", Title: "Hat Trick",
		Detail: "Win three games in a row.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.BestStreak >= 3
		},
	},
	{
		ID: "unstoppable", Emoji: "🔥", Title: "Unstoppable",
		Detail: "Win seven games in a row.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.BestStreak >= 7
		},
	},
	{
		ID: "veteran", Emoji: "🎖️", Title: "Veteran",
		Detail: "Play fifty games.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.GamesPlayed >= 50
		},
	},
	{
		ID: "mafia_mastermind", Emoji: "🕴️", Title: "Mastermind",
		Detail: "Win five games on the mafia side.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return teamWins(s, engine.TeamMafia) >= 5
		},
	},
	{
		ID: "town_pillar", Emoji: "🏛️", Title: "Pillar of the Town",
		Detail: "Win ten games for the town.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return teamWins(s, engine.TeamTown) >= 10
		},
	},
	{
		ID: "sole_survivor", Emoji: "🕯️", Title: "Sole Survivor",
		Detail: "Be the only player left alive at the end of a game.",
		Earned: func(_ *PlayerStats, summary engine.GameSummary, result engine.PlayerResult) bool {
			if !result.Survived {
				return false
			}
			alive := 0
			for _, p := range summary.Players {
				if p.Survived {
					alive++
				}
			}
			return alive == 1
		},
	},
	{
		ID: "untouchable", Emoji: "🧿", Title: "Untouchable",
		Detail: "Win ten games without dying.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, result engine.PlayerResult) bool {
			return s.Survivals >= 10 && s.Wins >= 10
		},
	},
	{
		ID: "jesters_delight", Emoji: "🃏", Title: "Jester's Delight",
		Detail: "Get yourself lynched as the Jester.",
		Earned: func(_ *PlayerStats, _ engine.GameSummary, result engine.PlayerResult) bool {
			return result.Role == engine.RoleJester && result.Won
		},
	},
	{
		ID: "lone_wolf", Emoji: "🩸", Title: "Lone Wolf",
		Detail: "Win a game as the Serial Killer.",
		Earned: func(_ *PlayerStats, _ engine.GameSummary, result engine.PlayerResult) bool {
			return result.Role == engine.RoleSerialKiller && result.Won
		},
	},
	{
		ID: "guardian", Emoji: "🛡️", Title: "Guardian",
		Detail: "Prevent ten killings across your games.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.TotalSaves >= 10
		},
	},
	{
		ID: "bloodhound", Emoji: "🔍", Title: "Bloodhound",
		Detail: "Correctly identify ten threats with investigations.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.TotalCorrectChecks >= 10
		},
	},
	{
		ID: "executioner", Emoji: "☠️", Title: "Executioner",
		Detail: "Personally eliminate twenty players.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.TotalKills >= 20
		},
	},
	{
		ID: "first_blood", Emoji: "🩹", Title: "Out of the Gate",
		Detail: "Be the first player to die in a game.",
		Earned: func(_ *PlayerStats, _ engine.GameSummary, result engine.PlayerResult) bool {
			return result.DiedFirst
		},
	},
	{
		ID: "night_one_victim", Emoji: "😵", Title: "Wrong Place, Wrong Night",
		Detail: "Die on the very first night.",
		Earned: func(_ *PlayerStats, _ engine.GameSummary, result engine.PlayerResult) bool {
			return !result.Survived && result.DiedOnDay == 1
		},
	},
	{
		ID: "mayor_gambit", Emoji: "📣", Title: "The Mayor's Gambit",
		Detail: "Win a game as the Mayor.",
		Earned: func(_ *PlayerStats, _ engine.GameSummary, result engine.PlayerResult) bool {
			return result.Role == engine.RoleMayor && result.Won
		},
	},
	{
		ID: "star_crossed", Emoji: "💔", Title: "Star-Crossed",
		Detail: "Die of grief because your lover was killed.",
		Secret: true,
		Earned: func(_ *PlayerStats, _ engine.GameSummary, result engine.PlayerResult) bool {
			return result.DeathCause == engine.CauseGrief
		},
	},
	{
		ID: "martyr", Emoji: "🕊️", Title: "Martyr",
		Detail: "Die taking a bullet for someone else as the Bodyguard.",
		Secret: true,
		Earned: func(_ *PlayerStats, _ engine.GameSummary, result engine.PlayerResult) bool {
			return result.DeathCause == engine.CauseBodyguard
		},
	},
	{
		ID: "polyglot", Emoji: "🎭", Title: "Man of Many Faces",
		Detail: "Play eight different roles.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return len(s.Roles) >= 8
		},
	},
	{
		ID: "conspirator", Emoji: "🤫", Title: "Conspirator",
		Detail: "Send fifty whispers.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.TotalWhispers >= 50
		},
	},
	{
		ID: "sharpshooter", Emoji: "🎯", Title: "Sharpshooter",
		Detail: "Vote to lynch a genuine threat twenty-five times.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			return s.TotalVotesOnEvil >= 25
		},
	},
	{
		ID: "decorated", Emoji: "🏅", Title: "Decorated",
		Detail: "Collect ten end-of-game awards.",
		Earned: func(s *PlayerStats, _ engine.GameSummary, _ engine.PlayerResult) bool {
			total := 0
			for _, n := range s.Awards {
				total += n
			}
			return total >= 10
		},
	},
}

// Achievements returns the catalogue, ordered for display.
func Achievements() []Achievement {
	out := append([]Achievement(nil), achievementList...)
	return out
}

// AchievementByID looks up a single achievement.
func AchievementByID(id string) (Achievement, bool) {
	for _, a := range achievementList {
		if a.ID == id {
			return a, true
		}
	}
	return Achievement{}, false
}

// teamWins counts wins across every role belonging to a faction.
func teamWins(s *PlayerStats, team engine.Team) int {
	total := 0
	for name, record := range s.Roles {
		if engine.RoleTeam(engine.Role(name)) == team {
			total += record.Won
		}
	}
	return total
}

// UnlockedList returns the player's achievements in display order, alongside
// the ones still locked. Secret achievements are omitted until earned.
func (s *PlayerStats) UnlockedList() (unlocked, locked []Achievement) {
	s.ensureMaps()
	for _, a := range Achievements() {
		if _, ok := s.Achievements[a.ID]; ok {
			unlocked = append(unlocked, a)
			continue
		}
		if !a.Secret {
			locked = append(locked, a)
		}
	}
	sort.SliceStable(unlocked, func(i, j int) bool {
		return s.Achievements[unlocked[i].ID].Before(s.Achievements[unlocked[j].ID])
	})
	return unlocked, locked
}
