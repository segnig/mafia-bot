package engine

import "sort"

// TargetRule decides who a role is allowed to point its night action at.
type TargetRule int

const (
	// TargetNone means the role has no night action at all.
	TargetNone TargetRule = iota
	// TargetOthers is every living player except the actor.
	TargetOthers
	// TargetOthersOrSelf is every living player, the actor included.
	TargetOthersOrSelf
	// TargetOutsideTeam excludes the actor's own faction, so the mafia
	// cannot aim their kill at each other.
	TargetOutsideTeam
)

// RoleInfo is the single description of what a role is and what it can do.
// Everything that used to be a switch over Role — team, night action, target
// list, prompt wording, role DM — reads from here, so adding a role means
// adding one entry rather than editing a dozen switches.
type RoleInfo struct {
	Role  Role
	Team  Team
	Emoji string
	Title string
	// Blurb is the explanation the player receives in their role DM.
	Blurb string
	// ActionKind is the callback token for this role's night action, empty
	// when the role has nothing to submit at night.
	ActionKind string
	// ActionPrompt heads the night action keyboard.
	ActionPrompt string
	Targets      TargetRule
	// OneShot roles may act only once per game.
	OneShot bool
	// AppearsAs is what an investigation reports, when it differs from the
	// role's real team.
	AppearsAs Team
}

// HasNightAction reports whether the role is ever asked to act at night.
func (r RoleInfo) HasNightAction() bool {
	return r.ActionKind != ""
}

var roleCatalog = map[Role]RoleInfo{
	RoleVillager: {
		Role: RoleVillager, Team: TeamTown, Emoji: "🏘️", Title: "Villager",
		Blurb:   "You are an ordinary townsperson with no special power. Your vote and your voice are your weapons — use them to find the mafia.",
		Targets: TargetNone,
	},
	RoleMafia: {
		Role: RoleMafia, Team: TeamMafia, Emoji: "🔪", Title: "Mafia",
		Blurb:        "Eliminate the town one by one. Each night you and your team pick a victim together — if you disagree, nobody dies.",
		ActionKind:   ActionMafiaKill,
		ActionPrompt: "🔪 *Mafia Kill*\nAgree with your team on tonight's victim:",
		Targets:      TargetOutsideTeam,
	},
	RoleGodfather: {
		Role: RoleGodfather, Team: TeamMafia, Emoji: "🎩", Title: "Godfather",
		Blurb:        "You lead the mafia. Investigations come back *Town* for you, so you can afford to be seen.",
		ActionKind:   ActionMafiaKill,
		ActionPrompt: "🎩 *Godfather's Order*\nName tonight's victim:",
		Targets:      TargetOutsideTeam,
		AppearsAs:    TeamTown,
	},
	RoleFramer: {
		Role: RoleFramer, Team: TeamMafia, Emoji: "🖊️", Title: "Framer",
		Blurb:        "You work for the mafia. Each night you plant evidence on one player, so any investigation of them that night reports *Mafia*.",
		ActionKind:   ActionFramerFrame,
		ActionPrompt: "🖊️ *Framer*\nPlant evidence on one player:",
		Targets:      TargetOutsideTeam,
	},
	RoleDetective: {
		Role: RoleDetective, Team: TeamTown, Emoji: "🔍", Title: "Detective",
		Blurb:        "Each night you investigate one player and learn which faction they belong to. Beware — some roles can fool you.",
		ActionKind:   ActionDetectiveCheck,
		ActionPrompt: "🔍 *Investigation*\nWho do you want to look into tonight?",
		Targets:      TargetOthers,
	},
	RoleDoctor: {
		Role: RoleDoctor, Team: TeamTown, Emoji: "💊", Title: "Doctor",
		Blurb:        "Each night you choose one player to save. If anyone tries to kill them tonight, they survive.",
		ActionKind:   ActionDoctorProtect,
		ActionPrompt: "💊 *Treatment*\nWho do you want to keep alive tonight?",
		Targets:      TargetOthers,
	},
	RoleBodyguard: {
		Role: RoleBodyguard, Team: TeamTown, Emoji: "🛡️", Title: "Bodyguard",
		Blurb:        "Each night you stand watch over one player. If a killer comes for them you take the blow instead — and you take the attacker down with you.",
		ActionKind:   ActionBodyguardGuard,
		ActionPrompt: "🛡️ *Watch*\nWho do you want to stand guard over tonight?",
		Targets:      TargetOthers,
	},
	RoleEscort: {
		Role: RoleEscort, Team: TeamTown, Emoji: "💃", Title: "Escort",
		Blurb:        "Each night you occupy one player for the evening. Whatever they meant to do, it does not happen.",
		ActionKind:   ActionEscortBlock,
		ActionPrompt: "💃 *Distraction*\nWhose night are you going to ruin?",
		Targets:      TargetOthers,
	},
	RoleLookout: {
		Role: RoleLookout, Team: TeamTown, Emoji: "🔭", Title: "Lookout",
		Blurb:        "Each night you watch one player's house and learn exactly who came calling.",
		ActionKind:   ActionLookoutWatch,
		ActionPrompt: "🔭 *Stakeout*\nWhose house are you watching tonight?",
		Targets:      TargetOthers,
	},
	RoleVigilante: {
		Role: RoleVigilante, Team: TeamTown, Emoji: "🔫", Title: "Vigilante",
		Blurb:        "You have one bullet for the whole game. Fire it at night — but be sure, because shooting a townsperson costs the town dearly.",
		ActionKind:   ActionVigilanteKill,
		ActionPrompt: "🔫 *One Bullet*\nWho are you shooting? This cannot be undone:",
		Targets:      TargetOthers,
		OneShot:      true,
	},
	RoleMayor: {
		Role: RoleMayor, Team: TeamTown, Emoji: "🏛️", Title: "Mayor",
		Blurb:   "You may reveal yourself once with /reveal. From then on everyone knows who you are, but your vote carries the weight of several.",
		Targets: TargetNone,
	},
	RoleSerialKiller: {
		Role: RoleSerialKiller, Team: TeamKiller, Emoji: "🩸", Title: "Serial Killer",
		Blurb:        "You hunt alone. Kill one player every night, and win when nobody is left to stop you. Investigations report you as *Mafia*.",
		ActionKind:   ActionSerialKill,
		ActionPrompt: "🩸 *The Hunt*\nChoose tonight's victim:",
		Targets:      TargetOthers,
		AppearsAs:    TeamMafia,
	},
	RoleJester: {
		Role: RoleJester, Team: TeamNeutral, Emoji: "🃏", Title: "Jester",
		Blurb:   "You win by getting yourself lynched during the day. Be loud, be suspicious, be impossible to ignore.",
		Targets: TargetNone,
	},
	RoleSurvivor: {
		Role: RoleSurvivor, Team: TeamNeutral, Emoji: "🧥", Title: "Survivor",
		Blurb:   "You have no side and no power. You win simply by being alive when the game ends, whoever else wins.",
		Targets: TargetNone,
	},
}

