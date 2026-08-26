package engine

import (
	"crypto/rand"
	"fmt"
	"sort"
	"time"
)

// Reduce processes an event against the current state, returning the new state and side effects.
func Reduce(gs *GameState, ev Event) (*GameState, []SideEffect) {
	switch e := ev.(type) {
	case GameCreatedEvent:
		return reduceGameCreated(gs)
	case ResumeEvent:
		return reduceResume(gs)
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
	case RoleDeliveryFailedEvent:
		return reduceRoleDeliveryFailed(gs, e)
	case NightActionEvent:
		return reduceNightAction(gs, e)
	case TimeoutEvent:
		return reduceTimeout(gs, e)
	case VoteEvent:
		return reduceVote(gs, e)
	case NominateEvent:
		return reduceNominate(gs, e)
	case SecondEvent:
		return reduceSecond(gs, e)
	case LastWordsCompleteEvent:
		return reduceLastWordsComplete(gs)
	case AccuseEvent:
		return reduceAccuse(gs, e)
	case DefendEvent:
		return reduceDefend(gs, e)
	case WhisperEvent:
		return reduceWhisper(gs, e)
	case PlayerSpokeEvent:
		return reducePlayerSpoke(gs, e)
	case HostTransferEvent:
		return reduceHostTransfer(gs, e)
	case KickEvent:
		return reduceKick(gs, e)
	case PlayerDisconnectedEvent:
		return reduceDisconnect(gs, e)
	case TimerWarningEvent:
		return reduceWarning(gs, e)
	default:
		return gs, nil
	}
}

// armPhase sets the deadline for the phase and returns the timer effects that
// keep it moving. Every non-terminal phase must call this, otherwise the game
// parks there with no way out.
func armPhase(gs *GameState, phase Phase) []SideEffect {
	d := gs.Config.PhaseTimeout(phase)
	if d <= 0 {
		d = 60 * time.Second
	}
	gs.PhaseDeadline = time.Now().Add(d)

	effects := []SideEffect{SetTimerEffect{Duration: d, Phase: phase}}

	// Short, self-explanatory phases don't need a countdown.
	if phase == PhaseLastWords || phase == PhaseRoleAssign {
		return effects
	}
	for _, warn := range []time.Duration{60 * time.Second, 10 * time.Second} {
		if d > warn {
			effects = append(effects, SetWarningTimerEffect{
				Duration:    d - warn,
				Phase:       phase,
				SecondsLeft: int(warn.Seconds()),
			})
		}
	}
	return effects
}

func reduceGameCreated(gs *GameState) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLobby {
		return gs, nil
	}
	gs.AppendLog("game_created", map[string]interface{}{"chat_id": gs.ChatID, "host": gs.HostID})
	effects := []SideEffect{lobbyStatusEffect(gs)}
	return gs, append(effects, armPhase(gs, PhaseLobby)...)
}

// reduceResume rebuilds the volatile parts of a game after a restart: timers
// live only in memory, so a restored game has nothing driving it forward.
func reduceResume(gs *GameState) (*GameState, []SideEffect) {
	if gs.Phase.IsTerminal() {
		return gs, nil
	}

	remaining := time.Until(gs.PhaseDeadline)
	if gs.PhaseDeadline.IsZero() || remaining <= 0 {
		return reduceTimeout(gs, TimeoutEvent{Phase: gs.Phase})
	}

	effects := []SideEffect{SetTimerEffect{Duration: remaining, Phase: gs.Phase}}
	if remaining > 10*time.Second && gs.Phase != PhaseLastWords {
		effects = append(effects, SetWarningTimerEffect{
			Duration:    remaining - 10*time.Second,
			Phase:       gs.Phase,
			SecondsLeft: 10,
		})
	}

	// Re-send whatever the players need in order to act, since the original
	// prompts may have scrolled away or never arrived.
	switch gs.Phase {
	case PhaseLobby:
		effects = append(effects, lobbyStatusEffect(gs))
	case PhaseRoleAssign:
		effects = append(effects, RolesDeliveredEffect{GameID: gs.ID})
	case PhaseNight:
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"🌙 Resuming *Night %d*. Players with a night action: check your DMs.", gs.DayNumber)})
		effects = append(effects, nightPrompts(gs, true)...)
	case PhaseDiscussion, PhaseNomination:
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"☀️ Resuming *Day %d* discussion — %d seconds left.", gs.DayNumber, int(remaining.Seconds()))})
	case PhaseVoting:
		effects = append(effects, SendGroupEffect{gs.ChatID, "⚖️ Resuming the vote."})
		effects = append(effects, votingKeyboardEffect(gs))
	case PhaseLastWords:
		if gs.LastWordsPlayer != nil {
			if p, ok := gs.Players[*gs.LastWordsPlayer]; ok {
				effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
					"🎤 %s still has the floor for last words.", p.Label())})
			}
		}
	}

	gs.AppendLog("game_resumed", map[string]interface{}{"phase": string(gs.Phase)})
	return gs, effects
}

func reduceJoin(gs *GameState, e JoinEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLobby {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf(
				"%s, this game already started — you can't join mid-game. You'll be notified when the next one opens.",
				EscapeMD(displayLabel(e.Username, e.DisplayName, e.PlayerID)))},
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
		ID:          e.PlayerID,
		Username:    e.Username,
		DisplayName: e.DisplayName,
		Alive:       true,
		JoinedAt:    e.Time,
	}
	gs.AppendLog("player_joined", map[string]interface{}{"player_id": e.PlayerID, "username": e.Username})

	return gs, []SideEffect{
		lobbyStatusEffect(gs),
	}
}

func displayLabel(username, displayName string, id PlayerID) string {
	if username != "" {
		return "@" + username
	}
	if displayName != "" {
		return displayName
	}
	return fmt.Sprintf("player %d", id)
}

func reduceLeave(gs *GameState, e LeaveEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLobby {
		return gs, nil
	}
	return removeFromLobby(gs, e.PlayerID, "left the lobby")
}

