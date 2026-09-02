package engine

import (
	"crypto/rand"
	"fmt"
	"io"
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
	case ConfigPresetEvent:
		return reduceConfigPreset(gs, e)
	case ConfigSettingEvent:
		return reduceConfigSetting(gs, e)
	case EndGameEvent:
		return reduceEndGame(gs, e)
	case RolesDeliveredEvent:
		return reduceRolesDelivered(gs, e)
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
	case RevealEvent:
		return reduceReveal(gs, e)
	case ReactEvent:
		return reduceReact(gs, e)
	case MafiaChatEvent:
		return reduceMafiaChat(gs, e)
	case GhostChatEvent:
		return reduceGhostChat(gs, e)
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
		// The restart lost track of which role DMs had gone out, so re-send
		// them all under a new deal. Sealing an empty batch instead would
		// report a clean delivery for messages that may never have been sent,
		// and start Night 1 for players who never learned their role.
		gs.DealNumber++
		effects = append(effects, SendGroupEffect{gs.ChatID,
			"♻️ Resuming role assignment — check your DMs, your role is on its way again."})
		effects = append(effects, roleDMEffects(gs)...)
		effects = append(effects, RolesDeliveredEffect{GameID: gs.ID, Deal: gs.DealNumber})
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
		Preset:     gs.Config.PresetName,
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

func reduceConfigPreset(gs *GameState, e ConfigPresetEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLobby {
		return gs, nil
	}
	if !CanEditLobbyConfig(gs.HostID, e.PlayerID, e.IsAdmin) {
		return gs, nil
	}
	cfg := PresetConfig(e.Preset)
	if err := ValidateConfig(cfg); err != nil {
		return gs, nil
	}
	gs.Config = cfg
	gs.AppendLog("config_preset", map[string]interface{}{"preset": e.Preset, "by": e.PlayerID})
	return gs, lobbyConfigEffects(gs)
}

func reduceConfigSetting(gs *GameState, e ConfigSettingEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseLobby {
		return gs, nil
	}
	if !CanEditLobbyConfig(gs.HostID, e.PlayerID, e.IsAdmin) {
		return gs, nil
	}
	cfg := gs.Config
	if !CycleSetting(&cfg, e.Key) {
		return gs, nil
	}
	if err := ValidateConfig(cfg); err != nil {
		return gs, nil
	}
	gs.Config = cfg
	gs.AppendLog("config_setting", map[string]interface{}{"key": e.Key, "by": e.PlayerID})
	return gs, lobbyConfigEffects(gs)
}

func lobbyConfigEffects(gs *GameState) []SideEffect {
	return []SideEffect{
		lobbyStatusEffect(gs),
		LobbyConfigUpdatedEffect{ChatID: gs.ChatID, Config: gs.Config},
	}
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
	gs.StartedAt = time.Now()
	gs.DealNumber++
	gs.AppendLog("phase_change", map[string]interface{}{"phase": string(PhaseRoleAssign)})

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

	var effects []SideEffect
	effects = append(effects, SendGroupEffect{gs.ChatID, formatRosterAnnouncement(gs)})

	pairs := pairLovers(gs, rand.Reader)

	// Deterministic DM order keeps the effect stream reproducible in tests.
	effects = append(effects, roleDMEffects(gs)...)

	// Mafia members learn who their team is, and how to talk to them.
	if mafiaCount > 1 {
		var mafiaNames []string
		var mafiaIDs []PlayerID
		for _, p := range playersByID(gs) {
			if RoleTeam(p.Role) == TeamMafia {
				mafiaNames = append(mafiaNames, fmt.Sprintf("%s (%s)", p.Label(), RoleBadge(p.Role)))
				mafiaIDs = append(mafiaIDs, p.ID)
			}
		}
		team := fmt.Sprintf("🔪 *Your mafia family:*\n%s\n\nYou must agree on one victim each night — a split vote kills nobody.",
			bulletList(mafiaNames))
		if gs.Config.MafiaNightChat {
			team += "\n\nTalk privately with `/mafia <message>` in this chat."
		}
		for _, pid := range mafiaIDs {
			effects = append(effects, SendDMEffect{PlayerID: pid, Text: team})
		}
	}

	// Lovers learn about each other but nothing else.
	for _, pair := range pairs {
		a, b := gs.Players[pair[0]], gs.Players[pair[1]]
		effects = append(effects,
			SendDMEffect{a.ID, fmt.Sprintf("💞 *You are in love with %s.*\nIf either of you dies, the other dies of grief. Neither of you knows the other's role.", b.Label())},
			SendDMEffect{b.ID, fmt.Sprintf("💞 *You are in love with %s.*\nIf either of you dies, the other dies of grief. Neither of you knows the other's role.", a.Label())},
		)
	}

	gs.AppendLog("roles_generated", map[string]interface{}{
		"mafia_count":    mafiaCount,
		"optional_roles": optionalRolesChosen,
		"n_players":      len(gs.Players),
	})
	gs.AddTimeline("🎬", fmt.Sprintf("The game began with %d players.", len(gs.Players)))

	// The transport turns this into a RolesDeliveredEvent once the DMs above
	// have resolved; that is what starts Night 1. The timer is a backstop in
	// case the acknowledgement never comes back.
	effects = append(effects, RolesDeliveredEffect{GameID: gs.ID, Deal: gs.DealNumber})
	effects = append(effects, armPhase(gs, PhaseRoleAssign)...)
	effects = append(effects, LobbyConfigUpdatedEffect{ChatID: gs.ChatID, Config: gs.Config})

	return gs, effects
}

// roleDMEffects emits one role DM per player, tagged with the current deal.
// Every path that deals or re-sends roles goes through here, so the deal tag
// can never be forgotten on one of them.
func roleDMEffects(gs *GameState) []SideEffect {
	var effects []SideEffect
	for _, p := range playersByID(gs) {
		effects = append(effects, SendRoleDMEffect{
			GameID:   gs.ID,
			PlayerID: p.ID,
			Text:     formatRoleDM(gs, p),
			Deal:     gs.DealNumber,
		})
	}
	return effects
}

// formatRoleDM is the message a player receives when roles are dealt. It reads
// entirely from the role catalogue, so a new role needs no change here.
func formatRoleDM(gs *GameState, p *Player) string {
	info := RoleInfoFor(p.Role)

	msg := fmt.Sprintf("%s *You are the %s*\n_%s team_\n\n%s",
		info.Emoji, EscapeMD(info.Title), TeamLabel(info.Team), info.Blurb)

	switch {
	case p.Role == RoleMayor:
		msg += fmt.Sprintf("\n\n🗳️ Once revealed, your vote counts as *%d*.", gs.Config.MayorVoteWeight)
	case info.HasNightAction():
		msg += "\n\n🌙 You will get a private keyboard each night. Tap a name to act."
	default:
		msg += "\n\n☀️ You have no night action — your influence is in the daytime."
	}

	msg += "\n\n⚠️ _Do not share or screenshot this message. Keep the mystery alive._"
	return msg
}

// formatRosterAnnouncement tells the group what kind of game they are about to
// play without leaking who holds which role.
func formatRosterAnnouncement(gs *GameState) string {
	counts := map[Team]int{}
	for _, p := range gs.Players {
		counts[RoleTeam(p.Role)]++
	}

	msg := "🎬 *The game begins!*\n━━━━━━━━━━━━━━━━━━━━\n"
	msg += fmt.Sprintf("👥 Players: *%d*\n", len(gs.Players))
	msg += fmt.Sprintf("🔪 Mafia: *%d*\n", counts[TeamMafia])
	if counts[TeamKiller] > 0 {
		msg += fmt.Sprintf("🩸 Serial killers: *%d*\n", counts[TeamKiller])
	}
	if counts[TeamNeutral] > 0 {
		msg += fmt.Sprintf("🎭 Neutrals: *%d*\n", counts[TeamNeutral])
	}
	if gs.Config.EnableLovers {
		msg += "💞 Two players are secretly in love\n"
	}
	msg += "━━━━━━━━━━━━━━━━━━━━\n📬 *Check your DMs for your role.*"
	return msg
}

// pairLovers links two players at the deal. Returns the pairs it created so
// the caller can DM them.
func pairLovers(gs *GameState, rng io.Reader) [][2]PlayerID {
	if !gs.Config.EnableLovers || len(gs.Players) < 4 {
		return nil
	}
	ids := make([]PlayerID, 0, len(gs.Players))
	for id := range gs.Players {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	shuffled := FisherYatesShuffle(ids, rng)
	a, b := shuffled[0], shuffled[1]
	gs.Players[a].LoverID = b
	gs.Players[b].LoverID = a
	gs.AppendLog("lovers_paired", map[string]interface{}{"a": a, "b": b})
	return [][2]PlayerID{{a, b}}
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

func bulletList(ss []string) string {
	result := ""
	for _, s := range ss {
		result += "  • " + s + "\n"
	}
	return result
}

func reduceRoleDeliveryFailed(gs *GameState, e RoleDeliveryFailedEvent) (*GameState, []SideEffect) {
	// A failure from a superseded deal has already been answered: the redeal
	// that superseded it sent this player a fresh role, and its own outcome is
	// what decides their fate. Acting on the stale one would remove a player
	// who is perfectly reachable. Deal 0 means untagged, which only happens in
	// tests that drive the reducer directly.
	if e.Deal != 0 && e.Deal != gs.DealNumber {
		return gs, nil
	}
	p, exists := gs.Players[e.PlayerID]
	if !exists {
		return gs, nil
	}

	// The failure belongs to the current deal but arrived after the phase moved
	// on — the backstop timer fired, or another failure in the same batch sent
	// the game back to the lobby. Redealing is no longer possible, so fall
	// through to the ordinary unreachable-player path instead of dropping the
	// failure on the floor and leaving them holding a role they never saw.
	if gs.Phase != PhaseRoleAssign {
		return reduceDisconnect(gs, PlayerDisconnectedEvent{PlayerID: e.PlayerID})
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
			rp.LoverID = 0
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
			rp.LoverID = 0
		}
		effects := []SideEffect{
			SendGroupEffect{gs.ChatID, "Failed to reassign roles. Returning to lobby."},
			lobbyStatusEffect(gs),
		}
		return gs, append(effects, armPhase(gs, PhaseLobby)...)
	}

	var effects []SideEffect
	effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("%s could not receive their role and has been removed. Roles have been reassigned to remaining players.", label)})

	// The old pairing may have included the player who just left.
	for _, rp := range gs.Players {
		rp.LoverID = 0
	}
	for pid, role := range assignment {
		gs.Players[pid].Role = role
	}
	pairLovers(gs, rand.Reader)

	// A new deal, so outcomes still arriving from the previous one no longer
	// apply to anything.
	gs.DealNumber++
	effects = append(effects, roleDMEffects(gs)...)

	effects = append(effects, RolesDeliveredEffect{GameID: gs.ID, Deal: gs.DealNumber})
	effects = append(effects, armPhase(gs, PhaseRoleAssign)...)
	return gs, effects
}

