package engine

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Reduce processes an event against the current state, returning the new state and side effects.
func Reduce(gs *GameState, ev Event) (*GameState, []SideEffect) {
	switch e := ev.(type) {
	case JoinEvent:
		return reduceJoin(gs, e)
	case LeaveEvent:
		return reduceLeave(gs, e)
	case BeginEvent:
		return reduceBegin(gs, e)
	case EndGameEvent:
		return reduceEndGame(gs, e)
	case RolesDeliveredEvent:
		return reduceRolesDelivered(gs)
	case NightActionEvent:
		return reduceNightAction(gs, e)
	case TimeoutEvent:
		return reduceTimeout(gs, e)
	case VoteEvent:
		return reduceVote(gs, e)
	case PlayerDisconnectedEvent:
		return reduceDisconnect(gs, e)
	case TimerWarningEvent:
		return reduceWarning(gs, e)
	default:
		return gs, nil
	}
}

func reduceJoin(gs *GameState, e JoinEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLobby {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("@%s, this game already started — you can't join mid-game. You'll be notified when the next one opens.", e.Username)},
		}
	}
	if _, exists := gs.Players[e.PlayerID]; exists {
		return gs, nil // idempotent
	}
	if len(gs.Players) >= gs.Config.MaxPlayers {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "The lobby is full!"},
		}
	}

	gs.Players[e.PlayerID] = &Player{
		ID:       e.PlayerID,
		Username: e.Username,
		Alive:    true,
		JoinedAt: e.Time,
	}
	gs.AppendLog("player_joined", map[string]interface{}{"player_id": e.PlayerID, "username": e.Username})

	return gs, []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("✋ @%s joined! (%d/%d players)", e.Username, len(gs.Players), gs.Config.MaxPlayers)},
	}
}

func reduceLeave(gs *GameState, e LeaveEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLobby {
		return gs, nil
	}
	p, exists := gs.Players[e.PlayerID]
	if !exists {
		return gs, nil
	}
	delete(gs.Players, e.PlayerID)
	gs.AppendLog("player_left", map[string]interface{}{"player_id": e.PlayerID})

	if e.PlayerID == gs.HostID {
		gs.HostID = reassignHost(gs)
		var hostMsg string
		if gs.HostID != 0 {
			hostMsg = fmt.Sprintf(" New host: @%s.", gs.Players[gs.HostID].Username)
		}
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("@%s left the lobby.%s (%d players)", p.Username, hostMsg, len(gs.Players))},
		}
	}

	return gs, []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("@%s left the lobby. (%d players)", p.Username, len(gs.Players))},
	}
}

func reassignHost(gs *GameState) PlayerID {
	var earliest *Player
	for _, p := range gs.Players {
		if earliest == nil || p.JoinedAt.Before(earliest.JoinedAt) {
			earliest = p
		}
	}
	if earliest != nil {
		return earliest.ID
	}
	return 0
}

func reduceBegin(gs *GameState, e BeginEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLobby {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "The game has already begun."},
		}
	}
	if e.PlayerID != gs.HostID {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "Only the host can start the game."},
		}
	}
	if len(gs.Players) < gs.Config.MinPlayers {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("Not enough players. Need at least %d, have %d.", gs.Config.MinPlayers, len(gs.Players))},
		}
	}

	gs.Phase = PhaseRoleAssign
	gs.RosterLocked = true
	gs.AppendLog("phase_change", map[string]interface{}{"phase": string(PhaseRoleAssign)})

	playerIDs := make([]PlayerID, 0, len(gs.Players))
	for pid := range gs.Players {
		playerIDs = append(playerIDs, pid)
	}

	assignment, err := AllocateRoles(playerIDs, gs.Config, rand.Reader)
	if err != nil {
		gs.Phase = PhaseLobby
		gs.RosterLocked = false
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "Failed to assign roles. Please try again."},
		}
	}

	var effects []SideEffect
	effects = append(effects, SendGroupEffect{gs.ChatID, "🎬 The game is starting! Check your DMs for your role..."})

	optionalRolesChosen := []string{}
	mafiaCount := 0
	for pid, role := range assignment {
		gs.Players[pid].Role = role
		effects = append(effects, SendDMEffect{
			PlayerID: pid,
			Text:     formatRoleDM(role),
		})
		if role != RoleVillager && role != RoleMafia {
			optionalRolesChosen = append(optionalRolesChosen, string(role))
		}
		if RoleTeam(role) == TeamMafia {
			mafiaCount++
		}
	}

	// Notify mafia members of each other (if more than one)
	if mafiaCount > 1 {
		mafiaNames := []string{}
		var mafiaIDs []PlayerID
		for pid, p := range gs.Players {
			if RoleTeam(p.Role) == TeamMafia {
				mafiaNames = append(mafiaNames, "@"+p.Username)
				mafiaIDs = append(mafiaIDs, pid)
			}
		}
		for _, pid := range mafiaIDs {
			effects = append(effects, SendDMEffect{
				PlayerID: pid,
				Text:     fmt.Sprintf("Your mafia teammates: %s\nCoordinate your kill target each night.", joinStrings(mafiaNames)),
			})
		}
	}

	gs.AppendLog("roles_generated", map[string]interface{}{
		"mafia_count":    mafiaCount,
		"optional_roles": optionalRolesChosen,
		"n_players":      len(gs.Players),
	})

	return gs, effects
}