// removeFromLobby deletes a player outright. Before the game starts there are
// no roles, so marking someone dead would corrupt the roster and the counts
// that role allocation depends on.
func removeFromLobby(gs *GameState, id PlayerID, reason string) (*GameState, []SideEffect) {
	p, exists := gs.Players[id]
	if !exists {
		return gs, nil
	}
	label := p.Label()
	delete(gs.Players, id)
	gs.AppendLog("player_left", map[string]interface{}{"player_id": id, "reason": reason})

	msg := fmt.Sprintf("%s %s.", label, reason)
	if id == gs.HostID {
		gs.HostID = reassignHost(gs)
		if gs.HostID != 0 {
			msg += fmt.Sprintf(" New host: %s.", gs.Players[gs.HostID].Label())
		}
	}

	if len(gs.Players) == 0 {
		gs.Phase = PhaseIdle
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, msg + " The lobby is now empty and has been closed."},
		}
	}

	return gs, []SideEffect{
		SendGroupEffect{gs.ChatID, msg},
		lobbyStatusEffect(gs),
	}
}

func lobbyStatusEffect(gs *GameState) SideEffect {
	ordered := playersByJoinTime(gs)
	players := make([]string, 0, len(ordered))
	for _, p := range ordered {
		players = append(players, p.PlainName())
	}
	hostName := ""
	if host, ok := gs.Players[gs.HostID]; ok {
		hostName = host.PlainName()
	}
	return SendLobbyStatusEffect{
		ChatID:     gs.ChatID,
		GameID:     gs.ID,
		HostName:   hostName,
		Players:    players,
		MinPlayers: gs.Config.MinPlayers,
		MaxPlayers: gs.Config.MaxPlayers,
	}
}

func playersByJoinTime(gs *GameState) []*Player {
	ordered := make([]*Player, 0, len(gs.Players))
	for _, p := range gs.Players {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].JoinedAt.Equal(ordered[j].JoinedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].JoinedAt.Before(ordered[j].JoinedAt)
	})
	return ordered
}

func playersByID(gs *GameState) []*Player {
	ordered := make([]*Player, 0, len(gs.Players))
	for _, p := range gs.Players {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	return ordered
}

func reassignHost(gs *GameState) PlayerID {
	var earliest *Player
	for _, p := range gs.Players {
		if earliest == nil || p.JoinedAt.Before(earliest.JoinedAt) ||
			(p.JoinedAt.Equal(earliest.JoinedAt) && p.ID < earliest.ID) {
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
	if e.PlayerID != gs.HostID && !e.IsAdmin {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "Only the host or a group admin can start the game."},
		}
	}
	if len(gs.Players) < gs.Config.MinPlayers {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("Not enough players. Need at least %d, have %d.", gs.Config.MinPlayers, len(gs.Players))},
		}
	}

	playerIDs := make([]PlayerID, 0, len(gs.Players))
	for pid := range gs.Players {
		playerIDs = append(playerIDs, pid)
	}
	sort.Slice(playerIDs, func(i, j int) bool { return playerIDs[i] < playerIDs[j] })

	assignment, err := AllocateRoles(playerIDs, gs.Config, rand.Reader)
	if err != nil {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "Failed to assign roles. Please try again."},
		}
	}

	gs.Phase = PhaseRoleAssign
	gs.RosterLocked = true
	gs.AppendLog("phase_change", map[string]interface{}{"phase": string(PhaseRoleAssign)})

	var effects []SideEffect
	effects = append(effects, SendGroupEffect{gs.ChatID, "🎬 The game is starting! Check your DMs for your role...\n\n⚠️ *Fair Play Reminder:* Do not screenshot or share your DM role with others. Play fair and keep the mystery alive!"})

	optionalRolesChosen := []string{}
	mafiaCount := 0
	for pid, role := range assignment {
		gs.Players[pid].Role = role
		if role != RoleVillager && role != RoleMafia {
			optionalRolesChosen = append(optionalRolesChosen, string(role))
		}
		if RoleTeam(role) == TeamMafia {
			mafiaCount++
		}
	}

	// Deterministic DM order keeps the effect stream reproducible in tests.
	for _, p := range playersByID(gs) {
		effects = append(effects, SendRoleDMEffect{
			GameID:   gs.ID,
			PlayerID: p.ID,
			Text:     formatRoleDM(p.Role),
		})
	}

	// Notify mafia members of each other (if more than one)
	if mafiaCount > 1 {
		var mafiaNames []string
		var mafiaIDs []PlayerID
		for _, p := range playersByID(gs) {
			if RoleTeam(p.Role) == TeamMafia {
				mafiaNames = append(mafiaNames, p.Label())
				mafiaIDs = append(mafiaIDs, p.ID)
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

	// The transport turns this into a RolesDeliveredEvent once the DMs above
	// have been queued; that is what starts Night 1. The timer is a backstop
	// in case the acknowledgement never comes back.
	effects = append(effects, RolesDeliveredEffect{GameID: gs.ID})
	effects = append(effects, armPhase(gs, PhaseRoleAssign)...)

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
	return fmt.Sprintf("Your role: *%s*", EscapeMD(string(role)))
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

func reduceRoleDeliveryFailed(gs *GameState, e RoleDeliveryFailedEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseRoleAssign {
		return gs, nil
	}
	p, exists := gs.Players[e.PlayerID]
	if !exists {
		return gs, nil
	}

	// Remove the player and redistribute roles (§8.3). Leaving them in with an
	// undelivered role would mean an unreachable mafia or doctor.
	label := p.Label()
	delete(gs.Players, e.PlayerID)
	gs.AppendLog("player_removed_dm_fail", map[string]interface{}{"player_id": e.PlayerID})

	if e.PlayerID == gs.HostID {
		gs.HostID = reassignHost(gs)
	}

	n := len(gs.Players)
	if n < gs.Config.MinPlayers {
		gs.Phase = PhaseLobby
		gs.RosterLocked = false
		for _, rp := range gs.Players {
			rp.Role = RoleUnassigned
		}
		effects := []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("%s could not receive their role and has been removed. Not enough players remain — back to lobby.", label)},
			lobbyStatusEffect(gs),
		}
		return gs, append(effects, armPhase(gs, PhaseLobby)...)
	}

	playerIDs := make([]PlayerID, 0, n)
	for pid := range gs.Players {
		playerIDs = append(playerIDs, pid)
	}
	sort.Slice(playerIDs, func(i, j int) bool { return playerIDs[i] < playerIDs[j] })

	assignment, err := AllocateRoles(playerIDs, gs.Config, rand.Reader)
	if err != nil {
		gs.Phase = PhaseLobby
		gs.RosterLocked = false
		for _, rp := range gs.Players {
			rp.Role = RoleUnassigned
		}
		effects := []SideEffect{
			SendGroupEffect{gs.ChatID, "Failed to reassign roles. Returning to lobby."},
			lobbyStatusEffect(gs),
		}
		return gs, append(effects, armPhase(gs, PhaseLobby)...)
	}

	var effects []SideEffect
	effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("%s could not receive their role and has been removed. Roles have been reassigned to remaining players.", label)})

	for pid, role := range assignment {
		gs.Players[pid].Role = role
	}
	for _, p := range playersByID(gs) {
		effects = append(effects, SendRoleDMEffect{
			GameID:   gs.ID,
			PlayerID: p.ID,
			Text:     formatRoleDM(p.Role),
		})
	}

	effects = append(effects, RolesDeliveredEffect{GameID: gs.ID})
	effects = append(effects, armPhase(gs, PhaseRoleAssign)...)
	return gs, effects
}