func reduceRolesDelivered(gs *GameState, e RolesDeliveredEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseRoleAssign {
		return gs, nil
	}
	// Only the deal that is currently outstanding may start the night. A redeal
	// leaves the previous batch's DMs resolving in the background, and their
	// completion says nothing about whether the roles now in play arrived.
	if e.Deal != 0 && e.Deal != gs.DealNumber {
		return gs, nil
	}
	return transitionToNight(gs)
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

// endGame is the single exit point for a finished game so the phase, winner,
// reveal state, and effect ordering stay consistent.
func endGame(gs *GameState, winner *WinResult, effects []SideEffect) (*GameState, []SideEffect) {
	gs.Phase = PhaseGameOver
	gs.Winner = winner
	gs.PhaseDeadline = time.Time{}
	for _, p := range gs.Players {
		p.RoleRevealed = true
	}
	// Survivors take their win here, once the final board is known.
	for _, p := range playersByID(gs) {
		if p.Role == RoleSurvivor && p.Alive {
			gs.JesterWon = appendUnique(gs.JesterWon, p.ID)
		}
	}
	gs.AppendLog("game_over", map[string]interface{}{"winner": string(winner.Winner)})
	gs.AddTimeline("🏁", winner.Description)

	summary := BuildGameSummary(gs)
	// Carries a rematch button, so a group can go straight into another game.
	effects = append(effects, SendGroupWithRematchEffect{gs.ChatID, FormatGameOver(gs, summary)})
	effects = append(effects, GameOverEffect{GameID: gs.ID, Result: *winner, Summary: summary})
	return gs, effects
}

func appendUnique(list []PlayerID, id PlayerID) []PlayerID {
	for _, existing := range list {
		if existing == id {
			return list
		}
	}
	return append(list, id)
}

func votingTargets(gs *GameState) []PlayerID {
	targets := gs.AlivePlayerIDs()
	if gs.ActiveTrial != nil {
		targets = []PlayerID{*gs.ActiveTrial}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
	return targets
}

func votingKeyboardEffect(gs *GameState) SideEffect {
	return SendVotingKeyboardEffect{
		ChatID:       gs.ChatID,
		GameID:       gs.ID,
		Targets:      votingTargets(gs),
		AllowNoLynch: gs.Config.AllowNoLynch,
		Prompt:       FormatVoteBoard(gs),
	}
}

// voteBoardUpdate refreshes the single live vote message in place, which is
// much calmer than announcing every individual ballot.
func voteBoardUpdate(gs *GameState) SideEffect {
	return UpdateVoteBoardEffect{
		ChatID:       gs.ChatID,
		GameID:       gs.ID,
		Targets:      votingTargets(gs),
		AllowNoLynch: gs.Config.AllowNoLynch,
		Text:         FormatVoteBoard(gs),
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
	if !isChange {
		voter.Stats.VotesCast++
	}

	var effects []SideEffect
	if gs.Config.LiveVoteBoard {
		// One message, edited in place, instead of a line per ballot.
		effects = append(effects, voteBoardUpdate(gs))
	} else {
		verb := "voted"
		if isChange {
			verb = "changed their vote"
		}
		choice := "*No Lynch*"
		if e.Vote.TargetID != NoLynchTarget {
			choice = gs.Players[e.Vote.TargetID].Label()
		}
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf("%s %s for %s. (%d/%d votes in)",
			voter.Label(), verb, choice, len(gs.Votes), gs.EligibleVoterCount())})
	}

	// Only players who can actually vote are counted, so one dropout does not
	// force every round to run the full clock.
	if len(gs.Votes) >= gs.EligibleVoterCount() {
		resolvedState, resolvedEffects := resolveLynch(gs)
		return resolvedState, append(effects, resolvedEffects...)
	}

	return gs, effects
}

// voteTally sums the ballots by target, weighted so a revealed Mayor counts
// for more than one.
func voteTally(gs *GameState) map[PlayerID]int {
	tally := make(map[PlayerID]int)
	for voterID, v := range gs.Votes {
		weight := 1
		if voter, ok := gs.Players[voterID]; ok {
			weight = voter.VoteWeight()
		}
		tally[v.TargetID] += weight
	}
	return tally
}

// lynchThreshold is the vote weight needed to execute someone.
func lynchThreshold(gs *GameState) int {
	if !gs.Config.LynchRequiresMajority {
		return 1
	}
	// Measured against total weight rather than headcount, so revealing as
	// Mayor raises the bar for everyone instead of only helping the Mayor.
	total := gs.TotalVoteWeight()
	if total < 1 {
		return 1
	}
	return total/2 + 1
}

func resolveLynch(gs *GameState) (*GameState, []SideEffect) {
	gs.Phase = PhaseLynchResolve

	tally := voteTally(gs)
	lynchTarget, maxVotes, tied := plurality(tally)
	threshold := lynchThreshold(gs)

	// The final board is worth showing even when nobody dies, so players can
	// see how close it was.
	effects := []SideEffect{SendGroupEffect{gs.ChatID, FormatFinalVoteBoard(gs, tally)}}

	switch {
	case tied:
		effects = append(effects, SendGroupEffect{gs.ChatID, "⚖️ The vote is tied. No one is lynched today."})
		gs.AppendLog("no_lynch", map[string]interface{}{"reason": "tie"})
		gs.AddTimeline("⚖️", "The vote tied — nobody was lynched.")

	case lynchTarget == NoLynchTarget:
		effects = append(effects, SendGroupEffect{gs.ChatID, "⚖️ The town votes to spare everyone. No one is lynched today."})
		gs.AppendLog("no_lynch", map[string]interface{}{"reason": "no_lynch_wins"})
		gs.AddTimeline("🕊️", "The town chose to spare everyone.")

	case maxVotes < threshold:
		// Without this, a single vote in a quiet round decides an execution.
		target := gs.Players[lynchTarget]
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"⚖️ %s led the vote with %d of the %d needed for a majority. No one is lynched today.",
			target.Label(), maxVotes, threshold)})
		gs.AppendLog("no_lynch", map[string]interface{}{"reason": "no_majority", "votes": maxVotes, "needed": threshold})
		gs.AddTimeline("⚖️", fmt.Sprintf("%s led the vote but escaped a majority.", target.PlainName()))

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
	gs.ActiveTrial = nil
	gs.AppendLog("player_lynched", map[string]interface{}{"player_id": lynchTarget, "votes": maxVotes})

	// Credit everyone who voted for a genuine threat before the reveal, so
	// the end-of-game awards can name the sharpest reader of the room.
	targetWasEvil := RoleTeam(target.Role) == TeamMafia || RoleTeam(target.Role) == TeamKiller
	if targetWasEvil {
		for voterID, v := range gs.Votes {
			if v.TargetID != lynchTarget {
				continue
			}
			if voter, ok := gs.Players[voterID]; ok {
				voter.Stats.VotesOnEvil++
			}
		}
	}

	deaths := killPlayer(gs, lynchTarget, "lynch")

	roleReveal := ""
	if gs.Config.RevealOnLynch {
		target.RoleRevealed = true
		roleReveal = fmt.Sprintf(" They were %s.", RoleBadge(target.Role))
	}
	effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
		"💀 %s has been executed by the town.%s", target.Label(), roleReveal)})
	gs.AddTimeline("💀", fmt.Sprintf("%s was lynched with %d votes.", target.PlainName(), maxVotes))

	// A lover follows the condemned to the grave in front of everyone.
	for _, dead := range deaths {
		if dead == lynchTarget {
			continue
		}
		if p, ok := gs.Players[dead]; ok {
			if gs.Config.RevealOnLynch {
				p.RoleRevealed = true
			}
			effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
				"💔 %s could not bear the loss and died of grief.", p.Label())})
			gs.AddTimeline("💔", fmt.Sprintf("%s died of grief.", p.PlainName()))
		}
	}

	// Jester win check (§2.2)
	if target.Role == RoleJester {
		gs.JesterWon = appendUnique(gs.JesterWon, lynchTarget)
		effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"🃏 %s was the *Jester* and wins! The game continues for everyone else.", target.Label())})
		gs.AddTimeline("🃏", fmt.Sprintf("%s won as the Jester.", target.PlainName()))
	}

	if winner := checkWinCondition(gs); winner != nil {
		return endGame(gs, winner, effects)
	}

	nightState, nightEffects := transitionToNight(gs)
	return nightState, append(effects, nightEffects...)
}