func formatRoleDM(role Role) string {
	switch role {
	case RoleVillager:
		return "🏘️ Your role: *Villager*\nYou are an ordinary townsperson. Use your vote wisely to find the mafia!"
	case RoleMafia:
		return "🔪 Your role: *Mafia*\nEliminate the town, one by one. Each night, vote with your team to kill a player."
	case RoleDetective:
		return "🔍 Your role: *Detective*\nEach night, investigate one player to learn their alignment (Town or Mafia)."
	case RoleDoctor:
		return "💊 Your role: *Doctor*\nEach night, choose one player to protect from elimination."
	case RoleGodfather:
		return "🎩 Your role: *Godfather*\nYou lead the mafia. You appear *innocent* to Detective investigations!"
	case RoleVigilante:
		return "🔫 Your role: *Vigilante*\nYou fight for the town. Once per game, you may kill a player at night. Choose wisely!"
	case RoleJester:
		return "🃏 Your role: *Jester*\nYou win if the town votes to lynch you during the day. Act suspicious!"
	}
	return fmt.Sprintf("Your role: *%s*", role)
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}

func reduceRolesDelivered(gs *GameState) (*GameState, []SideEffect) {
	if gs.Phase != PhaseRoleAssign {
		return gs, nil
	}
	return transitionToNight(gs)
}

func transitionToNight(gs *GameState) (*GameState, []SideEffect) {
	gs.Phase = PhaseNight
	gs.DayNumber++
	gs.NightActions = make(map[PlayerID]NightAction)
	gs.PhaseDeadline = time.Now().Add(time.Duration(gs.Config.NightTimeoutSec) * time.Second)

	for _, p := range gs.Players {
		p.ProtectedTonight = false
	}

	gs.AppendLog("phase_change", map[string]interface{}{"phase": "night", "day": gs.DayNumber})

	aliveTargets := gs.AlivePlayerIDs()
	var effects []SideEffect
	effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("🌙 *Night %d* falls over the town. Everyone close your eyes...", gs.DayNumber)})
	effects = append(effects, SetTimerEffect{Duration: time.Duration(gs.Config.NightTimeoutSec) * time.Second, Phase: PhaseNight})

	// Schedule warning timers
	nightDur := gs.Config.NightTimeoutSec
	if nightDur > 60 {
		effects = append(effects, SetWarningTimerEffect{
			Duration:    time.Duration(nightDur-60) * time.Second,
			Phase:       PhaseNight,
			SecondsLeft: 60,
		})
	}
	if nightDur > 10 {
		effects = append(effects, SetWarningTimerEffect{
			Duration:    time.Duration(nightDur-10) * time.Second,
			Phase:       PhaseNight,
			SecondsLeft: 10,
		})
	}

	// Send action prompts to roles with abilities
	hasActionRole := false
	for _, p := range gs.Players {
		if !p.Alive || p.Disconnected {
			continue
		}
		switch p.Role {
		case RoleMafia, RoleGodfather:
			targets := filterTargets(aliveTargets, p.ID, TeamMafia, gs)
			effects = append(effects, SendNightActionEffect{p.ID, p.Role, targets, gs.ID})
			hasActionRole = true
		case RoleDetective:
			targets := filterOutSelf(aliveTargets, p.ID)
			effects = append(effects, SendNightActionEffect{p.ID, RoleDetective, targets, gs.ID})
			hasActionRole = true
		case RoleDoctor:
			targets := aliveTargets
			if !gs.Config.DoctorSelfProtect {
				targets = filterOutSelf(aliveTargets, p.ID)
			}
			effects = append(effects, SendNightActionEffect{p.ID, RoleDoctor, targets, gs.ID})
			hasActionRole = true
		case RoleVigilante:
			if !p.UsedAbility {
				targets := filterOutSelf(aliveTargets, p.ID)
				effects = append(effects, SendNightActionEffect{p.ID, RoleVigilante, targets, gs.ID})
				hasActionRole = true
			}
		}
	}

	// Edge case: if no action roles are alive, still run night for mafia kill
	// (handled above — mafia always has at least one member alive if game hasn't ended)
	_ = hasActionRole

	return gs, effects
}