func reduceRolesDelivered(gs *GameState) (*GameState, []SideEffect) {
	if gs.Phase != PhaseRoleAssign {
		return gs, nil
	}
	return transitionToNight(gs)
}

// nightPrompts builds the per-role action DMs. When onlyPending is set it skips
// players who have already submitted, so a resume does not re-prompt them.
func nightPrompts(gs *GameState, onlyPending bool) []SideEffect {
	aliveTargets := gs.AlivePlayerIDs()
	sort.Slice(aliveTargets, func(i, j int) bool { return aliveTargets[i] < aliveTargets[j] })

	firstNight := gs.DayNumber == 1
	var effects []SideEffect

	for _, p := range playersByID(gs) {
		if !p.CanAct() {
			continue
		}
		if onlyPending {
			if _, submitted := gs.NightActions[p.ID]; submitted {
				continue
			}
		}
		switch p.Role {
		case RoleMafia, RoleGodfather:
			if firstNight && !gs.Config.FirstNightKill {
				if !onlyPending {
					effects = append(effects, SendDMEffect{p.ID, "🌙 Night 1 — no kill tonight. Use this time to strategize with your team."})
				}
			} else {
				targets := filterTargets(aliveTargets, p.ID, TeamMafia, gs)
				effects = append(effects, SendNightActionEffect{p.ID, p.Role, targets, gs.ID})
			}
		case RoleDetective:
			effects = append(effects, SendNightActionEffect{p.ID, RoleDetective, filterOutSelf(aliveTargets, p.ID), gs.ID})
		case RoleDoctor:
			targets := aliveTargets
			if !gs.Config.DoctorSelfProtect {
				targets = filterOutSelf(aliveTargets, p.ID)
			}
			effects = append(effects, SendNightActionEffect{p.ID, RoleDoctor, targets, gs.ID})
		case RoleVigilante:
			if !p.UsedAbility {
				effects = append(effects, SendNightActionEffect{p.ID, RoleVigilante, filterOutSelf(aliveTargets, p.ID), gs.ID})
			}
		}
	}
	return effects
}

