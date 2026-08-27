package engine

import (
	"fmt"
	"sort"
)

// nightPrompts builds the per-role action DMs. When onlyPending is set it skips
// players who have already submitted, so a resume does not re-prompt them.
func nightPrompts(gs *GameState, onlyPending bool) []SideEffect {
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

		targets := roleTargets(gs, p)
		if len(targets) == 0 {
			// The mafia still get their quiet first night, and a role with no
			// action at all deserves to be told so rather than left guessing.
			if !onlyPending {
				if msg := idleNightMessage(gs, p); msg != "" {
					effects = append(effects, SendDMEffect{p.ID, msg})
				}
			}
			continue
		}
		effects = append(effects, SendNightActionEffect{p.ID, p.Role, targets, gs.ID})
	}
	return effects
}

// idleNightMessage is what a player with nothing to submit hears at night.
func idleNightMessage(gs *GameState, p *Player) string {
	info := RoleInfoFor(p.Role)

	if info.ActionKind == ActionMafiaKill && gs.DayNumber == 1 && !gs.Config.FirstNightKill {
		if gs.Config.MafiaNightChat {
			return "🌙 *Night 1* — no kill tonight. Use `/mafia <message>` to plan with your team."
		}
		return "🌙 *Night 1* — no kill tonight. Use the time to plan with your team."
	}
	if info.OneShot && p.UsedAbility {
		return "🌙 The night falls. Your one-time ability is already spent — sit tight."
	}
	if !info.HasNightAction() {
		return fmt.Sprintf("🌙 *Night %d* — you have no night action. Rest up and listen carefully tomorrow.", gs.DayNumber)
	}
	return ""
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
	if !roleNeedsAction(gs, p) {
		return gs, []SideEffect{
			SendDMEffect{e.Action.ActorID, "❌ You have no action to submit tonight."},
		}
	}

	// The target list is the authority on legality, so a hand-crafted
	// callback cannot reach a player the keyboard never offered.
	if !containsPlayer(roleTargets(gs, p), e.Action.TargetID) {
		return gs, []SideEffect{
			SendDMEffect{e.Action.ActorID, "❌ That is not a valid target for you tonight."},
		}
	}

	_, hadPrevious := gs.NightActions[e.Action.ActorID]
	gs.NightActions[e.Action.ActorID] = e.Action
	gs.AppendLog("night_action", map[string]interface{}{
		"actor": e.Action.ActorID,
		"kind":  e.Action.Kind,
	})

	target := gs.Players[e.Action.TargetID]
	confirmMsg := fmt.Sprintf("✅ Locked in: *%s*.", target.Label())
	if hadPrevious {
		confirmMsg = fmt.Sprintf("🔄 Changed to *%s*.", target.Label())
	}
	if remaining := pendingActorCount(gs); remaining > 0 {
		confirmMsg += fmt.Sprintf("\n_Waiting on %d more player(s)._", remaining)
	}

	effects := []SideEffect{SendDMEffect{e.Action.ActorID, confirmMsg}}

	// Teammates see each other's picks so they can actually coordinate.
	if e.Action.Kind == ActionMafiaKill {
		for _, mate := range gs.TeammatesOf(e.Action.ActorID) {
			effects = append(effects, SendDMEffect{mate.ID, fmt.Sprintf(
				"🔪 %s wants to kill *%s*.", p.Label(), target.Label())})
		}
	}

	if allNightActionsSubmitted(gs) {
		resolvedState, resolvedEffects := resolveNight(gs)
		return resolvedState, append(effects, resolvedEffects...)
	}
	return gs, effects
}

func containsPlayer(list []PlayerID, id PlayerID) bool {
	for _, candidate := range list {
		if candidate == id {
			return true
		}
	}
	return false
}

func allNightActionsSubmitted(gs *GameState) bool {
	return pendingActorCount(gs) == 0
}