func filterTargets(all []PlayerID, self PlayerID, selfTeam Team, gs *GameState) []PlayerID {
	var targets []PlayerID
	for _, pid := range all {
		if pid == self {
			continue
		}
		if selfTeam == TeamMafia && RoleTeam(gs.Players[pid].Role) == TeamMafia {
			continue
		}
		targets = append(targets, pid)
	}
	return targets
}

func filterOutSelf(all []PlayerID, self PlayerID) []PlayerID {
	var result []PlayerID
	for _, pid := range all {
		if pid != self {
			result = append(result, pid)
		}
	}
	return result
}

func reduceNightAction(gs *GameState, e NightActionEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseNight {
		return gs, []SideEffect{
			SendDMEffect{e.Action.ActorID, "⏰ The night phase has ended. Your action was not recorded."},
		}
	}
	p, exists := gs.Players[e.Action.ActorID]
	if !exists || !p.Alive {
		return gs, nil
	}

	if !validActionForRole(p.Role, e.Action.Kind) {
		return gs, nil
	}

	// Validate target is alive
	target, exists := gs.Players[e.Action.TargetID]
	if !exists || !target.Alive {
		return gs, []SideEffect{
			SendDMEffect{e.Action.ActorID, "❌ Invalid target. That player is not alive."},
		}
	}

	// Doctor self-protect check
	if e.Action.Kind == ActionDoctorProtect && e.Action.TargetID == e.Action.ActorID && !gs.Config.DoctorSelfProtect {
		return gs, []SideEffect{
			SendDMEffect{e.Action.ActorID, "❌ You cannot protect yourself."},
		}
	}

	// Mafia can't target teammates
	if e.Action.Kind == ActionMafiaKill && RoleTeam(target.Role) == TeamMafia {
		return gs, []SideEffect{
			SendDMEffect{e.Action.ActorID, "❌ You cannot target a fellow mafia member."},
		}
	}

	// Vigilante one-time use check
	if e.Action.Kind == ActionVigilanteKill && p.UsedAbility {
		return gs, []SideEffect{
			SendDMEffect{e.Action.ActorID, "❌ You have already used your one-time kill ability."},
		}
	}

	// Overwrite previous action (last submission wins)
	_, hadPrevious := gs.NightActions[e.Action.ActorID]
	gs.NightActions[e.Action.ActorID] = e.Action
	gs.AppendLog("night_action", map[string]interface{}{
		"actor": e.Action.ActorID,
		"kind":  e.Action.Kind,
	})

	var confirmMsg string
	if hadPrevious {
		confirmMsg = "✅ Action updated. Your new target has been recorded."
	} else {
		confirmMsg = "✅ Action recorded."
	}

	if allNightActionsSubmitted(gs) {
		resolvedState, resolvedEffects := resolveNight(gs)
		return resolvedState, append([]SideEffect{SendDMEffect{e.Action.ActorID, confirmMsg}}, resolvedEffects...)
	}

	return gs, []SideEffect{SendDMEffect{e.Action.ActorID, confirmMsg}}
}

func validActionForRole(role Role, kind string) bool {
	switch role {
	case RoleMafia, RoleGodfather:
		return kind == ActionMafiaKill
	case RoleDetective:
		return kind == ActionDetectiveCheck
	case RoleDoctor:
		return kind == ActionDoctorProtect
	case RoleVigilante:
		return kind == ActionVigilanteKill
	}
	return false
}