func transitionToNight(gs *GameState) (*GameState, []SideEffect) {
	gs.Phase = PhaseNight
	gs.DayNumber++
	gs.NightActions = make(map[PlayerID]NightAction)
	gs.Nominations = make(map[PlayerID]*Nomination)
	gs.ActiveTrial = nil
	gs.LastWordsPlayer = nil
	gs.Accusations = make(map[PlayerID][]PlayerID)
	gs.DefenseUsed = make(map[PlayerID]bool)
	gs.Whispers = nil
	gs.SpeakCount = make(map[PlayerID]int)

	for _, p := range gs.Players {
		p.ProtectedTonight = false
	}

	gs.AppendLog("phase_change", map[string]interface{}{"phase": "night", "day": gs.DayNumber})

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, nightNarration(gs.DayNumber, len(gs.AlivePlayers()))},
	}

	// Nobody has an action to submit tonight, so arming a timer and waiting
	// out the clock would just be dead air.
	if allNightActionsSubmitted(gs) {
		resolved, resolvedEffects := resolveNight(gs)
		return resolved, append(effects, resolvedEffects...)
	}

	effects = append(effects, armPhase(gs, PhaseNight)...)
	effects = append(effects, nightPrompts(gs, false)...)
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
	// Must match the eligibility used by allNightActionsSubmitted, or a player
	// the phase is not waiting on could still steer the outcome.
	p, exists := gs.Players[e.Action.ActorID]
	if !exists || !p.CanAct() {
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

	// Mafia have no kill on the first night in the classic variant
	if e.Action.Kind == ActionMafiaKill && gs.DayNumber == 1 && !gs.Config.FirstNightKill {
		return gs, []SideEffect{
			SendDMEffect{e.Action.ActorID, "❌ There is no kill on Night 1."},
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

	confirmMsg := "✅ Action recorded."
	if hadPrevious {
		confirmMsg = "✅ Action updated. Your new target has been recorded."
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
	firstNight := gs.DayNumber == 1
	for _, p := range gs.Players {
		if !p.CanAct() {
			continue
		}
		switch p.Role {
		case RoleMafia, RoleGodfather:
			if firstNight && !gs.Config.FirstNightKill {
				continue // no action needed on first night
			}
			if _, ok := gs.NightActions[p.ID]; !ok {
				return false
			}
		case RoleDetective, RoleDoctor:
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

// sortedNightActions gives night resolution a stable order. Ranging over the
// map directly would make the outcome depend on Go's randomised map iteration.
func sortedNightActions(gs *GameState) []NightAction {
	actions := make([]NightAction, 0, len(gs.NightActions))
	for _, a := range gs.NightActions {
		actions = append(actions, a)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].ActorID < actions[j].ActorID })
	return actions
}

// actorMayAct decides whether a player killed earlier in this same night still
// completes their own action.
func actorMayAct(gs *GameState, actorID PlayerID) bool {
	if gs.Config.SimultaneousNightActions {
		return true
	}
	a, ok := gs.Players[actorID]
	return ok && a.Alive
}

func resolveNight(gs *GameState) (*GameState, []SideEffect) {
	gs.Phase = PhaseNightResolve
	gs.LastNightDeaths = nil
	gs.LastCheckResult = nil

	actions := sortedNightActions(gs)

	// Resolution order (documented in §8.4):
	// 1. Doctor protection (status flag)
	// 2. Kill sources (Mafia, Vigilante)
	// 3. Detective check (informational)

	// Step 1: Doctor protection
	for _, action := range actions {
		if action.Kind == ActionDoctorProtect {
			if target, ok := gs.Players[action.TargetID]; ok {
				target.ProtectedTonight = true
			}
		}
	}

	// Step 2a: Mafia kill (skipped on Night 1 if FirstNightKill is disabled)
	firstNight := gs.DayNumber == 1
	mafiaTarget := PlayerID(0)
	if !firstNight || gs.Config.FirstNightKill {
		mafiaTarget = resolveMafiaTarget(gs)
	}
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
	for _, action := range actions {
		if action.Kind != ActionVigilanteKill {
			continue
		}
		if !actorMayAct(gs, action.ActorID) {
			gs.AppendLog("action_cancelled", map[string]interface{}{"actor": action.ActorID, "reason": "actor_died"})
			continue
		}
		if actor, ok := gs.Players[action.ActorID]; ok {
			actor.UsedAbility = true
		}
		target, ok := gs.Players[action.TargetID]
		if !ok {
			continue
		}
		if target.ProtectedTonight {
			gs.AppendLog("kill_prevented", map[string]interface{}{"target": action.TargetID, "by": "doctor"})
			continue
		}
		if target.Alive {
			target.Alive = false
			gs.LastNightDeaths = append(gs.LastNightDeaths, action.TargetID)
			gs.AppendLog("player_killed", map[string]interface{}{"player_id": action.TargetID, "cause": "vigilante"})
		}
	}

	// Step 3: Detective check
	var effects []SideEffect
	for _, action := range actions {
		if action.Kind != ActionDetectiveCheck {
			continue
		}
		if !actorMayAct(gs, action.ActorID) {
			gs.AppendLog("action_cancelled", map[string]interface{}{"actor": action.ActorID, "reason": "actor_died"})
			continue
		}
		target, ok := gs.Players[action.TargetID]
		if !ok {
			continue
		}
		resultTeam := RoleTeam(target.Role)
		if target.Role == RoleGodfather {
			resultTeam = TeamTown // Godfather's gimmick
		}
		gs.LastCheckResult = &CheckResult{
			DetectiveID: action.ActorID,
			TargetID:    action.TargetID,
			ResultTeam:  resultTeam,
		}
		// A detective who died tonight cannot act on the result, and DMing a
		// dead player is just confusing.
		if detective, ok := gs.Players[action.ActorID]; ok && detective.Alive {
			effects = append(effects, SendDMEffect{
				PlayerID: action.ActorID,
				Text:     fmt.Sprintf("🔍 Investigation complete: %s is aligned with *%s*.", target.Label(), string(resultTeam)),
			})
		}
	}

	for _, pid := range gs.LastNightDeaths {
		if p, ok := gs.Players[pid]; ok && gs.Config.RevealOnNightKill {
			p.RoleRevealed = true
		}
	}

	// Immediate win check after night resolution (§8.8)
	if winner := checkWinCondition(gs); winner != nil {
		effects = append(effects, SendGroupEffect{gs.ChatID, formatNightDeaths(gs)})
		return endGame(gs, winner, effects)
	}

	// §8.8: Last two = 1 mafia + 1 town, currently night → short-circuit
	alive := gs.AlivePlayers()
	if len(alive) == 2 && gs.AliveMafiaCount() == 1 {
		effects = append(effects, SendGroupEffect{gs.ChatID, formatNightDeaths(gs)})
		return endGame(gs, &WinResult{Winner: TeamMafia, Description: "Mafia has taken over the town! Mafia wins! 🔪"}, effects)
	}

	// Track consecutive no-kill nights
	if len(gs.LastNightDeaths) == 0 {
		gs.ConsecutiveNoKillNights++
	} else {
		gs.ConsecutiveNoKillNights = 0
	}

	// Transition to day
	gs.Phase = PhaseDiscussion
	gs.Votes = make(map[PlayerID]Vote)
	gs.Nominations = make(map[PlayerID]*Nomination)

	effects = append(effects, SendGroupEffect{gs.ChatID, formatNightDeaths(gs)})
	effects = append(effects, SendGroupEffect{gs.ChatID, dayNarration(gs.DayNumber, len(gs.AlivePlayers()))})
	if gs.Config.NominationSystem {
		effects = append(effects, SendGroupEffect{gs.ChatID, "💬 Discuss and use /nominate @player to put someone on trial."})
	} else {
		effects = append(effects, SendGroupEffect{gs.ChatID, discussionHelpText()})
	}
	effects = append(effects, armPhase(gs, PhaseDiscussion)...)

	gs.AppendLog("phase_change", map[string]interface{}{"phase": "discussion", "day": gs.DayNumber})
	return gs, effects
}

func resolveMafiaTarget(gs *GameState) PlayerID {
	votes := make(map[PlayerID]int)
	for _, action := range sortedNightActions(gs) {
		if action.Kind == ActionMafiaKill && actorMayAct(gs, action.ActorID) {
			votes[action.TargetID]++
		}
	}
	target, _, tied := plurality(votes)
	if tied {
		return 0 // §8.4: a split mafia vote means no kill
	}
	return target
}

// plurality returns the single highest-voted entry. When two or more entries
// share the top count it reports a tie and no winner.
func plurality(tally map[PlayerID]int) (winner PlayerID, count int, tied bool) {
	ids := make([]PlayerID, 0, len(tally))
	for id := range tally {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		if tally[id] > count {
			count = tally[id]
			winner = id
		}
	}
	if count == 0 {
		return 0, 0, true
	}
	share := 0
	for _, c := range tally {
		if c == count {
			share++
		}
	}
	return winner, count, share > 1
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
		msg += p.Label()
		if gs.Config.RevealOnNightKill {
			msg += fmt.Sprintf(" (%s)", string(p.Role))
		}
	}
	msg += " was found dead!"
	return msg
}

func formatGameOver(gs *GameState) string {
	description := "The game ended early."
	if gs.Winner != nil {
		description = gs.Winner.Description
	}
	msg := fmt.Sprintf("🏆 *Game Over!* %s\n\n*Final Roles:*\n", description)
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
		msg += fmt.Sprintf("%s %s — %s (%s)\n", status, p.Label(), string(p.Role), RoleTeam(p.Role))
	}
	if len(gs.JesterWon) > 0 {
		msg += "\n🃏 Jester winners: "
		for i, pid := range gs.JesterWon {
			if i > 0 {
				msg += ", "
			}
			msg += gs.Players[pid].Label()
		}
	}
	return msg
}

// endGame is the single exit point for a finished game so the phase, winner,
// reveal state, and effect ordering stay consistent.
func endGame(gs *GameState, winner *WinResult, effects []SideEffect) (*GameState, []SideEffect) {
	gs.Phase = PhaseGameOver
	gs.Winner = winner
	gs.PhaseDeadline = time.Time{}
	for _, p := range gs.Players {
		p.RoleRevealed = true
	}
	gs.AppendLog("game_over", map[string]interface{}{"winner": string(winner.Winner)})
	effects = append(effects, SendGroupEffect{gs.ChatID, formatGameOver(gs)})
	effects = append(effects, GameOverEffect{GameID: gs.ID, Result: *winner})
	return gs, effects
}

func votingKeyboardEffect(gs *GameState) SideEffect {
	targets := gs.AlivePlayerIDs()
	prompt := "🗳️ *Day Vote*\nChoose one player below:"
	if gs.ActiveTrial != nil {
		targets = []PlayerID{*gs.ActiveTrial}
		if p, ok := gs.Players[*gs.ActiveTrial]; ok {
			prompt = fmt.Sprintf("🗳️ *Trial Vote*\nGuilty (lynch %s) or innocent (skip)?", p.Label())
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
	return SendVotingKeyboardEffect{
		ChatID:       gs.ChatID,
		GameID:       gs.ID,
		Targets:      targets,
		AllowNoLynch: gs.Config.AllowNoLynch,
		Prompt:       prompt,
	}
}

func reduceVote(gs *GameState, e VoteEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseVoting {
		return gs, nil
	}
	// Must match EligibleVoterCount, otherwise a player excluded from the
	// quorum could still cast the deciding ballot.
	voter, exists := gs.Players[e.Vote.VoterID]
	if !exists || !voter.CanAct() {
		return gs, nil
	}

	// Validate target
	if e.Vote.TargetID != NoLynchTarget {
		target, exists := gs.Players[e.Vote.TargetID]
		if !exists || !target.Alive {
			return gs, nil
		}
		// During a trial only the accused can be voted for.
		if gs.ActiveTrial != nil && e.Vote.TargetID != *gs.ActiveTrial {
			return gs, nil
		}
	} else if !gs.Config.AllowNoLynch {
		return gs, nil
	}

	_, isChange := gs.Votes[e.Vote.VoterID]
	gs.Votes[e.Vote.VoterID] = e.Vote
	gs.AppendLog("vote_cast", map[string]interface{}{"voter": e.Vote.VoterID, "target": e.Vote.TargetID, "changed": isChange})

	verb := "voted"
	if isChange {
		verb = "changed their vote"
	}
	choice := "*No Lynch*"
	if e.Vote.TargetID != NoLynchTarget {
		choice = gs.Players[e.Vote.TargetID].Label()
	}
	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("%s %s for %s. (%d/%d votes in)",
			voter.Label(), verb, choice, len(gs.Votes), gs.EligibleVoterCount())},
	}

	// Only players who can actually vote are counted, so one dropout does not
	// force every round to run the full clock.
	if len(gs.Votes) >= gs.EligibleVoterCount() {
		resolvedState, resolvedEffects := resolveLynch(gs)
		return resolvedState, append(effects, resolvedEffects...)
	}

	return gs, effects
}

// lynchThreshold is the number of votes needed to execute someone.
func lynchThreshold(gs *GameState) int {
	if !gs.Config.LynchRequiresMajority {
		return 1
	}
	eligible := gs.EligibleVoterCount()
	if eligible < 1 {
		return 1
	}
	return eligible/2 + 1
}

func resolveLynch(gs *GameState) (*GameState, []SideEffect) {
	gs.Phase = PhaseLynchResolve

	tally := make(map[PlayerID]int)
	for _, v := range gs.Votes {
		tally[v.TargetID]++
	}
	lynchTarget, maxVotes, tied := plurality(tally)
	threshold := lynchThreshold(gs)

	var effects []SideEffect

	switch {
	case tied:
		effects = append(effects, SendGroupEffect{gs.ChatID, "⚖️ The vote is tied. No one is lynched today."})
		gs.AppendLog("no_lynch", map[string]interface{}{"reason": "tie"})

	case lynchTarget == NoLynchTarget:
		effects = append(effects, SendGroupEffect{gs.ChatID, "⚖️ The town votes to spare everyone. No one is lynched today."})
		gs.AppendLog("no_lynch", map[string]interface{}{"reason": "no_lynch_wins"})

	case maxVotes < threshold:
		// Without this, a single vote in a quiet round decides an execution.
		target := gs.Players[lynchTarget]
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"⚖️ %s led the vote with %d of the %d needed for a majority. No one is lynched today.",
			target.Label(), maxVotes, threshold)})
		gs.AppendLog("no_lynch", map[string]interface{}{"reason": "no_majority", "votes": maxVotes, "needed": threshold})

	default:
		target := gs.Players[lynchTarget]
		if !target.Alive {
			// Defensive: re-validate target is alive (§8.6)
			effects = append(effects, SendGroupEffect{gs.ChatID, "⚖️ No valid lynch target. No one is lynched."})
			break
		}
		if gs.Config.AllowLastWords {
			gs.Phase = PhaseLastWords
			gs.LastWordsPlayer = &lynchTarget
			effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
				"⚖️ The town has decided. %s, you have %d seconds for your last words...",
				target.Label(), gs.Config.LastWordsSec)})
			effects = append(effects, armPhase(gs, PhaseLastWords)...)
			return gs, effects
		}
		return executeLynch(gs, lynchTarget, maxVotes, effects)
	}

	if winner := checkWinCondition(gs); winner != nil {
		return endGame(gs, winner, effects)
	}

	nightState, nightEffects := transitionToNight(gs)
	return nightState, append(effects, nightEffects...)
}

