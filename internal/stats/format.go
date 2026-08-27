package stats

import (
	"fmt"
	"strings"
	"time"

	"github.com/segni/mafia-bot/internal/engine"
)

const divider = "━━━━━━━━━━━━━━━━━━━━"

// FormatPlayerCard is the /stats reply for one player.
func FormatPlayerCard(s *PlayerStats) string {
	if s == nil || s.GamesPlayed == 0 {
		return "📊 *No games played yet.*\n\nJoin a game with /join and your record starts building itself."
	}

	emoji, rank := s.Rank()
	msg := fmt.Sprintf("📊 *%s*\n%s %s\n%s\n", engine.EscapeMD(s.DisplayName()), emoji, rank, divider)

	msg += fmt.Sprintf("🎮 Games: *%d*\n", s.GamesPlayed)
	msg += fmt.Sprintf("🏆 Wins: *%d*  ·  💀 Losses: *%d*\n", s.Wins, s.Losses)
	msg += fmt.Sprintf("📈 Win rate: *%s* %s\n", percent(s.WinRate()), engine.ProgressBar(s.Wins, maxInt(s.Wins+s.Losses, 1), 10))
	msg += fmt.Sprintf("❤️ Survival rate: *%s*\n", percent(s.SurvivalRate()))

	if s.CurrentStreak > 1 {
		msg += fmt.Sprintf("🔥 On a *%d*-win streak\n", s.CurrentStreak)
	}
	if s.BestStreak > 0 {
		msg += fmt.Sprintf("⭐ Best streak: *%d*\n", s.BestStreak)
	}

	if role, record, ok := s.FavouriteRole(); ok {
		msg += fmt.Sprintf("%s Most played: *%s* (%d games, %d won)\n",
			engine.RoleEmoji(engine.Role(role)), engine.EscapeMD(engine.RoleTitle(engine.Role(role))),
			record.Played, record.Won)
	}

	if best := bestRoles(s, 3); best != "" {
		msg += divider + "\n*Best roles*\n" + best
	}

	unlocked, locked := s.UnlockedList()
	msg += divider + "\n"
	msg += fmt.Sprintf("🏅 Achievements: *%d*/%d\n", len(unlocked), len(unlocked)+len(locked))
	if len(unlocked) > 0 {
		var icons []string
		for _, a := range unlocked {
			icons = append(icons, a.Emoji)
		}
		msg += strings.Join(icons, " ") + "\n"
	}
	msg += "\n_/achievements to see them all._"
	return msg
}

// bestRoles lists the roles the player wins with most often, requiring at
// least two games so a single lucky round does not top the list.
func bestRoles(s *PlayerStats, limit int) string {
	type entry struct {
		role   string
		record RoleRecord
	}
	var entries []entry
	for role, record := range s.Roles {
		if record.Played >= 2 {
			entries = append(entries, entry{role, record})
		}
	}
	if len(entries) == 0 {
		return ""
	}
	// Sort by win rate, then by games played, then by name for stability.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			a, b := entries[j], entries[j-1]
			ra := float64(a.record.Won) / float64(a.record.Played)
			rb := float64(b.record.Won) / float64(b.record.Played)
			better := ra > rb ||
				(ra == rb && a.record.Played > b.record.Played) ||
				(ra == rb && a.record.Played == b.record.Played && a.role < b.role)
			if !better {
				break
			}
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}

	out := ""
	for _, e := range entries {
		rate := float64(e.record.Won) / float64(e.record.Played)
		out += fmt.Sprintf("%s %s — %d/%d (%s)\n",
			engine.RoleEmoji(engine.Role(e.role)),
			engine.EscapeMD(engine.RoleTitle(engine.Role(e.role))),
			e.record.Won, e.record.Played, percent(rate))
	}
	return out
}

