package engine

import (
	"fmt"
	"strings"
)

// PlayerGuideURL is the full player guide on GitHub. /guide points here.
const PlayerGuideURL = "https://github.com/segnig/mafia-bot/blob/master/docs/GAME_GUIDE.md"

// ResolveHelpTopic returns help text for a /help argument. An empty topic is
// the help index.
func ResolveHelpTopic(topic string) string {
	topic = normalizeHelpTopic(topic)
	switch topic {
	case "":
		return FormatHelpIndex()
	case "general", "game", "play", "howto", "how", "start":
		return FormatHelpGeneral()
	case "settings", "setting", "config", "configure", "set":
		return FormatHelpSettings()
	case "commands", "command", "cmds":
		return FormatHelpCommands()
	case "roles", "role":
		return FormatHelpRolesIndex()
	case "gameplay", "flow", "phases", "day", "night":
		return FormatHelpGameplay()
	case "stats", "leaderboard", "achievements", "progression", "record":
		return FormatHelpStats()
	case "lovers", "lover":
		return FormatHelpLovers()
	default:
		if role, ok := LookupRoleHelp(topic); ok {
			return FormatHelpRole(role)
		}
		return FormatHelpUnknown(topic)
	}
}

func normalizeHelpTopic(topic string) string {
	return strings.TrimSpace(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(topic), "/")))
}

// FormatHelpIndex is the /help menu.
func FormatHelpIndex() string {
	msg := "📖 *Mafia Bot — Help*\n" + divider + "\n"
	msg += "Pick a topic:\n\n"
	msg += "• `/help general` — full how-to (setup, flow, tips)\n"
	msg += "• `/help settings` — presets, toggles, custom `/set` values\n"
	msg += "• `/help commands` — every command\n"
	msg += "• `/help gameplay` — day/night flow and winning\n"
	msg += "• `/help stats` — records, leaderboard, achievements\n"
	msg += "• `/help roles` — list of role names\n"
	msg += "• `/help <role>` — one role in detail\n"
	msg += "  _e.g. `/help detective`, `/help godfather`, `/help jester`_\n"
	msg += "• `/help lovers` — the lovers modifier\n"
	msg += "• `/guide` — link to the full player guide\n"
	msg += divider + "\n"
	msg += "_First time? DM me `/start`, then `/help general`._"
	return msg
}

// FormatHelpGeneral is the detailed overview.
func FormatHelpGeneral() string {
	msg := "🎭 *Mafia Bot — General Guide*\n" + divider + "\n"

	msg += "*What is this?*\n"
	msg += "Social deduction in Telegram. Town finds the hidden mafia; mafia outnumber town to win. "
	msg += "Neutrals and killers may also be in play depending on settings.\n\n"

	msg += "*Before your first game*\n"
	msg += "1. **DM the bot** and send `/start` — required so role DMs can reach you\n"
	msg += "2. Add the bot to a **group chat**\n"
	msg += "3. Someone runs `/startgame` to open a lobby\n"
	msg += "   _(or `/schedule in 2h` to auto-open one later)_\n"
	msg += "4. Players `/join` or tap **Join Lobby**\n"
	msg += "5. **Host** configures rules (`/settings`, `/set`) then runs `/begin` (min 5 players)\n\n"

	msg += "*Game flow*\n"
	msg += "`LOBBY → role DMs → NIGHT → DAY → vote → … → GAME OVER`\n\n"
	msg += "• *Night* — the group goes quiet; roles with powers get buttons in DM\n"
	msg += "• *Day* — debate, `/accuse`, `/defend`, `/whisper`, then vote\n"
	msg += "• *Trial mode* (optional) — `/nominate` then `/second` before voting\n"
	msg += "• *Last words* (optional) — lynched player speaks briefly\n\n"

	msg += "*Host only (lobby)*\n"
	msg += "`/settings` or ⚙️ Configure — presets and toggles\n"
	msg += "`/set night 75` — custom timer values\n\n"

	msg += "*During the day (alive players)*\n"
	msg += "`/accuse @player` · `/defend statement` · `/whisper @player msg`\n"
	msg += "`/reveal` — Mayor only · `/nominate` / `/second` — trial mode\n\n"

	msg += "*In your DMs*\n"
	msg += "`/myrole` · `/mafia msg` (mafia, night) · `/ghost msg` (eliminated players)\n\n"

	msg += "*Info anytime*\n"
	msg += "`/status` · `/graveyard` · `/roles` · `/stats` · `/leaderboard` · `/achievements` · `/lastgame`\n\n"

	msg += "*Etiquette*\n"
	msg += "• Don't screenshot your role DM into the group\n"
	msg += "• Dead players use `/ghost`, not the group chat\n"
	msg += "• Bluffing and lying about your role is encouraged\n\n"

	msg += divider + "\n"
	msg += "_More: `/help settings` · `/help roles` · `/guide`_"
	return msg
}