func executeLynch(gs *GameState, lynchTarget PlayerID, maxVotes int, effects []SideEffect) (*GameState, []SideEffect) {
	target := gs.Players[lynchTarget]
	target.Alive = false
	gs.ActiveTrial = nil
	gs.AppendLog("player_lynched", map[string]interface{}{"player_id": lynchTarget, "votes": maxVotes})

	roleReveal := ""
	if gs.Config.RevealOnLynch {
		target.RoleRevealed = true
		roleReveal = fmt.Sprintf(" They were a *%s*.", string(target.Role))
	}
	effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
		"💀 %s has been executed by the town.%s", target.Label(), roleReveal)})

	// Jester win check (§2.2)
	if target.Role == RoleJester {
		gs.JesterWon = append(gs.JesterWon, lynchTarget)
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"🃏 %s was the *Jester* and wins! The game continues for everyone else.", target.Label())})
	}

	if winner := checkWinCondition(gs); winner != nil {
		return endGame(gs, winner, effects)
	}

	nightState, nightEffects := transitionToNight(gs)
	return nightState, append(effects, nightEffects...)
}

func checkWinCondition(gs *GameState) *WinResult {
	// Before roles are dealt every role is the empty string, which maps to
	// town — so an unstarted game would immediately report "all mafia dead".
	if !gs.Started() {
		return nil
	}

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
		if p.CanAct() {
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
			return reduceBegin(gs, BeginEvent{PlayerID: gs.HostID, IsAdmin: true})
		}
		gs.Phase = PhaseIdle
		gs.PhaseDeadline = time.Time{}
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "⏰ Lobby timed out without enough players. Game cancelled."},
		}

	case PhaseRoleAssign:
		// The delivery acknowledgement never came back; start anyway rather
		// than leaving the game parked forever.
		return transitionToNight(gs)

	case PhaseNight:
		return resolveNight(gs)

	case PhaseDiscussion:
		gs.Phase = PhaseVoting
		gs.Votes = make(map[PlayerID]Vote)
		gs.AppendLog("phase_change", map[string]interface{}{"phase": "voting"})

		var effects []SideEffect
		effects = append(effects, SendGroupEffect{gs.ChatID, formatDiscussionSummary(gs)})
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"⚖️ *Voting phase!* You have %d seconds to cast your vote. %d votes are needed to lynch.",
			gs.Config.VotingTimeoutSec, lynchThreshold(gs))})
		effects = append(effects, votingKeyboardEffect(gs))
		return gs, append(effects, armPhase(gs, PhaseVoting)...)

	case PhaseNomination:
		gs.Phase = PhaseLynchResolve
		effects := []SideEffect{
			SendGroupEffect{gs.ChatID, "⏰ No nomination was seconded. No one is lynched today."},
		}
		if winner := checkWinCondition(gs); winner != nil {
			return endGame(gs, winner, effects)
		}
		nightState, nightEffects := transitionToNight(gs)
		return nightState, append(effects, nightEffects...)

	case PhaseVoting:
		if len(gs.Votes) == 0 {
			gs.Phase = PhaseLynchResolve
			effects := []SideEffect{
				SendGroupEffect{gs.ChatID, "⏰ No votes were cast. No one is lynched."},
			}
			if winner := checkWinCondition(gs); winner != nil {
				return endGame(gs, winner, effects)
			}
			nightState, nightEffects := transitionToNight(gs)
			return nightState, append(effects, nightEffects...)
		}
		return resolveLynch(gs)

	case PhaseLastWords:
		return reduceLastWordsComplete(gs)
	}

	return gs, nil
}