func allNightActionsSubmitted(gs *GameState) bool {
	for _, p := range gs.Players {
		if !p.Alive || p.Disconnected {
			continue
		}
		switch p.Role {
		case RoleMafia, RoleGodfather:
			if _, ok := gs.NightActions[p.ID]; !ok {
				return false
			}
		case RoleDetective:
			if _, ok := gs.NightActions[p.ID]; !ok {
				return false
			}
		case RoleDoctor:
			if _, ok := gs.NightActions[p.ID]; !ok {
				return false
			}
		case RoleVigilante:
			if !p.UsedAbility {
				if _, ok := gs.NightActions[p.ID]; !ok {
					return false
				}
			}
		}
	}
	return true
}

func resolveNight(gs *GameState) (*GameState, []SideEffect) {
	gs.Phase = PhaseNightResolve
	gs.LastNightDeaths = nil
	gs.LastCheckResult = nil

	// Resolution order (documented in §8.4):
	// 1. Doctor protection (status flag)
	// 2. Kill sources (Mafia, Vigilante)
	// 3. Detective check (informational)

	// Step 1: Doctor protection
	for _, action := range gs.NightActions {
		if action.Kind == ActionDoctorProtect {
			if target, ok := gs.Players[action.TargetID]; ok {
				target.ProtectedTonight = true
			}
		}
	}

	// Step 2a: Mafia kill
	mafiaTarget := resolveMafiaTarget(gs)
	if mafiaTarget != 0 {
		if target, ok := gs.Players[mafiaTarget]; ok {
			if target.ProtectedTonight {
				gs.AppendLog("kill_prevented", map[string]interface{}{"target": mafiaTarget, "by": "doctor"})
			} else {
				target.Alive = false
				gs.LastNightDeaths = append(gs.LastNightDeaths, mafiaTarget)
				gs.AppendLog("player_killed", map[string]interface{}{"player_id": mafiaTarget, "cause": "mafia"})
			}
		}
	}

	// Step 2b: Vigilante kill
	for _, action := range gs.NightActions {
		if action.Kind == ActionVigilanteKill {
			if actor, ok := gs.Players[action.ActorID]; ok {
				actor.UsedAbility = true
			}
			if target, ok := gs.Players[action.TargetID]; ok {
				if target.ProtectedTonight {
					gs.AppendLog("kill_prevented", map[string]interface{}{"target": action.TargetID, "by": "doctor"})
				} else if target.Alive {
					// Only kill if not already dead from mafia this same night
					target.Alive = false
					alreadyDead := false
					for _, d := range gs.LastNightDeaths {
						if d == action.TargetID {
							alreadyDead = true
							break
						}
					}
					if !alreadyDead {
						gs.LastNightDeaths = append(gs.LastNightDeaths, action.TargetID)
					}
					gs.AppendLog("player_killed", map[string]interface{}{"player_id": action.TargetID, "cause": "vigilante"})
				}
			}
		}
	}

	// Step 3: Detective check
	var effects []SideEffect
	for _, action := range gs.NightActions {
		if action.Kind == ActionDetectiveCheck {
			if target, ok := gs.Players[action.TargetID]; ok {
				resultTeam := RoleTeam(target.Role)
				if target.Role == RoleGodfather {
					resultTeam = TeamTown // Godfather's gimmick
				}
				gs.LastCheckResult = &CheckResult{
					DetectiveID: action.ActorID,
					TargetID:    action.TargetID,
					ResultTeam:  resultTeam,
				}
				effects = append(effects, SendDMEffect{
					PlayerID: action.ActorID,
					Text:     fmt.Sprintf("🔍 Investigation complete: *%s* is aligned with *%s*.", target.Username, string(resultTeam)),
				})
			}
		}
	}

	// Immediate win check after night resolution (§8.8)
	if winner := checkWinCondition(gs); winner != nil {
		gs.Phase = PhaseGameOver
		gs.Winner = winner
		effects = append(effects, SendGroupEffect{gs.ChatID, formatNightDeaths(gs)})
		effects = append(effects, GameOverEffect{*winner})
		effects = append(effects, SendGroupEffect{gs.ChatID, formatGameOver(gs)})
		return gs, effects
	}

	// §8.8: Last two = 1 mafia + 1 town, currently night → short-circuit
	alive := gs.AlivePlayers()
	if len(alive) == 2 && gs.AliveMafiaCount() == 1 {
		// Mafia wins — can force any outcome
		gs.Phase = PhaseGameOver
		gs.Winner = &WinResult{Winner: TeamMafia, Description: "Mafia has taken over the town! Mafia wins! 🔪"}
		effects = append(effects, SendGroupEffect{gs.ChatID, formatNightDeaths(gs)})
		effects = append(effects, GameOverEffect{*gs.Winner})
		effects = append(effects, SendGroupEffect{gs.ChatID, formatGameOver(gs)})
		return gs, effects
	}

	// Transition to day
	gs.Phase = PhaseDayAnnounce
	effects = append(effects, SendGroupEffect{gs.ChatID, formatNightDeaths(gs)})

	gs.Phase = PhaseDiscussion
	gs.Votes = make(map[PlayerID]Vote)
	gs.PhaseDeadline = time.Now().Add(time.Duration(gs.Config.DiscussionTimeoutSec) * time.Second)
	effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("☀️ *Day %d* — Discuss! (%d seconds)", gs.DayNumber, gs.Config.DiscussionTimeoutSec)})
	effects = append(effects, SetTimerEffect{Duration: time.Duration(gs.Config.DiscussionTimeoutSec) * time.Second, Phase: PhaseDiscussion})

	// Warning timers for discussion
	discDur := gs.Config.DiscussionTimeoutSec
	if discDur > 60 {
		effects = append(effects, SetWarningTimerEffect{Duration: time.Duration(discDur-60) * time.Second, Phase: PhaseDiscussion, SecondsLeft: 60})
	}
	if discDur > 10 {
		effects = append(effects, SetWarningTimerEffect{Duration: time.Duration(discDur-10) * time.Second, Phase: PhaseDiscussion, SecondsLeft: 10})
	}

	gs.AppendLog("phase_change", map[string]interface{}{"phase": "discussion", "day": gs.DayNumber})
	return gs, effects
}