// FormatHelpSettings documents lobby configuration.
func FormatHelpSettings() string {
	msg := "⚙️ *Settings — Host Only, Lobby Only*\n" + divider + "\n"
	msg += "Settings are configured **after `/startgame`** and **before `/begin`**. "
	msg += "Only the **host** can change them — not admins, not other players.\n\n"

	msg += "*Open the panel*\n"
	msg += "• Tap **⚙️ Configure** on the lobby card\n"
	msg += "• Or type `/settings` in the group\n\n"

	msg += "*Presets*\n"
	for _, name := range PresetNames() {
		label, pitch := PresetLabel(name)
		msg += fmt.Sprintf("• *%s* — _%s_\n", EscapeMD(label), EscapeMD(pitch))
	}
	msg += "\nTap a preset, then tweak individual options. Tap **✅ Done** when finished.\n\n"

	msg += "*Custom values*\n"
	msg += "Type `/set <key> <value>` for exact numbers:\n\n"
	for _, s := range Settings() {
		switch s.Kind {
		case SettingToggle:
			msg += fmt.Sprintf("• `%s` — on/off — _%s_\n", s.Key, EscapeMD(s.Help))
		default:
			msg += fmt.Sprintf("• `%s` — %d–%d — _%s_\n", s.Key, s.Min, s.Max, EscapeMD(s.Help))
		}
	}
	msg += "\n*Examples*\n"
	msg += "`/set night 75` · `/set discussion 150` · `/set lovers on`\n\n"
	msg += "Type `/set` alone for the full list.\n\n"
	msg += "_Settings lock when the host runs `/begin`. Saved choices become the default for the next game._"
	return msg
}

// FormatHelpCommands lists every command.
func FormatHelpCommands() string {
	msg := "⌨️ *All Commands*\n" + divider + "\n"

	msg += "*Setup & lobby*\n"
	msg += "`/startgame` — open a lobby\n"
	msg += "`/schedule in 2h` · `/schedule at 20:00` — auto-open a lobby later\n"
	msg += "`/join` · `/leave` — join or leave before start\n"
	msg += "`/begin` — host starts (enough players + everyone DM'd `/start`)\n"
	msg += "`/settings` — host configures rules in lobby\n"
	msg += "`/set <key> <value>` — host sets a custom value in lobby\n\n"

	msg += "*Day (alive, reachable)*\n"
	msg += "`/accuse @player` · `/defend text` · `/whisper @player msg`\n"
	msg += "`/reveal` — Mayor · `/nominate` · `/second` — trial mode\n\n"

	msg += "*DM only*\n"
	msg += "`/start` — register with the bot (required once)\n"
	msg += "`/myrole` — your role card\n"
	msg += "`/mafia msg` — mafia team chat at night\n"
	msg += "`/ghost msg` — chat with other eliminated players\n\n"

	msg += "*Info*\n"
	msg += "`/status` · `/graveyard` · `/roles` · `/help` · `/guide`\n"
	msg += "`/stats` · `/leaderboard` · `/leaderboard global` · `/achievements` · `/lastgame`\n\n"

	msg += "*Host / admin*\n"
	msg += "`/host @player` — transfer host · `/kick @player` — remove AFK player\n"
	msg += "`/endgame` — host or admin force-ends the game\n\n"

	msg += divider + "\n"
	msg += "_Role details: `/help detective` · Settings: `/help settings`_"
	return msg
}

