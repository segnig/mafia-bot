package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const divider = "━━━━━━━━━━━━━━━━━━━━"

// ProgressBar renders value out of total as a fixed-width bar. It is used for
// vote counts and phase clocks, where a shape is faster to read than a number.
func ProgressBar(value, total, width int) string {
	if width <= 0 {
		return ""
	}
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	if value < 0 {
		value = 0
	}
	if value > total {
		value = total
	}
	filled := value * width / total
	// Any progress at all should show, otherwise a single vote looks like none.
	if filled == 0 && value > 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// MoodEmojis are the reactions offered on the day mood bar.
func MoodEmojis() []string {
	return []string{"🤔", "😱", "😂", "🧐", "😡"}
}

// IsMoodEmoji guards the reaction callback against arbitrary input.
func IsMoodEmoji(e string) bool {
	for _, candidate := range MoodEmojis() {
		if candidate == e {
			return true
		}
	}
	return false
}

func formatMood(gs *GameState) string {
	if len(gs.Reactions) == 0 {
		return ""
	}
	var parts []string
	for _, emoji := range MoodEmojis() {
		if n := gs.Reactions[emoji]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", emoji, n))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "🎭 *Mood:* " + strings.Join(parts, "   ") + "\n"
}

// FormatVoteBoard is the live tally shown under the voting keyboard. It is
// rewritten in place after every ballot, so it doubles as the vote prompt.
func FormatVoteBoard(gs *GameState) string {
	header := "🗳️ *Day Vote*"
	if gs.ActiveTrial != nil {
		if p, ok := gs.Players[*gs.ActiveTrial]; ok {
			header = fmt.Sprintf("⚖️ *Trial of %s*", p.Label())
		}
	}

	threshold := lynchThreshold(gs)
	tally := voteTally(gs)

	msg := header + "\n" + divider + "\n"
	msg += formatTallyLines(gs, tally, threshold)
	msg += divider + "\n"
	msg += fmt.Sprintf("🎯 *%d* needed to lynch · %d/%d voted",
		threshold, len(gs.Votes), gs.EligibleVoterCount())

	if left := int(time.Until(gs.PhaseDeadline).Seconds()); left > 0 && !gs.PhaseDeadline.IsZero() {
		msg += fmt.Sprintf("\n⏳ %ds remaining", left)
	}
	if !gs.Config.LiveVoteBoard {
		return msg
	}
	msg += "\n\n_Tap a name below. You can change your vote._"
	return msg
}

// VoteCounts is the weighted tally by target. Exported so the transport can
// label ballot buttons with their current count.
func (gs *GameState) VoteCounts() map[PlayerID]int {
	return voteTally(gs)
}

// LynchThreshold is the vote weight needed to execute someone.
func (gs *GameState) LynchThreshold() int {
	return lynchThreshold(gs)
}

// FormatFinalVoteBoard is the closing tally, posted once voting ends.
func FormatFinalVoteBoard(gs *GameState, tally map[PlayerID]int) string {
	if len(gs.Votes) == 0 {
		return "🗳️ *Final tally* — not a single vote was cast."
	}
	return "🗳️ *Final tally*\n" + divider + "\n" + formatTallyLines(gs, tally, lynchThreshold(gs))
}

// formatTallyLines lists every candidate with a bar and the names behind it.
func formatTallyLines(gs *GameState, tally map[PlayerID]int, threshold int) string {
	type row struct {
		id     PlayerID
		label  string
		weight int
		voters []string
	}

	voters := make(map[PlayerID][]string)
	for voterID, v := range gs.Votes {
		if voter, ok := gs.Players[voterID]; ok {
			name := voter.Label()
			if voter.VoteWeight() > 1 {
				name += fmt.Sprintf(" ×%d", voter.VoteWeight())
			}
			voters[v.TargetID] = append(voters[v.TargetID], name)
		}
	}
	for id := range voters {
		sort.Strings(voters[id])
	}

	var rows []row
	for _, id := range votingTargets(gs) {
		p, ok := gs.Players[id]
		if !ok {
			continue
		}
		rows = append(rows, row{id, p.Label(), tally[id], voters[id]})
	}
	if gs.Config.AllowNoLynch {
		rows = append(rows, row{NoLynchTarget, "🕊️ Skip today", tally[NoLynchTarget], voters[NoLynchTarget]})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].weight != rows[j].weight {
			return rows[i].weight > rows[j].weight
		}
		return rows[i].id < rows[j].id
	})

	msg := ""
	for _, r := range rows {
		// Candidates nobody has voted for would bury the ones that matter.
		if r.weight == 0 && len(gs.Votes) > 0 {
			continue
		}
		msg += fmt.Sprintf("%s `%s` *%d*\n", r.label, ProgressBar(r.weight, threshold, 8), r.weight)
		if len(r.voters) > 0 {
			msg += "   ↳ " + joinStrings(r.voters) + "\n"
		}
	}
	if msg == "" {
		msg = "_No votes yet._\n"
	}
	return msg
}