func reduceDisconnect(gs *GameState, e PlayerDisconnectedEvent) (*GameState, []SideEffect) {
	p, exists := gs.Players[e.PlayerID]
	if !exists {
		return gs, nil
	}

	// In the lobby there are no roles yet, so treat this as leaving rather
	// than as a casualty — otherwise the win check sees zero mafia.
	if gs.Phase == PhaseLobby {
		return removeFromLobby(gs, e.PlayerID, "left the chat and was removed from the lobby")
	}

	if p.Disconnected {
		return gs, nil
	}
	p.Disconnected = true
	gs.AppendLog("player_disconnected", map[string]interface{}{"player_id": e.PlayerID})

	// They no longer count toward any quorum, so anything they already
	// submitted has to go with them — otherwise the tally can exceed the
	// number of players the phase is waiting on.
	delete(gs.Votes, e.PlayerID)
	delete(gs.NightActions, e.PlayerID)

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("📵 %s has gone silent and can no longer act.", p.Label())},
	}

	if winner := checkWinCondition(gs); winner != nil {
		return endGame(gs, winner, effects)
	}

	// Losing this player may have been the last thing a phase was waiting on.
	switch gs.Phase {
	case PhaseNight:
		if allNightActionsSubmitted(gs) {
			resolved, resolvedEffects := resolveNight(gs)
			return resolved, append(effects, resolvedEffects...)
		}
	case PhaseVoting:
		if len(gs.Votes) >= gs.EligibleVoterCount() {
			resolved, resolvedEffects := resolveLynch(gs)
			return resolved, append(effects, resolvedEffects...)
		}
	}

	return gs, effects
}

func reduceWarning(gs *GameState, e TimerWarningEvent) (*GameState, []SideEffect) {
	if gs.Phase != e.Phase {
		return gs, nil
	}
	var location string
	switch e.Phase {
	case PhaseLobby:
		location = "joining"
	case PhaseNight:
		location = "night actions"
	case PhaseDiscussion:
		location = "discussion"
	case PhaseNomination:
		location = "seconding a nomination"
	case PhaseVoting:
		location = "voting"
	default:
		location = "this phase"
	}
	return gs, []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("⏰ *%d seconds* remaining for %s!", e.SecondsLeft, location)},
	}
}

func reduceNominate(gs *GameState, e NominateEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseDiscussion && gs.Phase != PhaseNomination {
		return gs, nil
	}
	if !gs.Config.NominationSystem {
		return gs, nil
	}

	nominator, exists := gs.Players[e.NominatorID]
	if !exists || !nominator.Alive {
		return gs, nil
	}
	target, exists := gs.Players[e.TargetID]
	if !exists || !target.Alive {
		return gs, nil
	}
	if e.NominatorID == e.TargetID {
		return gs, nil // can't nominate yourself
	}

	if _, exists := gs.Nominations[e.TargetID]; exists {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("%s is already nominated.", target.Label())},
		}
	}

	gs.Nominations[e.TargetID] = &Nomination{
		NominatorID: e.NominatorID,
		TargetID:    e.TargetID,
		Time:        time.Now(),
	}
	gs.AppendLog("nomination", map[string]interface{}{"nominator": e.NominatorID, "target": e.TargetID})

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"⚠️ %s nominates %s for trial! Someone must /second this nomination.",
			nominator.Label(), target.Label())},
	}

	// Entering the nomination window needs its own clock; the discussion timer
	// is for a phase we just left and would be ignored when it fires.
	if gs.Phase == PhaseDiscussion {
		gs.Phase = PhaseNomination
		gs.AppendLog("phase_change", map[string]interface{}{"phase": "nomination"})
		effects = append(effects, armPhase(gs, PhaseNomination)...)
	}

	return gs, effects
}