// pendingActorCount is how many players the night is still waiting on.
func pendingActorCount(gs *GameState) int {
	pending := 0
	for _, p := range gs.Players {
		if !roleNeedsAction(gs, p) {
			continue
		}
		if _, ok := gs.NightActions[p.ID]; !ok {
			pending++
		}
	}
	return pending
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

// actionResolves combines the two ways a submitted action can come to nothing:
// the actor died before it landed, or somebody occupied them for the evening.
func actionResolves(gs *GameState, actorID PlayerID) bool {
	if !actorMayAct(gs, actorID) {
		return false
	}
	a, ok := gs.Players[actorID]
	return ok && !a.BlockedTonight
}

// nightResolution carries the bookkeeping for one night so the individual
// steps do not have to thread half a dozen maps between them.
type nightResolution struct {
	// protectedBy credits a prevented kill to whoever prevented it.
	protectedBy map[PlayerID]PlayerID
	// guardedBy lists the bodyguards standing over each player.
	guardedBy map[PlayerID][]PlayerID
	// causes records how each of tonight's victims died, for the dawn report.
	causes map[PlayerID]string
	// effects collects DMs produced while resolving.
	effects []SideEffect
}

func resolveNight(gs *GameState) (*GameState, []SideEffect) {
	gs.Phase = PhaseNightResolve
	gs.LastNightDeaths = nil
	gs.LastCheckResult = nil
	gs.NightVisits = make(map[PlayerID][]PlayerID)

	for _, p := range gs.Players {
		p.BlockedTonight = false
		p.FramedTonight = false
	}

	res := &nightResolution{
		protectedBy: make(map[PlayerID]PlayerID),
		guardedBy:   make(map[PlayerID][]PlayerID),
		causes:      make(map[PlayerID]string),
	}

	actions := sortedNightActions(gs)

	// Resolution order. Each step only depends on the ones before it, which
	// is what keeps the whole night deterministic:
	//   1. roleblocks    — decide whose actions happen at all
	//   2. visits        — record who went where, for the Lookout
	//   3. framing       — alter what an investigation will report
	//   4. protection    — doctors heal, bodyguards take up position
	//   5. kills         — mafia, serial killer, vigilante
	//   6. information   — detective and lookout results
	//   7. grief         — lovers follow each other into the grave
	resolveRoleblocks(gs, actions, res)
	recordVisits(gs, actions)
	resolveFraming(gs, actions)
	resolveProtection(gs, actions, res)
	resolveKills(gs, actions, res)
	resolveInformation(gs, actions, res)

	for _, pid := range gs.LastNightDeaths {
		if p, ok := gs.Players[pid]; ok && gs.Config.RevealOnNightKill {
			p.RoleRevealed = true
		}
	}

	dawn := formatNightDeaths(gs, res.causes)
	effects := res.effects
	if len(gs.LastNightDeaths) == 0 {
		gs.AddTimeline("🌅", "A quiet night — nobody died.")
	}

	if winner := checkWinCondition(gs); winner != nil {
		effects = append(effects, SendGroupEffect{gs.ChatID, dawn})
		return endGame(gs, winner, effects)
	}

	if len(gs.LastNightDeaths) == 0 {
		gs.ConsecutiveNoKillNights++
	} else {
		gs.ConsecutiveNoKillNights = 0
	}

	return transitionToDay(gs, dawn, effects)
}

func resolveRoleblocks(gs *GameState, actions []NightAction, res *nightResolution) {
	for _, action := range actions {
		if action.Kind != ActionEscortBlock {
			continue
		}
		// A roleblock cannot itself be blocked — otherwise two escorts
		// pointing at each other would need an arbitrary tiebreak.
		if !actorMayAct(gs, action.ActorID) {
			continue
		}
		target, ok := gs.Players[action.TargetID]
		if !ok || !target.Alive {
			continue
		}
		target.BlockedTonight = true
		gs.AppendLog("roleblocked", map[string]interface{}{"actor": action.ActorID, "target": action.TargetID})
		res.effects = append(res.effects, SendDMEffect{action.TargetID,
			"💃 Someone kept you busy all night. Whatever you planned did not happen."})
	}
}

// recordVisits notes who called on whom, which is the Lookout's whole payoff.
// The mafia are handled separately: only the member who carries out the kill
// is seen, so the whole team is not exposed by one stakeout.
func recordVisits(gs *GameState, actions []NightAction) {
	for _, action := range actions {
		if action.Kind == ActionMafiaKill || action.Kind == ActionLookoutWatch {
			continue
		}
		if !actionResolves(gs, action.ActorID) {
			continue
		}
		addVisit(gs, action.TargetID, action.ActorID)
	}

	if triggerman, target := mafiaKillDecision(gs); target != 0 {
		addVisit(gs, target, triggerman)
	}
}

func addVisit(gs *GameState, target, visitor PlayerID) {
	if gs.NightVisits == nil {
		gs.NightVisits = make(map[PlayerID][]PlayerID)
	}
	for _, existing := range gs.NightVisits[target] {
		if existing == visitor {
			return
		}
	}
	gs.NightVisits[target] = append(gs.NightVisits[target], visitor)
}

func resolveFraming(gs *GameState, actions []NightAction) {
	for _, action := range actions {
		if action.Kind != ActionFramerFrame || !actionResolves(gs, action.ActorID) {
			continue
		}
		if target, ok := gs.Players[action.TargetID]; ok && target.Alive {
			target.FramedTonight = true
			gs.AppendLog("framed", map[string]interface{}{"actor": action.ActorID, "target": action.TargetID})
		}
	}
}

func resolveProtection(gs *GameState, actions []NightAction, res *nightResolution) {
	for _, action := range actions {
		if !actionResolves(gs, action.ActorID) {
			continue
		}
		target, ok := gs.Players[action.TargetID]
		if !ok || !target.Alive {
			continue
		}
		switch action.Kind {
		case ActionDoctorProtect:
			target.ProtectedTonight = true
			res.protectedBy[action.TargetID] = action.ActorID
		case ActionBodyguardGuard:
			res.guardedBy[action.TargetID] = append(res.guardedBy[action.TargetID], action.ActorID)
		}
	}
}

// mafiaKillDecision returns the mafia's agreed victim and the member who
// carries out the kill. A split vote means no kill at all.
func mafiaKillDecision(gs *GameState) (triggerman, target PlayerID) {
	if gs.DayNumber == 1 && !gs.Config.FirstNightKill {
		return 0, 0
	}
	votes := make(map[PlayerID]int)
	for _, action := range sortedNightActions(gs) {
		if action.Kind == ActionMafiaKill && actionResolves(gs, action.ActorID) {
			votes[action.TargetID]++
		}
	}
	victim, _, tied := plurality(votes)
	if tied || victim == 0 {
		return 0, 0
	}
	// The lowest-numbered member who backed the winning target pulls the
	// trigger. Any stable choice works; this one is reproducible in tests.
	for _, action := range sortedNightActions(gs) {
		if action.Kind == ActionMafiaKill && action.TargetID == victim && actionResolves(gs, action.ActorID) {
			return action.ActorID, victim
		}
	}
	return 0, 0
}

func resolveKills(gs *GameState, actions []NightAction, res *nightResolution) {
	if triggerman, victim := mafiaKillDecision(gs); victim != 0 {
		attack(gs, triggerman, victim, "mafia", res)
	}

	for _, action := range actions {
		switch action.Kind {
		case ActionSerialKill:
			if !actionResolves(gs, action.ActorID) {
				logCancelled(gs, action)
				continue
			}
			attack(gs, action.ActorID, action.TargetID, "serial_killer", res)

		case ActionVigilanteKill:
			if !actionResolves(gs, action.ActorID) {
				logCancelled(gs, action)
				continue
			}
			// The bullet is spent even if the shot is stopped, so a blocked
			// vigilante keeps their shot but a saved target does not.
			if actor, ok := gs.Players[action.ActorID]; ok {
				actor.UsedAbility = true
			}
			attack(gs, action.ActorID, action.TargetID, "vigilante", res)
		}
	}
}

func logCancelled(gs *GameState, action NightAction) {
	reason := "actor_died"
	if a, ok := gs.Players[action.ActorID]; ok && a.BlockedTonight {
		reason = "roleblocked"
	}
	gs.AppendLog("action_cancelled", map[string]interface{}{"actor": action.ActorID, "reason": reason})
}

// attack applies one kill attempt through the protection chain: a doctor's
// treatment stops it outright, otherwise a bodyguard trades their life for the
// target's and takes the attacker with them.
func attack(gs *GameState, attackerID, victimID PlayerID, cause string, res *nightResolution) {
	victim, ok := gs.Players[victimID]
	if !ok || !victim.Alive {
		return
	}

	if victim.ProtectedTonight {
		gs.AppendLog("kill_prevented", map[string]interface{}{"target": victimID, "by": "doctor", "cause": cause})
		if healer, ok := res.protectedBy[victimID]; ok {
			creditSave(gs, healer)
			res.effects = append(res.effects, SendDMEffect{healer,
				"💊 Your patient was attacked last night — and lived. Good work."})
		}
		res.effects = append(res.effects, SendDMEffect{victimID,
			"💊 Someone tried to kill you last night. A doctor saved your life."})
		return
	}

	if guards := res.guardedBy[victimID]; len(guards) > 0 {
		sort.Slice(guards, func(i, j int) bool { return guards[i] < guards[j] })
		guard := guards[0]
		res.guardedBy[victimID] = guards[1:]

		gs.AppendLog("bodyguard_intercept", map[string]interface{}{
			"guard": guard, "target": victimID, "attacker": attackerID,
		})
		creditSave(gs, guard)
		res.effects = append(res.effects,
			SendDMEffect{victimID, "🛡️ A bodyguard died in your place last night. You live."},
		)
		markDead(gs, guard, "bodyguard", res)
		if attackerID != 0 {
			markDead(gs, attackerID, "bodyguard_counter", res)
			creditKill(gs, guard)
		}
		return
	}

	markDead(gs, victimID, cause, res)
	if attackerID != 0 {
		creditKill(gs, attackerID)
	}
}

func creditSave(gs *GameState, id PlayerID) {
	if p, ok := gs.Players[id]; ok {
		p.Stats.Saves++
	}
}

func creditKill(gs *GameState, id PlayerID) {
	if p, ok := gs.Players[id]; ok {
		p.Stats.Kills++
	}
}

// markDead kills a player during the night and records the cause for the dawn
// report, following the chain of grief through any lover.
func markDead(gs *GameState, id PlayerID, cause string, res *nightResolution) {
	for _, dead := range killPlayer(gs, id, cause) {
		gs.LastNightDeaths = append(gs.LastNightDeaths, dead)
		if _, known := res.causes[dead]; !known {
			res.causes[dead] = cause
		}
		if dead != id {
			res.causes[dead] = "grief"
		}
	}
}

// killPlayer is the single place a player dies. It records the death, keeps the
// order for the graveyard and the first-blood award, and follows the link
// between lovers so a pair always leaves together.
func killPlayer(gs *GameState, id PlayerID, cause string) []PlayerID {
	p, ok := gs.Players[id]
	if !ok || !p.Alive {
		return nil
	}
	p.Alive = false
	p.DiedOnDay = gs.DayNumber
	p.DeathCause = cause
	gs.DeathOrder = append(gs.DeathOrder, id)
	gs.AppendLog("player_killed", map[string]interface{}{"player_id": id, "cause": cause})

	dead := []PlayerID{id}
	if p.LoverID != 0 {
		if lover, ok := gs.Players[p.LoverID]; ok && lover.Alive {
			dead = append(dead, killPlayer(gs, p.LoverID, "grief")...)
		}
	}
	return dead
}

func resolveInformation(gs *GameState, actions []NightAction, res *nightResolution) {
	for _, action := range actions {
		switch action.Kind {
		case ActionDetectiveCheck:
			if !actionResolves(gs, action.ActorID) {
				logCancelled(gs, action)
				continue
			}
			target, ok := gs.Players[action.TargetID]
			if !ok {
				continue
			}
			resultTeam := investigationResult(target)
			gs.LastCheckResult = &CheckResult{
				DetectiveID: action.ActorID,
				TargetID:    action.TargetID,
				ResultTeam:  resultTeam,
			}
			if resultTeam == TeamMafia && RoleTeam(target.Role) != TeamTown {
				if d, ok := gs.Players[action.ActorID]; ok {
					d.Stats.CorrectChecks++
				}
			}
			// A detective who died tonight cannot act on the result, and
			// DMing a dead player is just confusing.
			if detective, ok := gs.Players[action.ActorID]; ok && detective.Alive {
				res.effects = append(res.effects, SendDMEffect{
					PlayerID: action.ActorID,
					Text: fmt.Sprintf("🔍 Your investigation of %s comes back: *%s*.",
						target.Label(), TeamLabel(resultTeam)),
				})
			}

		case ActionLookoutWatch:
			if !actionResolves(gs, action.ActorID) {
				logCancelled(gs, action)
				continue
			}
			lookout, ok := gs.Players[action.ActorID]
			if !ok || !lookout.Alive {
				continue
			}
			target, ok := gs.Players[action.TargetID]
			if !ok {
				continue
			}
			res.effects = append(res.effects, SendDMEffect{
				PlayerID: action.ActorID,
				Text:     formatStakeout(gs, target),
			})
		}
	}
}

func formatStakeout(gs *GameState, target *Player) string {
	visitors := gs.NightVisits[target.ID]
	if len(visitors) == 0 {
		return fmt.Sprintf("🔭 You watched %s all night. Nobody came or went.", target.Label())
	}
	sorted := append([]PlayerID(nil), visitors...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var names []string
	for _, id := range sorted {
		if v, ok := gs.Players[id]; ok {
			names = append(names, v.Label())
		}
	}
	return fmt.Sprintf("🔭 You watched %s all night. Visitors: %s.", target.Label(), joinStrings(names))
}

// transitionToDay opens the discussion phase and posts the dawn report.
func transitionToDay(gs *GameState, dawn string, effects []SideEffect) (*GameState, []SideEffect) {
	gs.Phase = PhaseDiscussion
	gs.Votes = make(map[PlayerID]Vote)
	gs.Nominations = make(map[PlayerID]*Nomination)
	gs.Reactions = make(map[string]int)
	gs.ReactedBy = make(map[PlayerID]string)

	effects = append(effects, SendGroupEffect{gs.ChatID, dawn})
	effects = append(effects, SendGroupEffect{gs.ChatID, dayNarration(gs.DayNumber, len(gs.AlivePlayers()))})
	if gs.Config.NominationSystem {
		effects = append(effects, SendGroupEffect{gs.ChatID, "💬 Discuss, then use /nominate @player to put someone on trial."})
	} else {
		effects = append(effects, SendGroupEffect{gs.ChatID, discussionHelpText(gs)})
	}
	if gs.Config.DayReactions {
		effects = append(effects, ReactionBarEffect{
			ChatID: gs.ChatID,
			GameID: gs.ID,
			Text:   "🎭 *How's the town feeling?* Tap to react — the mood is shown in the day summary.",
		})
	}
	effects = append(effects, armPhase(gs, PhaseDiscussion)...)

	gs.AppendLog("phase_change", map[string]interface{}{"phase": "discussion", "day": gs.DayNumber})
	return gs, effects
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
	gs.NightVisits = make(map[PlayerID][]PlayerID)

	for _, p := range gs.Players {
		p.ProtectedTonight = false
		p.BlockedTonight = false
		p.FramedTonight = false
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

// nightCauseText turns an internal cause tag into dawn-report prose.
func nightCauseText(cause string) string {
	switch cause {
	case "grief":
		return "died of grief"
	case "bodyguard":
		return "fell defending someone else"
	case "bodyguard_counter":
		return "was cut down by a bodyguard"
	case "vigilante":
		return "was shot"
	case "serial_killer":
		return "was found brutally murdered"
	default:
		return "was found dead"
	}
}

func formatNightDeaths(gs *GameState, causes map[PlayerID]string) string {
	if len(gs.LastNightDeaths) == 0 {
		return "☀️ *Dawn breaks* — and to everyone's surprise, nobody died last night."
	}

	msg := "☀️ *Dawn breaks* over the town...\n"
	for _, pid := range gs.LastNightDeaths {
		p, ok := gs.Players[pid]
		if !ok {
			continue
		}
		line := fmt.Sprintf("\n💀 %s %s", p.Label(), nightCauseText(causes[pid]))
		if gs.Config.RevealOnNightKill {
			line += fmt.Sprintf(" — they were %s", RoleBadge(p.Role))
		}
		msg += line + "."
		gs.AddTimeline("💀", fmt.Sprintf("%s %s.", p.PlainName(), nightCauseText(causes[pid])))
	}
	return msg
}