// FormatLeaderboard is the /leaderboard reply.
func FormatLeaderboard(title string, players []*PlayerStats, limit int) string {
	ranked := Leaderboard(players)
	if len(ranked) == 0 {
		return "🏆 *Leaderboard*\n\n_No games have been recorded yet. Be the first._"
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	msg := fmt.Sprintf("🏆 *%s*\n%s\n", engine.EscapeMD(title), divider)
	for i, s := range ranked {
		msg += fmt.Sprintf("%s %s — *%d* wins · %s of %d games\n",
			medal(i), engine.EscapeMD(s.DisplayName()), s.Wins, percent(s.WinRate()), s.GamesPlayed)
	}
	msg += divider + "\n_/stats for your own record._"
	return msg
}

func medal(index int) string {
	switch index {
	case 0:
		return "🥇"
	case 1:
		return "🥈"
	case 2:
		return "🥉"
	default:
		return fmt.Sprintf("*%d.*", index+1)
	}
}

// FormatAchievements is the /achievements reply, unlocked first.
func FormatAchievements(s *PlayerStats) string {
	if s == nil {
		s = NewPlayerStats(0)
	}
	unlocked, locked := s.UnlockedList()

	msg := fmt.Sprintf("🏅 *Achievements* — %d of %d\n%s\n",
		len(unlocked), len(unlocked)+len(locked), divider)

	if len(unlocked) == 0 {
		msg += "_Nothing unlocked yet._\n"
	}
	for _, a := range unlocked {
		msg += fmt.Sprintf("%s *%s*\n_%s_\n", a.Emoji, engine.EscapeMD(a.Title), engine.EscapeMD(a.Detail))
	}
	if len(locked) > 0 {
		msg += divider + "\n*Still to earn*\n"
		for _, a := range locked {
			msg += fmt.Sprintf("🔒 %s — _%s_\n", engine.EscapeMD(a.Title), engine.EscapeMD(a.Detail))
		}
	}
	msg += "\n_Some achievements are secret and only appear once earned._"
	return msg
}

// FormatUnlockDM is the congratulations message sent when something unlocks.
func FormatUnlockDM(earned []Achievement) string {
	if len(earned) == 0 {
		return ""
	}
	msg := "🎉 *Achievement unlocked!*\n"
	if len(earned) > 1 {
		msg = fmt.Sprintf("🎉 *%d achievements unlocked!*\n", len(earned))
	}
	for _, a := range earned {
		msg += fmt.Sprintf("\n%s *%s*\n_%s_\n", a.Emoji, engine.EscapeMD(a.Title), engine.EscapeMD(a.Detail))
	}
	return msg
}

// FormatGameRecap renders a stored game record for /lastgame.
func FormatGameRecap(record *GameRecord) string {
	if record == nil {
		return "📜 *No finished game on record for this chat yet.*"
	}

	msg := "📜 *Last game*\n" + divider + "\n"
	msg += engine.EscapeMD(record.WinnerDesc) + "\n"
	msg += fmt.Sprintf("\n📅 %s · %d day(s)", record.EndedAt.Format("2 Jan 15:04"), record.Days)
	if record.Duration > 0 {
		msg += " · ⏱️ " + engine.FormatDuration(record.Duration)
	}
	msg += "\n" + divider + "\n*Roles*\n"

	for _, p := range record.Players {
		status := "💀"
		if p.Survived {
			status = "✅"
		}
		line := fmt.Sprintf("%s %s — %s", status, engine.EscapeMD(p.Name), engine.RoleBadge(p.Role))
		if p.Won {
			line += " 🏅"
		}
		msg += line + "\n"
	}

	if len(record.Awards) > 0 {
		msg += divider + "\n*Awards*\n"
		for _, a := range record.Awards {
			msg += fmt.Sprintf("%s *%s* — %s\n", a.Emoji, engine.EscapeMD(a.Title), engine.EscapeMD(a.PlayerName))
		}
	}
	return msg
}

func percent(rate float64) string {
	return fmt.Sprintf("%.0f%%", rate*100)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// GameRecord is a finished game as stored for /lastgame and history browsing.
// It mirrors engine.GameSummary but is flattened for storage and display.
type GameRecord struct {
	GameID     engine.GameID          `json:"game_id"`
	ChatID     int64                  `json:"chat_id"`
	StartedAt  time.Time              `json:"started_at"`
	EndedAt    time.Time              `json:"ended_at"`
	Duration   time.Duration          `json:"duration"`
	Days       int                    `json:"days"`
	Winner     engine.Team            `json:"winner"`
	WinnerDesc string                 `json:"winner_desc"`
	Aborted    bool                   `json:"aborted"`
	Preset     string                 `json:"preset"`
	Players    []RecordPlayer         `json:"players"`
	Awards     []engine.Award         `json:"awards"`
	Timeline   []engine.TimelineEntry `json:"timeline"`
}

// RecordPlayer is one player's line in a stored game record.
type RecordPlayer struct {
	ID       engine.PlayerID `json:"id"`
	Name     string          `json:"name"`
	Username string          `json:"username"`
	Role     engine.Role     `json:"role"`
	Survived bool            `json:"survived"`
	Won      bool            `json:"won"`
}

// RecordFromSummary flattens a finished game into its stored form.
func RecordFromSummary(summary engine.GameSummary) *GameRecord {
	record := &GameRecord{
		GameID:     summary.GameID,
		ChatID:     summary.ChatID,
		StartedAt:  summary.StartedAt,
		EndedAt:    summary.EndedAt,
		Duration:   summary.Duration(),
		Days:       summary.Days,
		Winner:     summary.Winner,
		WinnerDesc: summary.WinnerDesc,
		Aborted:    summary.Aborted,
		Preset:     summary.Preset,
		Awards:     summary.Awards,
		Timeline:   summary.Timeline,
	}
	for _, p := range summary.Players {
		record.Players = append(record.Players, RecordPlayer{
			ID:       p.ID,
			Name:     p.Name,
			Username: p.Username,
			Role:     p.Role,
			Survived: p.Survived,
			Won:      p.Won,
		})
	}
	return record
}