func resolveMafiaTarget(gs *GameState) PlayerID {
	votes := make(map[PlayerID]int)
	for _, action := range gs.NightActions {
		if action.Kind == ActionMafiaKill {
			votes[action.TargetID]++
		}
	}
	if len(votes) == 0 {
		return 0 // no mafia votes submitted — no kill
	}

	// Plurality; on tie, no kill (§8.4 safest default)
	var maxVotes int
	var target PlayerID
	tied := false
	for pid, count := range votes {
		if count > maxVotes {
			maxVotes = count
			target = pid
			tied = false
		} else if count == maxVotes {
			tied = true
		}
	}
	if tied {
		return 0
	}
	return target
}

func formatNightDeaths(gs *GameState) string {
	if len(gs.LastNightDeaths) == 0 {
		return "☀️ The sun rises... *no one* died last night!"
	}
	msg := "☀️ The sun rises... "
	for i, pid := range gs.LastNightDeaths {
		p := gs.Players[pid]
		if i > 0 {
			msg += " and "
		}
		msg += fmt.Sprintf("*%s*", p.Username)
		if gs.Config.RevealRoleOnDeath {
			msg += fmt.Sprintf(" (%s)", string(p.Role))
		}
		msg += " was found dead"
	}
	msg += "!"
	return msg
}

func formatGameOver(gs *GameState) string {
	if gs.Winner == nil {
		return "Game Over!"
	}
	msg := fmt.Sprintf("🏆 *Game Over!* %s\n\n*Final Roles:*\n", gs.Winner.Description)
	for _, p := range gs.Players {
		status := "💀"
		if p.Alive {
			status = "✅"
		}
		msg += fmt.Sprintf("%s @%s — %s (%s)\n", status, p.Username, p.Role, RoleTeam(p.Role))
	}
	if len(gs.JesterWon) > 0 {
		msg += "\n🃏 Jester winners: "
		for i, pid := range gs.JesterWon {
			if i > 0 {
				msg += ", "
			}
			msg += "@" + gs.Players[pid].Username
		}
	}
	return msg
}