// FormatHelpGameplay explains phases and win conditions.
func FormatHelpGameplay() string {
	msg := "🎲 *Gameplay*\n" + divider + "\n"

	msg += "*Phases*\n"
	msg += "1. **Lobby** — players join; host sets rules\n"
	msg += "2. **Role DMs** — everyone gets their role in private; night won't start until delivered\n"
	msg += "3. **Night** — mafia pick a victim; town powers act via DM buttons\n"
	msg += "4. **Day discussion** — debate, accuse, defend, whisper\n"
	msg += "5. **Voting** — live vote board; majority (or setting) lynches someone\n"
	msg += "6. **Last words** — optional final message from the condemned\n"
	msg += "7. Repeat night/day until someone wins\n\n"

	msg += "*How town wins*\n"
	msg += "Eliminate every mafia member and any rival killers (e.g. Serial Killer).\n\n"

	msg += "*How mafia wins*\n"
	msg += "Reach equal numbers with everyone else, with no Serial Killer left to steal the game.\n\n"

	msg += "*Neutrals*\n"
	msg += "• **Jester** — get yourself lynched\n"
	msg += "• **Survivor** — stay alive at the end, regardless of who wins\n\n"

	msg += "*Night tips*\n"
	msg += "• Last button tap before the timer wins\n"
	msg += "• Mafia must **agree** on one target or nobody dies\n"
	msg += "• Doctor saves from kills; Bodyguard trades lives with an attacker\n"
	msg += "• Escort blocks someone's action; Lookout sees visitors\n"
	msg += "• Framer makes investigations read Mafia for one night\n\n"

	msg += "*Day tips*\n"
	msg += "• Silent/unreachable players (📵) can't act or vote\n"
	msg += "• Mayor `/reveal` makes their vote count heavier\n"
	msg += "• Trial mode requires nominate + second before a vote\n\n"

	msg += divider + "\n"
	msg += "_Every role: `/help roles`_"
	return msg
}

// FormatHelpStats covers progression systems.
func FormatHelpStats() string {
	msg := "📊 *Stats & Progression*\n" + divider + "\n"

	msg += "*`/stats`* — your lifetime record\n"
	msg += "Games, wins, losses, win rate, survival rate, streaks, per-role breakdown.\n"
	msg += "Reply to someone or mention them for their record.\n\n"

	msg += "*`/leaderboard`* — top 10 in this group\n"
	msg += "`/leaderboard global` — top 10 across all groups\n"
	msg += "Ranked by wins, win rate (with a minimum-games weight), and best streak.\n"
	msg += "Only **finished** games count — cancelled games don't affect records.\n\n"

	msg += "*`/achievements`* — permanent unlocks\n"
	msg += "First win, hat tricks, role-specific trophies, and a few secret ones.\n\n"

	msg += "*`/lastgame`* — recap of this group's most recent finished game\n\n"

	msg += "*Rank titles* (from total wins)\n"
	msg += "🔰 Newcomer → 🌱 Apprentice → ⭐ Regular → 🏆 Veteran → 💎 Mastermind → 👑 Legend\n\n"

	msg += divider + "\n"
	msg += "_Cancelled games don't count as wins or losses._"
	return msg
}

// FormatHelpRolesIndex lists role names for /help <role> lookup.
func FormatHelpRolesIndex() string {
	msg := "🎭 *Roles — pick one for details*\n" + divider + "\n"
	lastTeam := Team("")
	for _, info := range AllRoles() {
		if info.Team != lastTeam {
			msg += fmt.Sprintf("\n*%s*\n", TeamLabel(info.Team))
			lastTeam = info.Team
		}
		msg += fmt.Sprintf("%s `/help %s`\n", info.Emoji, strings.ToLower(info.Title))
	}
	msg += "\n*Also*\n`/help lovers` — paired players who die together\n\n"
	msg += divider + "\n"
	msg += "_Quick list: `/roles`_"
	return msg
}

// FormatHelpLovers explains the lovers modifier.
func FormatHelpLovers() string {
	return "💞 *Lovers Modifier*\n" + divider + "\n" +
		"When enabled in settings, two players are secretly paired at the deal.\n\n" +
		"• Each lover is told who their partner is in DM\n" +
		"• **When one dies, the other dies immediately** — grief, regardless of role\n" +
		"• A mafioso paired with a townsperson creates very awkward nights\n\n" +
		"Enable in the lobby: `/settings` → 💞 Lovers, or `/set lovers on`\n\n" +
		divider + "\n" +
		"_Turned on by default in the 🎲 Chaos preset._"
}