// unassignedInfo is what an undealt role reports. It is deliberately town, so
// pre-deal code paths behave as they always have.
var unassignedInfo = RoleInfo{Role: RoleUnassigned, Team: TeamTown, Emoji: "❔", Title: "Unassigned"}

// RoleInfoFor never fails: an unknown role behaves like a plain townsperson
// rather than panicking a live game.
func RoleInfoFor(r Role) RoleInfo {
	if info, ok := roleCatalog[r]; ok {
		return info
	}
	if r == RoleUnassigned {
		return unassignedInfo
	}
	return RoleInfo{Role: r, Team: TeamTown, Emoji: "❔", Title: string(r)}
}

// RoleTitle is the display name for a role, e.g. "Serial Killer".
func RoleTitle(r Role) string {
	return RoleInfoFor(r).Title
}

// RoleEmoji is the icon shown next to a role in announcements.
func RoleEmoji(r Role) string {
	return RoleInfoFor(r).Emoji
}

// RoleBadge is the emoji and title together, ready to interpolate.
func RoleBadge(r Role) string {
	info := RoleInfoFor(r)
	return info.Emoji + " " + info.Title
}

// AllRoles lists every implemented role in a stable order, for help text.
func AllRoles() []RoleInfo {
	infos := make([]RoleInfo, 0, len(roleCatalog))
	for _, info := range roleCatalog {
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Team != infos[j].Team {
			return teamOrder(infos[i].Team) < teamOrder(infos[j].Team)
		}
		return infos[i].Title < infos[j].Title
	})
	return infos
}

func teamOrder(t Team) int {
	switch t {
	case TeamTown:
		return 0
	case TeamMafia:
		return 1
	case TeamKiller:
		return 2
	default:
		return 3
	}
}

// ActionKindForRole is the night action token a role submits, empty when the
// role has no night action.
func ActionKindForRole(r Role) string {
	return RoleInfoFor(r).ActionKind
}

// validActionForRole guards against a callback claiming an action the sender's
// role does not have.
func validActionForRole(role Role, kind string) bool {
	info := RoleInfoFor(role)
	return info.HasNightAction() && info.ActionKind == kind
}

// investigationResult is what a detective learns about a target, taking the
// target's disguise and any framing this night into account.
func investigationResult(target *Player) Team {
	if target.FramedTonight {
		return TeamMafia
	}
	info := RoleInfoFor(target.Role)
	if info.AppearsAs != "" {
		return info.AppearsAs
	}
	return info.Team
}

// roleNeedsAction reports whether the game should wait for this player to
// submit something tonight.
func roleNeedsAction(gs *GameState, p *Player) bool {
	if !p.CanAct() {
		return false
	}
	info := RoleInfoFor(p.Role)
	if !info.HasNightAction() {
		return false
	}
	if info.OneShot && p.UsedAbility {
		return false
	}
	// The classic opening: the mafia get a night to talk instead of a kill.
	if info.ActionKind == ActionMafiaKill && gs.DayNumber == 1 && !gs.Config.FirstNightKill {
		return false
	}
	return true
}

// roleTargets is the list of players this role may act on tonight, or nil when
// it has nothing to do.
func roleTargets(gs *GameState, p *Player) []PlayerID {
	if !roleNeedsAction(gs, p) {
		return nil
	}
	info := RoleInfoFor(p.Role)

	alive := gs.AlivePlayerIDs()
	sort.Slice(alive, func(i, j int) bool { return alive[i] < alive[j] })

	rule := info.Targets
	// The doctor is the one role whose self-targeting is a game setting.
	if p.Role == RoleDoctor && gs.Config.DoctorSelfProtect {
		rule = TargetOthersOrSelf
	}

	var targets []PlayerID
	for _, pid := range alive {
		if pid == p.ID && rule != TargetOthersOrSelf {
			continue
		}
		if rule == TargetOutsideTeam && pid != p.ID {
			if other, ok := gs.Players[pid]; ok && RoleTeam(other.Role) == info.Team {
				continue
			}
		}
		targets = append(targets, pid)
	}
	return targets
}