func reduceVote(gs *GameState, e VoteEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseVoting {
		return gs, nil
	}
	voter, exists := gs.Players[e.Vote.VoterID]
	if !exists || !voter.Alive {
		return gs, nil
	}

	// Validate target
	if e.Vote.TargetID != NoLynchTarget {
		target, exists := gs.Players[e.Vote.TargetID]
		if !exists || !target.Alive {
			return gs, nil
		}
	} else if !gs.Config.AllowNoLynch {
		return gs, nil
	}

	_, isChange := gs.Votes[e.Vote.VoterID]
	gs.Votes[e.Vote.VoterID] = e.Vote
	gs.AppendLog("vote_cast", map[string]interface{}{"voter": e.Vote.VoterID, "target": e.Vote.TargetID, "changed": isChange})

	// Notify group of vote (and changes)
	var effects []SideEffect
	if e.Vote.TargetID == NoLynchTarget {
		if isChange {
			effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("@%s changed their vote to *No Lynch*.", voter.Username)})
		} else {
			effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("@%s voted *No Lynch*.", voter.Username)})
		}
	} else {
		target := gs.Players[e.Vote.TargetID]
		if isChange {
			effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("@%s changed their vote to @%s.", voter.Username, target.Username)})
		} else {
			effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("@%s voted for @%s. (%d/%d)", voter.Username, target.Username, len(gs.Votes), len(gs.AlivePlayers()))})
		}
	}

	// Check if all alive players voted
	if len(gs.Votes) >= len(gs.AlivePlayers()) {
		resolvedState, resolvedEffects := resolveLynch(gs)
		return resolvedState, append(effects, resolvedEffects...)
	}

	return gs, effects
}

func resolveLynch(gs *GameState) (*GameState, []SideEffect) {
	gs.Phase = PhaseLynchResolve

	tally := make(map[PlayerID]int)
	for _, v := range gs.Votes {
		tally[v.TargetID]++
	}

	var maxVotes int
	var lynchTarget PlayerID
	tied := false
	for pid, count := range tally {
		if count > maxVotes {
			maxVotes = count
			lynchTarget = pid
			tied = false
		} else if count == maxVotes {
			tied = true
		}
	}

	var effects []SideEffect

	if tied || lynchTarget == NoLynchTarget || maxVotes == 0 {
		effects = append(effects, SendGroupEffect{gs.ChatID, "⚖️ The vote is tied (or no lynch wins). No one is lynched today."})
		gs.AppendLog("no_lynch", map[string]interface{}{"reason": "tie_or_no_lynch"})
	} else {
		target := gs.Players[lynchTarget]

		// Defensive: re-validate target is alive (§8.6)
		if !target.Alive {
			effects = append(effects, SendGroupEffect{gs.ChatID, "⚖️ No valid lynch target. No one is lynched."})
		} else {
			target.Alive = false
			gs.AppendLog("player_lynched", map[string]interface{}{"player_id": lynchTarget, "votes": maxVotes})

			roleReveal := ""
			if gs.Config.RevealRoleOnDeath {
				roleReveal = fmt.Sprintf(" They were a *%s*.", string(target.Role))
			}
			effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("⚖️ The town has spoken! *%s* has been lynched with %d votes.%s", target.Username, maxVotes, roleReveal)})

			// Jester win check (§2.2)
			if target.Role == RoleJester {
				gs.JesterWon = append(gs.JesterWon, lynchTarget)
				effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("🃏 *%s* was the *Jester* and wins! The game continues for everyone else.", target.Username)})
			}
		}
	}

	// Win check
	if winner := checkWinCondition(gs); winner != nil {
		gs.Phase = PhaseGameOver
		gs.Winner = winner
		effects = append(effects, GameOverEffect{*winner})
		effects = append(effects, SendGroupEffect{gs.ChatID, formatGameOver(gs)})
		return gs, effects
	}

	// Back to night
	nightState, nightEffects := transitionToNight(gs)
	effects = append(effects, nightEffects...)
	return nightState, effects
}

func checkWinCondition(gs *GameState) *WinResult {
	mafiaAlive := gs.AliveMafiaCount()
	townAlive := gs.AliveTownCount()
	neutralAlive := gs.AliveNeutralCount()

	if mafiaAlive == 0 {
		return &WinResult{Winner: TeamTown, Description: "All mafia have been eliminated! Town wins! 🎉"}
	}

	if mafiaAlive >= townAlive+neutralAlive {
		return &WinResult{Winner: TeamMafia, Description: "Mafia has taken over the town! Mafia wins! 🔪"}
	}

	// §8.8: all remaining players disconnected
	allDisconnected := true
	for _, p := range gs.Players {
		if p.Alive && !p.Disconnected {
			allDisconnected = false
			break
		}
	}
	if allDisconnected {
		return &WinResult{Winner: "", Description: "All players disconnected. Game void — no winner."}
	}

	return nil
}