func reduceSecond(gs *GameState, e SecondEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseNomination {
		return gs, nil
	}

	seconder, exists := gs.Players[e.PlayerID]
	if !exists || !seconder.Alive {
		return gs, nil
	}

	nom, exists := gs.Nominations[e.NominationTarget]
	if !exists {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "There is no active nomination for that player."},
		}
	}

	if e.PlayerID == nom.NominatorID {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "You cannot second your own nomination."},
		}
	}

	target, exists := gs.Players[e.NominationTarget]
	if !exists || !target.Alive {
		return gs, nil
	}

	nom.SecondedBy = e.PlayerID
	gs.AppendLog("nomination_seconded", map[string]interface{}{"seconder": e.PlayerID, "target": e.NominationTarget})

	trialTarget := e.NominationTarget
	gs.Phase = PhaseVoting
	gs.ActiveTrial = &trialTarget
	gs.Votes = make(map[PlayerID]Vote)

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"⚖️ %s seconds the nomination! %s is now on trial.\n\nVote guilty (lynch) or innocent (skip). %d votes are needed to convict. You have %d seconds.",
			seconder.Label(), target.Label(), lynchThreshold(gs), gs.Config.VotingTimeoutSec)},
		votingKeyboardEffect(gs),
	}
	return gs, append(effects, armPhase(gs, PhaseVoting)...)
}

func reduceLastWordsComplete(gs *GameState) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLastWords || gs.LastWordsPlayer == nil {
		return gs, nil
	}

	lynchTarget := *gs.LastWordsPlayer
	gs.LastWordsPlayer = nil
	gs.Phase = PhaseLynchResolve

	// A kick can land while the condemned still has the floor, in which case
	// there is no longer an execution to carry out.
	target, exists := gs.Players[lynchTarget]
	if !exists || !target.Alive {
		gs.ActiveTrial = nil
		effects := []SideEffect{
			SendGroupEffect{gs.ChatID, "⚖️ The condemned player is already out of the game. No execution takes place."},
		}
		if winner := checkWinCondition(gs); winner != nil {
			return endGame(gs, winner, effects)
		}
		nightState, nightEffects := transitionToNight(gs)
		return nightState, append(effects, nightEffects...)
	}

	maxVotes := 0
	for _, v := range gs.Votes {
		if v.TargetID == lynchTarget {
			maxVotes++
		}
	}

	return executeLynch(gs, lynchTarget, maxVotes, nil)
}

func reduceAccuse(gs *GameState, e AccuseEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseDiscussion && gs.Phase != PhaseNomination {
		return gs, nil
	}
	accuser, exists := gs.Players[e.AccuserID]
	if !exists || !accuser.Alive {
		return gs, nil
	}
	target, exists := gs.Players[e.TargetID]
	if !exists || !target.Alive {
		return gs, nil
	}
	if e.AccuserID == e.TargetID {
		return gs, nil
	}

	if gs.Accusations == nil {
		gs.Accusations = make(map[PlayerID][]PlayerID)
	}

	for _, aid := range gs.Accusations[e.TargetID] {
		if aid == e.AccuserID {
			return gs, []SideEffect{
				SendGroupEffect{gs.ChatID, fmt.Sprintf("%s, you already accused %s.", accuser.Label(), target.Label())},
			}
		}
	}

	gs.Accusations[e.TargetID] = append(gs.Accusations[e.TargetID], e.AccuserID)
	gs.AppendLog("accusation", map[string]interface{}{"accuser": e.AccuserID, "target": e.TargetID})

	count := len(gs.Accusations[e.TargetID])
	aliveCount := len(gs.AlivePlayers())

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("👉 %s accuses %s! (%d/%d accusations)",
			accuser.Label(), target.Label(), count, aliveCount)},
	}

	if count > aliveCount/2 {
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"⚠️ %s has been accused by the majority! %s, use /defend to make your case.",
			target.Label(), target.Label())})
	}

	return gs, effects
}

func reduceDefend(gs *GameState, e DefendEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseDiscussion && gs.Phase != PhaseNomination {
		return gs, nil
	}
	player, exists := gs.Players[e.PlayerID]
	if !exists || !player.Alive {
		return gs, nil
	}

	if gs.DefenseUsed == nil {
		gs.DefenseUsed = make(map[PlayerID]bool)
	}
	if gs.DefenseUsed[e.PlayerID] {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("%s, you've already made your defense today.", player.Label())},
		}
	}

	gs.DefenseUsed[e.PlayerID] = true
	gs.AppendLog("defense", map[string]interface{}{"player": e.PlayerID})

	statement := EscapeMD(TruncateRunes(e.Statement, 500))

	return gs, []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("🛡️ *Defense from* %s:\n\"%s\"", player.Label(), statement)},
	}
}

func reduceWhisper(gs *GameState, e WhisperEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseDiscussion && gs.Phase != PhaseNomination {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "Whispers are only allowed during the day discussion."},
		}
	}
	from, exists := gs.Players[e.FromID]
	if !exists || !from.Alive {
		return gs, nil
	}
	to, exists := gs.Players[e.ToID]
	if !exists || !to.Alive {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "That player is not alive."},
		}
	}
	if e.FromID == e.ToID {
		return gs, nil
	}

	msg := TruncateRunes(e.Message, 200)

	gs.Whispers = append(gs.Whispers, Whisper{
		FromID:  e.FromID,
		ToID:    e.ToID,
		Message: msg,
		Time:    time.Now(),
	})
	gs.AppendLog("whisper", map[string]interface{}{"from": e.FromID, "to": e.ToID})

	escaped := EscapeMD(msg)
	return gs, []SideEffect{
		SendDMEffect{e.ToID, fmt.Sprintf("🤫 *Whisper from* %s: %s", from.Label(), escaped)},
		SendDMEffect{e.FromID, fmt.Sprintf("🤫 Whisper sent to %s.", to.Label())},
		// The group learns that a whisper happened, but not what it said.
		SendGroupEffect{gs.ChatID, fmt.Sprintf("🤫 %s whispered something to %s...", from.Label(), to.Label())},
	}
}

func reducePlayerSpoke(gs *GameState, e PlayerSpokeEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseDiscussion && gs.Phase != PhaseNomination {
		return gs, nil
	}
	if _, exists := gs.Players[e.PlayerID]; !exists {
		return gs, nil
	}
	if gs.SpeakCount == nil {
		gs.SpeakCount = make(map[PlayerID]int)
	}
	gs.SpeakCount[e.PlayerID]++
	return gs, nil
}