// FormatHelpRole is detailed help for one role.
func FormatHelpRole(role Role) string {
	info := RoleInfoFor(role)
	msg := fmt.Sprintf("%s *%s*\n%s\n", info.Emoji, EscapeMD(info.Title), divider)
	msg += fmt.Sprintf("*Team:* %s\n\n", TeamLabel(info.Team))
	msg += info.Blurb + "\n"

	if info.HasNightAction() {
		msg += "\n*Night action:* yes"
		if info.OneShot {
			msg += " (once per game)"
		}
		msg += "\n"
		if info.ActionPrompt != "" {
			// Strip markdown from prompt for help
			prompt := strings.TrimPrefix(info.ActionPrompt, info.Emoji+" ")
			msg += fmt.Sprintf("_%s_\n", EscapeMD(strings.ReplaceAll(prompt, "*", "")))
		}
	} else {
		msg += "\n*Night action:* none\n"
	}

	if info.AppearsAs != "" && info.AppearsAs != info.Team {
		msg += fmt.Sprintf("\n*Investigations read as:* %s\n", TeamLabel(info.AppearsAs))
	}

	switch role {
	case RoleMayor:
		msg += "\n*Day command:* `/reveal` — go public for a heavier vote\n"
	case RoleMafia, RoleGodfather:
		msg += "\n*Team chat:* `/mafia message` in DM during night (if enabled in settings)\n"
	case RoleVigilante:
		msg += "\n*Warning:* shooting a townsperson has serious consequences\n"
	case RoleJester:
		msg += "\n*Win condition:* get lynched during the day\n"
	case RoleSurvivor:
		msg += "\n*Win condition:* survive to the end on any side\n"
	}

	msg += "\n" + divider + "\n"
	msg += "_All roles: `/help roles`_"
	return msg
}

// FormatHelpUnknown suggests valid topics when a topic wasn't found.
func FormatHelpUnknown(topic string) string {
	msg := fmt.Sprintf("❓ No help topic *%s*.\n\n", EscapeMD(topic))
	msg += "*Try:*\n"
	msg += "• `/help` — menu\n"
	msg += "• `/help general` · `/help settings` · `/help roles`\n"
	msg += "• `/help detective` (or any role name)\n"
	msg += "• `/guide` — full player guide online"
	return msg
}

// FormatGuideMessage is the /guide reply with the doc link.
func FormatGuideMessage() string {
	return "📚 *Player Guide*\n" + divider + "\n" +
		"The complete reference — every role, setting, phase, command, stat, achievement, and rule.\n\n" +
		fmt.Sprintf("[Open the guide](%s)\n\n", PlayerGuideURL) +
		"*In-chat shortcuts:*\n" +
		"• `/help general` — detailed overview\n" +
		"• `/help settings` — configure the lobby\n" +
		"• `/help roles` — role index\n" +
		"• `/help <role>` — one role explained\n\n" +
		divider + "\n" +
		"_Start here if you're new: DM `/start`, then `/help general`_"
}

var roleHelpAliases = map[string]Role{
	"villager": RoleVillager, "villagers": RoleVillager,
	"mafia": RoleMafia, "mafioso": RoleMafia, "mafiosi": RoleMafia,
	"godfather": RoleGodfather, "gf": RoleGodfather,
	"framer": RoleFramer,
	"detective": RoleDetective, "det": RoleDetective, "cop": RoleDetective, "investigator": RoleDetective,
	"doctor": RoleDoctor, "doc": RoleDoctor, "medic": RoleDoctor,
	"bodyguard": RoleBodyguard, "bg": RoleBodyguard, "guard": RoleBodyguard,
	"escort": RoleEscort, "blocker": RoleEscort,
	"lookout": RoleLookout, "watch": RoleLookout,
	"vigilante": RoleVigilante, "vig": RoleVigilante, "shooter": RoleVigilante,
	"mayor": RoleMayor,
	"serial killer": RoleSerialKiller, "serialkiller": RoleSerialKiller,
	"sk": RoleSerialKiller, "killer": RoleSerialKiller,
	"jester": RoleJester, "jest": RoleJester,
	"survivor": RoleSurvivor, "surv": RoleSurvivor,
}

// LookupRoleHelp resolves a /help topic string to a role.
func LookupRoleHelp(topic string) (Role, bool) {
	topic = normalizeHelpTopic(topic)
	topic = strings.ReplaceAll(topic, "-", " ")
	topic = strings.ReplaceAll(topic, "_", " ")

	if role, ok := roleHelpAliases[topic]; ok {
		return role, true
	}
	for _, info := range AllRoles() {
		if strings.EqualFold(info.Title, topic) {
			return info.Role, true
		}
		if string(info.Role) == topic {
			return info.Role, true
		}
	}
	return "", false
}
