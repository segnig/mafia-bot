package engine

import "fmt"

// reduceReveal handles a Mayor going public. It is a one-way trade: everyone
// learns who they are, and in exchange their ballot carries extra weight.
func reduceReveal(gs *GameState, e RevealEvent) (*GameState, []SideEffect) {
	p, exists := gs.Players[e.PlayerID]
	if !exists {
		return gs, nil
	}
	if p.Role != RoleMayor {
		return gs, []SideEffect{
			SendDMEffect{e.PlayerID, "Only the Mayor can reveal themselves."},
		}
	}
	if !p.CanAct() {
		return gs, nil
	}
	if p.ExtraVotes > 0 {
		return gs, []SideEffect{
			SendDMEffect{e.PlayerID, "You have already revealed yourself."},
		}
	}
	// Revealing at night would leak the Mayor to the mafia before the town
	// could benefit from it.
	if gs.Phase != PhaseDiscussion && gs.Phase != PhaseNomination && gs.Phase != PhaseVoting {
		return gs, []SideEffect{
			SendDMEffect{e.PlayerID, "You can only reveal yourself during the day."},
		}
	}

	weight := gs.Config.MayorVoteWeight
	if weight < 1 {
		weight = 1
	}
	p.ExtraVotes = weight - 1
	p.RoleRevealed = true
	gs.AppendLog("mayor_revealed", map[string]interface{}{"player_id": e.PlayerID, "weight": weight})
	gs.AddTimeline("🏛️", fmt.Sprintf("%s revealed themselves as the Mayor.", p.PlainName()))

	effects := []SideEffect{
		SendGroupEffect{gs.ChatID, fmt.Sprintf(
			"🏛️ *%s steps forward as the MAYOR!*\n\nTheir vote now counts as *%d*. They can no longer be protected by anonymity — and the mafia now know exactly where to strike.",
			p.Label(), weight)},
	}
	// The live board shows vote weights, so it needs refreshing.
	if gs.Phase == PhaseVoting && gs.Config.LiveVoteBoard {
		effects = append(effects, voteBoardUpdate(gs))
	}
	return gs, effects
}

// reduceReact records a tap on the day's mood bar. Each player holds one
// reaction, which they may change, so the tally cannot be stuffed.
func reduceReact(gs *GameState, e ReactEvent) (*GameState, []SideEffect) {
	if gs.Phase != PhaseDiscussion && gs.Phase != PhaseNomination {
		return gs, nil
	}
	if !IsMoodEmoji(e.Emoji) {
		return gs, nil
	}
	if _, exists := gs.Players[e.PlayerID]; !exists {
		return gs, nil
	}
	if gs.Reactions == nil {
		gs.Reactions = make(map[string]int)
	}
	if gs.ReactedBy == nil {
		gs.ReactedBy = make(map[PlayerID]string)
	}

	if previous, had := gs.ReactedBy[e.PlayerID]; had {
		if previous == e.Emoji {
			return gs, nil
		}
		gs.Reactions[previous]--
		if gs.Reactions[previous] <= 0 {
			delete(gs.Reactions, previous)
		}
	}
	gs.ReactedBy[e.PlayerID] = e.Emoji
	gs.Reactions[e.Emoji]++
	return gs, nil
}

// reduceMafiaChat relays a message between mafia members. It never touches the
// group chat, so the team can coordinate without the town seeing anything.
func reduceMafiaChat(gs *GameState, e MafiaChatEvent) (*GameState, []SideEffect) {
	if !gs.Config.MafiaNightChat {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "Mafia chat is disabled in this game."},
		}
	}
	from, exists := gs.Players[e.FromID]
	if !exists || !from.CanAct() {
		return gs, nil
	}
	if RoleTeam(from.Role) != TeamMafia {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "Only the mafia can use this channel."},
		}
	}
	// Restricted to the night so the mafia cannot coordinate in real time
	// while the town is trying to read the room.
	if gs.Phase != PhaseNight && gs.Phase != PhaseRoleAssign {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "🌙 Mafia chat is only open at night."},
		}
	}

	mates := gs.TeammatesOf(e.FromID)
	if len(mates) == 0 {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "You are the only one left. There is nobody to talk to."},
		}
	}

	body := EscapeMD(TruncateRunes(e.Message, 400))
	gs.AppendLog("mafia_chat", map[string]interface{}{"from": e.FromID})

	effects := []SideEffect{SendDMEffect{e.FromID, "🔪 Sent to your family."}}
	for _, mate := range mates {
		effects = append(effects, SendDMEffect{mate.ID, fmt.Sprintf(
			"🔪 *%s:* %s", from.Label(), body)})
	}
	return gs, effects
}

// reduceGhostChat relays a message between eliminated players. The living never
// see it, so the dead can speculate freely without spoiling the game.
func reduceGhostChat(gs *GameState, e GhostChatEvent) (*GameState, []SideEffect) {
	if !gs.Config.GhostChat {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "Ghost chat is disabled in this game."},
		}
	}
	from, exists := gs.Players[e.FromID]
	if !exists {
		return gs, nil
	}
	if from.Alive {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "👻 Ghost chat is for the dead. You are still very much alive."},
		}
	}

	var ghosts []*Player
	for _, p := range playersByID(gs) {
		if !p.Alive && p.ID != e.FromID {
			ghosts = append(ghosts, p)
		}
	}
	if len(ghosts) == 0 {
		return gs, []SideEffect{
			SendDMEffect{e.FromID, "👻 You are the only ghost so far. Your words echo into nothing."},
		}
	}

	body := EscapeMD(TruncateRunes(e.Message, 400))
	gs.AppendLog("ghost_chat", map[string]interface{}{"from": e.FromID})

	effects := []SideEffect{SendDMEffect{e.FromID, fmt.Sprintf("👻 Sent to %d other ghost(s).", len(ghosts))}}
	for _, ghost := range ghosts {
		effects = append(effects, SendDMEffect{ghost.ID, fmt.Sprintf(
			"👻 *%s (dead):* %s", from.Label(), body)})
	}
	return gs, effects
}