func reduceHostTransfer(gs *GameState, e HostTransferEvent) (*GameState, []SideEffect) {
	if gs.Phase.IsTerminal() {
		return gs, nil
	}
	if e.FromPlayerID != gs.HostID && !e.IsAdmin {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "Only the current host or a group admin can transfer host."},
		}
	}
	target, exists := gs.Players[e.ToPlayerID]
	if !exists {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "That player is not in this game."},
		}
	}
	// A dead or disconnected host would leave host-only commands stranded.
	if !target.CanAct() {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, fmt.Sprintf("%s can't host — they are no longer active in this game.", target.Label())},
		}
	}
	gs.HostID = e.ToPlayerID
	gs.AppendLog("host_transfer", map[string]interface{}{"from": e.FromPlayerID, "to": e.ToPlayerID})
	return gs, []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("👑 Host transferred to %s.", target.Label())},
	}
}

func reduceKick(gs *GameState, e KickEvent) (*GameState, []SideEffect) {
	if gs.Phase.IsTerminal() {
		return gs, nil
	}
	if e.HostID != gs.HostID && !e.IsAdmin {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "Only the host or a group admin can kick players."},
		}
	}
	target, exists := gs.Players[e.TargetID]
	if !exists {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "That player is not in this game."},
		}
	}

	// Before the game starts there is no such thing as a dead player, so the
	// kick has to actually remove them from the roster.
	if gs.Phase == PhaseLobby {
		return removeFromLobby(gs, e.TargetID, "was removed from the lobby by the host")
	}

	if !target.Alive {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "That player is already out of the game."},
		}
	}

	target.Alive = false
	target.Disconnected = true
	delete(gs.Votes, e.TargetID)
	delete(gs.NightActions, e.TargetID)
	gs.AppendLog("player_kicked", map[string]interface{}{"player_id": e.TargetID, "by": e.HostID})

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("🚪 %s has been kicked from the game by the host.", target.Label())},
	}

	if winner := checkWinCondition(gs); winner != nil {
		return endGame(gs, winner, effects)
	}

	switch gs.Phase {
	case PhaseNight:
		if allNightActionsSubmitted(gs) {
			resolved, resolvedEffects := resolveNight(gs)
			return resolved, append(effects, resolvedEffects...)
		}
	case PhaseVoting:
		if len(gs.Votes) >= gs.EligibleVoterCount() {
			resolved, resolvedEffects := resolveLynch(gs)
			return resolved, append(effects, resolvedEffects...)
		}
	}

	return gs, effects
}

func reduceEndGame(gs *GameState, e EndGameEvent) (*GameState, []SideEffect) {
	if gs.Phase.IsTerminal() {
		return gs, nil
	}
	if e.PlayerID != gs.HostID && !e.IsAdmin {
		return gs, []SideEffect{
			SendGroupEffect{gs.ChatID, "Only the host or a group admin can end the game."},
		}
	}
	gs.AppendLog("game_ended_by_host", map[string]interface{}{"host": e.PlayerID})
	return endGame(gs,
		&WinResult{Winner: "", Description: "The game was ended early by the host. No winner."},
		[]SideEffect{SendGroupEffect{gs.ChatID, "🛑 The game has been ended by the host."}},
	)
}

// Narrator-style thematic announcements
func nightNarration(dayNum int, aliveCount int) string {
	narrations := []string{
		"🌙 *Night %d* — The town falls silent. Doors are locked, curtains drawn. Somewhere in the darkness, evil stirs...",
		"🌑 *Night %d* — A cold wind sweeps through the empty streets. The townspeople retreat to their homes, hoping to see the dawn...",
		"🌙 *Night %d* — The moon casts long shadows across the town square. %d souls lie awake, wondering who among them is the wolf...",
		"🌑 *Night %d* — The clock tower strikes midnight. In the darkness, allegiances are tested and fates are sealed...",
		"🌙 *Night %d* — Candles flicker and die. The town holds its breath as the night claims another victim — or does it?",
	}
	idx := (dayNum - 1) % len(narrations)
	if idx < 0 {
		idx = 0
	}
	text := narrations[idx]
	if idx == 2 {
		return fmt.Sprintf(text, dayNum, aliveCount)
	}
	return fmt.Sprintf(text, dayNum)
}

func dayNarration(dayNum int, aliveCount int) string {
	narrations := []string{
		"☀️ *Day %d* — The survivors gather in the town square. %d remain. Someone here is not who they claim to be...",
		"☀️ *Day %d* — Dawn breaks. The %d remaining townsfolk eye each other with suspicion. Time to find the truth.",
		"☀️ *Day %d* — Another morning, another chance for justice. %d players remain — discuss and decide who to put on trial.",
		"☀️ *Day %d* — The rooster crows. %d anxious faces gather. Today, the town must act — or the mafia will strike again tonight.",
		"☀️ *Day %d* — Sunlight exposes what darkness conceals. Among the %d of you, wolves walk in sheep's clothing.",
	}
	idx := (dayNum - 1) % len(narrations)
	if idx < 0 {
		idx = 0
	}
	return fmt.Sprintf(narrations[idx], dayNum, aliveCount)
}

func formatDiscussionSummary(gs *GameState) string {
	msg := "📋 *Discussion Summary:*\n"

	if len(gs.Accusations) > 0 {
		type entry struct {
			label string
			count int
		}
		var entries []entry
		for targetID, accusers := range gs.Accusations {
			if target, ok := gs.Players[targetID]; ok {
				entries = append(entries, entry{target.Label(), len(accusers)})
			}
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].count != entries[j].count {
				return entries[i].count > entries[j].count
			}
			return entries[i].label < entries[j].label
		})
		msg += "\n*Accusations:*\n"
		for _, e := range entries {
			msg += fmt.Sprintf("  👉 %s — %d accusation(s)\n", e.label, e.count)
		}
	} else {
		msg += "\n_No accusations were made._\n"
	}

	if len(gs.Whispers) > 0 {
		msg += fmt.Sprintf("\n🤫 %d whisper(s) exchanged.\n", len(gs.Whispers))
	}

	var silent []string
	for _, p := range playersByJoinTime(gs) {
		if !p.Alive {
			continue
		}
		if gs.SpeakCount == nil || gs.SpeakCount[p.ID] == 0 {
			silent = append(silent, p.Label())
		}
	}
	if len(silent) > 0 {
		msg += "\n😶 *Silent players:* " + joinStrings(silent) + "\n"
	}

	return msg
}

func discussionHelpText() string {
	return `💬 *Discussion Commands:*
• /accuse @player — publicly accuse someone
• /defend [statement] — make your defense (once per day)
• /whisper @player [message] — send a private whisper (group sees it happened!)
• /status — view game status

_Discuss, argue, bluff, and deceive. The vote is coming..._`
}