// FormatGraveyard lists the dead in the order they died, revealing only the
// roles that have already been made public.
func FormatGraveyard(gs *GameState) string {
	dead := gs.DeadPlayers()
	if len(dead) == 0 {
		return "⚰️ *Graveyard*\n" + divider + "\n_Nobody has died yet. Enjoy it while it lasts._"
	}

	msg := fmt.Sprintf("⚰️ *Graveyard* — %d fallen\n%s\n", len(dead), divider)
	for i, p := range dead {
		role := "_role unknown_"
		if p.RoleRevealed {
			role = RoleBadge(p.Role)
		}
		day := ""
		if p.DiedOnDay > 0 {
			day = fmt.Sprintf(" · day %d", p.DiedOnDay)
		}
		msg += fmt.Sprintf("%d. 💀 %s — %s%s\n", i+1, p.Label(), role, day)
	}
	if gs.Config.GhostChat {
		msg += divider + "\n_Ghosts: DM me `/ghost <message>` to talk among yourselves._"
	}
	return msg
}

// FormatGameOver is the closing recap: who won, who was who, the awards, and
// a night-by-night account of how it happened.
func FormatGameOver(gs *GameState, summary GameSummary) string {
	description := "The game ended early."
	if gs.Winner != nil {
		description = gs.Winner.Description
	}

	msg := "🏆 *GAME OVER*\n" + divider + "\n" + description + "\n"
	if summary.Days > 0 {
		msg += fmt.Sprintf("\n📅 Lasted *%d* day(s)", summary.Days)
		if d := summary.Duration(); d > 0 {
			msg += fmt.Sprintf(" · ⏱️ %s", FormatDuration(d))
		}
		msg += "\n"
	}

	msg += divider + "\n*Final roles*\n"
	for _, p := range playersByJoinTime(gs) {
		status := "💀"
		if p.Alive {
			status = "✅"
		}
		// A game ended from the lobby has no roles to reveal, so naming a team
		// there would be misleading.
		if p.Role == RoleUnassigned {
			msg += fmt.Sprintf("%s %s — no role assigned\n", status, p.Label())
			continue
		}
		line := fmt.Sprintf("%s %s — %s", status, p.Label(), RoleBadge(p.Role))
		if summary.WonFor(p.ID) {
			line += " 🏅"
		}
		msg += line + "\n"
	}

	if len(summary.Awards) > 0 {
		msg += divider + "\n*Awards*\n"
		for _, a := range summary.Awards {
			msg += fmt.Sprintf("%s *%s* — %s\n_%s_\n", a.Emoji, a.Title, EscapeMD(a.PlayerName), a.Detail)
		}
	}

	if len(summary.Timeline) > 0 {
		msg += divider + "\n*How it happened*\n"
		for _, entry := range summary.Timeline {
			msg += fmt.Sprintf("%s Day %d — %s\n", entry.Icon, entry.Day, EscapeMD(entry.Text))
		}
	}

	msg += divider + "\n_Tap below for a rematch. /stats to see your record._"
	return msg
}

// FormatDuration renders a game length the way a person would say it.
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d.Minutes())
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
}

// FormatRoleList is the /roles reference, grouped by faction.
func FormatRoleList() string {
	msg := "🎭 *Roles in this bot*\n" + divider + "\n"
	lastTeam := Team("")
	for _, info := range AllRoles() {
		if info.Team != lastTeam {
			msg += fmt.Sprintf("\n*%s*\n", TeamLabel(info.Team))
			lastTeam = info.Team
		}
		msg += fmt.Sprintf("%s *%s* — %s\n", info.Emoji, EscapeMD(info.Title), info.Blurb)
	}
	msg += "\n_Which roles appear depends on the player count and the group's preset._"
	return msg
}