func reduceTimeout(gs *GameState, e TimeoutEvent) (*GameState, []SideEffect) {
	if gs.Phase != e.Phase {
		return gs, nil
	}

	switch e.Phase {
	case PhaseLobby:
		if len(gs.Players) >= gs.Config.MinPlayers {
			return reduceBegin(gs, BeginEvent{PlayerID: gs.HostID})
		}
		gs.Phase = PhaseIdle
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "⏰ Lobby timed out without enough players. Game cancelled."},
		}

	case PhaseNight:
		return resolveNight(gs)

	case PhaseDiscussion:
		gs.Phase = PhaseVoting
		gs.Votes = make(map[PlayerID]Vote)
		gs.PhaseDeadline = time.Now().Add(time.Duration(gs.Config.VotingTimeoutSec) * time.Second)
		gs.AppendLog("phase_change", map[string]interface{}{"phase": "voting"})

		targets := gs.AlivePlayerIDs()
		effects := []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("⚖️ *Voting phase!* You have %d seconds to cast your vote.", gs.Config.VotingTimeoutSec)},
			SendVotingKeyboardEffect{gs.ChatID, targets, gs.Config.AllowNoLynch},
			SetTimerEffect{Duration: time.Duration(gs.Config.VotingTimeoutSec) * time.Second, Phase: PhaseVoting},
		}
		// Warning timers
		voteDur := gs.Config.VotingTimeoutSec
		if voteDur > 10 {
			effects = append(effects, SetWarningTimerEffect{Duration: time.Duration(voteDur-10) * time.Second, Phase: PhaseVoting, SecondsLeft: 10})
		}
		return gs, effects

	case PhaseVoting:
		if len(gs.Votes) == 0 {
			gs.Phase = PhaseLynchResolve
			var effects []SideEffect
			effects = append(effects, SendGroupEffect{gs.ChatID, "⏰ No votes were cast. No one is lynched."})
			if winner := checkWinCondition(gs); winner != nil {
				gs.Phase = PhaseGameOver
				gs.Winner = winner
				effects = append(effects, GameOverEffect{*winner})
				effects = append(effects, SendGroupEffect{gs.ChatID, formatGameOver(gs)})
				return gs, effects
			}
			nightState, nightEffects := transitionToNight(gs)
			effects = append(effects, nightEffects...)
			return nightState, effects
		}
		return resolveLynch(gs)
	}

	return gs, nil
}

func reduceDisconnect(gs *GameState, e PlayerDisconnectedEvent) (*GameState, []SideEffect) {
	p, exists := gs.Players[e.PlayerID]
	if !exists {
		return gs, nil
	}
	p.Disconnected = true
	gs.AppendLog("player_disconnected", map[string]interface{}{"player_id": e.PlayerID})

	// If all alive players are disconnected, void the game
	if winner := checkWinCondition(gs); winner != nil {
		gs.Phase = PhaseGameOver
		gs.Winner = winner
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, formatGameOver(gs)},
			GameOverEffect{*winner},
		}
	}

	// If this was the only mafia member still connected during night, resolve immediately
	if gs.Phase == PhaseNight && allNightActionsSubmitted(gs) {
		return resolveNight(gs)
	}

	return gs, nil
}

func reduceWarning(gs *GameState, e TimerWarningEvent) (*GameState, []SideEffect) {
	if gs.Phase != e.Phase {
		return gs, nil
	}
	var location string
	switch e.Phase {
	case PhaseNight:
		location = "night actions"
	case PhaseDiscussion:
		location = "discussion"
	case PhaseVoting:
		location = "voting"
	default:
		location = "this phase"
	}
	return gs, []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("⏰ *%d seconds* remaining for %s!", e.SecondsLeft, location)},
	}
}

func reduceEndGame(gs *GameState, e EndGameEvent) (*GameState, []SideEffect) {
	if gs.Phase == PhaseIdle || gs.Phase == PhaseGameOver {
		return gs, nil
	}
	if e.PlayerID != gs.HostID {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "Only the host can end the game."},
		}
	}
	gs.Phase = PhaseGameOver
	gs.AppendLog("game_ended_by_host", map[string]interface{}{"host": e.PlayerID})
	return gs, []SideEffect{
		SendGroupEffect{gs.ChatID, "🛑 The game has been ended by the host. No winner declared.\n\n" + formatGameOver(gs)},
	}
}