// checkWinCondition decides whether the game is over. With three hostile
// possibilities on the board — mafia, a lone killer, and neutrals who win on
// their own terms — a faction only wins once every rival killer is gone or it
// holds enough of the board that nothing can stop it.
func checkWinCondition(gs *GameState) *WinResult {
	// Before roles are dealt every role is the empty string, which maps to
	// town — so an unstarted game would immediately report "all mafia dead".
	if !gs.Started() {
		return nil
	}

	mafia := gs.AliveMafiaCount()
	town := gs.AliveTownCount()
	neutral := gs.AliveNeutralCount()
	killers := gs.AliveKillerCount()
	total := mafia + town + neutral + killers

	if total == 0 {
		return &WinResult{Winner: "", Description: "Everyone is dead. Nobody wins. ⚰️"}
	}

	// §8.8: all remaining players disconnected
	anyActive := false
	for _, p := range gs.Players {
		if p.CanAct() {
			anyActive = true
			break
		}
	}
	if !anyActive {
		return &WinResult{Winner: "", Description: "All players disconnected. Game void — no winner."}
	}

	if mafia == 0 && killers == 0 {
		return &WinResult{Winner: TeamTown, Description: "Every threat has been eliminated. *Town wins!* 🎉"}
	}

	// A faction wins at parity, but only once no rival killer is left to
	// take it from them.
	if killers == 0 && mafia > 0 && mafia >= total-mafia {
		return &WinResult{Winner: TeamMafia, Description: "The mafia now runs this town. *Mafia wins!* 🔪"}
	}
	if mafia == 0 && killers > 0 && killers >= total-killers {
		return &WinResult{Winner: TeamKiller, Description: "The killer stalks the last of the town. *Serial Killer wins!* 🩸"}
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
	// A finished game has already declared a winner and written its records.
	// Re-running the win check here could declare a second one.
	if gs.Phase.IsTerminal() {
		return gs, nil
	}
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

	// During a vote the countdown belongs on the board that is already there,
	// so the warning refreshes it in place instead of adding a message.
	if e.Phase == PhaseVoting && gs.Config.LiveVoteBoard {
		return gs, []SideEffect{voteBoardUpdate(gs)}
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

	msg := fmt.Sprintf("⏰ *%d seconds* remaining for %s!", e.SecondsLeft, location)
	// Naming who the night is still waiting on is the single most useful
	// nudge, and it does not leak anything: the roster is public.
	if e.Phase == PhaseNight {
		if pending := pendingActorNames(gs); pending != "" {
			msg += "\n\n_Still waiting on: " + pending + "_"
		}
	}
	return gs, []SideEffect{SendGroupEffect{gs.ChatID, msg}}
}

// pendingActorNames lists players who still owe a night action.
func pendingActorNames(gs *GameState) string {
	var names []string
	for _, p := range playersByJoinTime(gs) {
		if !roleNeedsAction(gs, p) {
			continue
		}
		if _, submitted := gs.NightActions[p.ID]; !submitted {
			names = append(names, p.Label())
		}
	}
	return joinStrings(names)
}

func reduceNominate(gs *GameState, e NominateEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseDiscussion && gs.Phase != PhaseNomination {
		return gs, nil
	}
	if !gs.Config.NominationSystem {
		return gs, nil
	}

	// CanAct, not Alive: a player the bot cannot reach is excluded from the
	// vote a nomination leads to, so letting them open one would put a trial
	// in motion that they are not allowed to take part in.
	nominator, exists := gs.Players[e.NominatorID]
	if !exists || !nominator.CanAct() {
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

	// Seconding forces the trial immediately, so it needs the same eligibility
	// as voting in it.
	seconder, exists := gs.Players[e.PlayerID]
	if !exists || !seconder.CanAct() {
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
	if !exists || !accuser.CanAct() {
		return gs, nil
	}
	// The target only has to be alive: an unreachable player can still be put
	// on trial and lynched, they just cannot drive the day themselves.
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
	accuser.Stats.Accusations++
	gs.AppendLog("accusation", map[string]interface{}{"accuser": e.AccuserID, "target": e.TargetID})

	count := len(gs.Accusations[e.TargetID])
	// Measured against the players who can actually accuse, which is the same
	// population every other majority in the game is measured against.
	eligible := gs.EligibleVoterCount()

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("👉 %s accuses %s! (%d/%d accusations)",
			accuser.Label(), target.Label(), count, eligible)},
	}

	if count > eligible/2 {
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
	if !exists || !player.CanAct() {
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
	if !exists || !from.CanAct() {
		return gs, nil
	}
	// A whisper is a DM, so an unreachable recipient would never see it.
	to, exists := gs.Players[e.ToID]
	if !exists || !to.CanAct() {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "That player can't receive whispers right now."},
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
	from.Stats.Whispers++
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
	p, exists := gs.Players[e.PlayerID]
	if !exists {
		return gs, nil
	}
	if gs.SpeakCount == nil {
		gs.SpeakCount = make(map[PlayerID]int)
	}
	// SpeakCount resets each day for the silent-player check; Stats.Messages
	// accumulates over the whole game for the awards.
	gs.SpeakCount[e.PlayerID]++
	p.Stats.Messages++
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

	target.Disconnected = true
	delete(gs.Votes, e.TargetID)
	delete(gs.NightActions, e.TargetID)
	deaths := killPlayer(gs, e.TargetID, "kicked")
	gs.AppendLog("player_kicked", map[string]interface{}{"player_id": e.TargetID, "by": e.HostID})

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf("🚪 %s has been kicked from the game by the host.", target.Label())},
	}
	for _, dead := range deaths {
		if dead == e.TargetID {
			continue
		}
		if p, ok := gs.Players[dead]; ok {
			effects = append(effects, SendGroupEffect{gs.ChatID, fmt.Sprintf(
				"💔 %s could not bear the loss and died of grief.", p.Label())})
		}
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

	if mood := formatMood(gs); mood != "" {
		msg += "\n" + mood
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

func discussionHelpText(gs *GameState) string {
	msg := `💬 *Your options right now:*
• /accuse @player — publicly accuse someone
• /defend [statement] — make your case (once per day)
• /whisper @player [message] — a private note (the group sees it happened)
• /graveyard — who has died, and what they were
• /status — the current state of play`

	if gs.Config.GhostChat {
		msg += "\n\n_Eliminated? DM me `/ghost <message>` to talk with the other ghosts._"
	}
	msg += "\n\n_Discuss, argue, bluff, deceive. The vote is coming..._"
	return msg
}
