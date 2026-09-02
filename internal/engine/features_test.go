package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// nightGame builds a game parked at night with the given roles, keyed by
// player ID starting at 1. It is the shortest way to set up a night
// interaction without going through the whole lobby.
func nightGame(t *testing.T, cfg GameConfig, roles ...Role) *GameState {
	t.Helper()
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseNight
	gs.DayNumber = 2
	gs.StartedAt = time.Now().Add(-5 * time.Minute)
	base := time.Now()
	for i, role := range roles {
		id := PlayerID(i + 1)
		gs.Players[id] = &Player{
			ID:       id,
			Username: fmt.Sprintf("p%d", id),
			Role:     role,
			Alive:    true,
			JoinedAt: base.Add(time.Duration(i) * time.Millisecond),
		}
	}
	return gs
}

func act(gs *GameState, actor PlayerID, kind string, target PlayerID) {
	gs.NightActions[actor] = NightAction{ActorID: actor, Kind: kind, TargetID: target}
}

func dmsTo(effects []SideEffect, id PlayerID) []string {
	var out []string
	for _, eff := range effects {
		if dm, ok := eff.(SendDMEffect); ok && dm.PlayerID == id {
			out = append(out, dm.Text)
		}
	}
	return out
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Role catalogue
// ---------------------------------------------------------------------------

// Every role the dealer can hand out must be described in the catalogue,
// otherwise a player would receive a role DM for "❔ unassigned".
func TestRoleCatalogueCoversEveryDealtRole(t *testing.T) {
	dealt := []Role{RoleVillager, RoleMafia}
	for _, def := range DefaultOptionalRoles() {
		dealt = append(dealt, def.Role)
	}

	for _, role := range dealt {
		info, ok := roleCatalog[role]
		if !ok {
			t.Errorf("role %q can be dealt but has no catalogue entry", role)
			continue
		}
		if info.Title == "" || info.Emoji == "" || info.Blurb == "" {
			t.Errorf("role %q has an incomplete catalogue entry: %+v", role, info)
		}
		if info.Team != RoleTeam(role) {
			t.Errorf("role %q: catalogue says team %q, RoleTeam says %q", role, info.Team, RoleTeam(role))
		}
		if info.HasNightAction() && info.ActionPrompt == "" {
			t.Errorf("role %q has a night action but no prompt", role)
		}
		if info.HasNightAction() && info.Targets == TargetNone {
			t.Errorf("role %q has a night action but targets nobody", role)
		}
		if !info.HasNightAction() && info.Targets != TargetNone {
			t.Errorf("role %q has no night action but a target rule", role)
		}
	}
}

// A role definition that joins the mafia must replace a mafia slot, or the
// dealer adds an extra enemy on top of the computed count.
func TestOptionalMafiaRolesReplaceASlot(t *testing.T) {
	for _, def := range DefaultOptionalRoles() {
		if RoleTeam(def.Role) == TeamMafia && !def.ReplacesMafiaSlot {
			t.Errorf("%s joins the mafia but does not replace a mafia slot", def.Role)
		}
		if RoleTeam(def.Role) != TeamMafia && def.ReplacesMafiaSlot {
			t.Errorf("%s replaces a mafia slot but is not mafia", def.Role)
		}
	}
}

func TestMafiaCannotTargetTeammates(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleGodfather, RoleFramer, RoleVillager, RoleDoctor)

	targets := roleTargets(gs, gs.Players[1])
	for _, id := range targets {
		if RoleTeam(gs.Players[id].Role) == TeamMafia {
			t.Errorf("mafia was offered teammate %d (%s) as a target", id, gs.Players[id].Role)
		}
	}
	if len(targets) != 2 {
		t.Errorf("expected the two non-mafia players as targets, got %v", targets)
	}
}

// The serial killer is hostile to the mafia too, so they must be allowed to
// target them.
func TestSerialKillerMayTargetAnyone(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleSerialKiller, RoleMafia, RoleVillager)

	targets := roleTargets(gs, gs.Players[1])
	if len(targets) != 2 {
		t.Fatalf("expected both other players as targets, got %v", targets)
	}
}

// ---------------------------------------------------------------------------
// Night interactions
// ---------------------------------------------------------------------------

func TestEscortBlocksTheDoctorSoTheKillLands(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleEscort, RoleDoctor, RoleMafia, RoleVillager, RoleVillager)
	act(gs, 1, ActionEscortBlock, 2)   // escort occupies the doctor
	act(gs, 2, ActionDoctorProtect, 4) // doctor tries to save the villager
	act(gs, 3, ActionMafiaKill, 4)     // mafia goes for the same villager

	gs, effects := resolveNight(gs)

	if gs.Players[4].Alive {
		t.Error("a blocked doctor should not save anyone")
	}
	if !containsSubstring(dmsTo(effects, 2), "kept you busy") {
		t.Error("the blocked player should be told their night went nowhere")
	}
}

// A roleblock cannot itself be blocked, otherwise two escorts pointing at each
// other would need an arbitrary tiebreak.
func TestRoleblocksCannotBeBlocked(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleEscort, RoleEscort, RoleMafia, RoleVillager, RoleVillager)
	act(gs, 1, ActionEscortBlock, 2)
	act(gs, 2, ActionEscortBlock, 1)

	gs, _ = resolveNight(gs)

	if !gs.Players[1].BlockedTonight || !gs.Players[2].BlockedTonight {
		t.Error("two escorts targeting each other should both be blocked")
	}
}

func TestFramerMakesAnInnocentReadAsMafia(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleFramer, RoleDetective, RoleVillager, RoleMafia, RoleVillager)
	act(gs, 1, ActionFramerFrame, 3)
	act(gs, 2, ActionDetectiveCheck, 3)

	gs, effects := resolveNight(gs)

	if gs.LastCheckResult == nil {
		t.Fatal("the detective produced no result")
	}
	if gs.LastCheckResult.ResultTeam != TeamMafia {
		t.Errorf("a framed villager should read as mafia, got %q", gs.LastCheckResult.ResultTeam)
	}
	if !containsSubstring(dmsTo(effects, 2), TeamLabel(TeamMafia)) {
		t.Error("the detective should be told the framed result")
	}

	// A frame lasts one night only: checking the same player again without a
	// fresh frame must come back clean.
	gs, _ = transitionToNight(gs)
	act(gs, 2, ActionDetectiveCheck, 3)
	act(gs, 1, ActionFramerFrame, 5)
	act(gs, 4, ActionMafiaKill, 5)
	gs, _ = resolveNight(gs)

	if gs.LastCheckResult.ResultTeam != TeamTown {
		t.Errorf("framing should not carry into the next night, got %q", gs.LastCheckResult.ResultTeam)
	}
}

// Framing a real villager must not be credited as a correct check, or the
// mafia could farm the detective's award.
func TestFramedVillagerIsNotACorrectCheck(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleFramer, RoleDetective, RoleVillager, RoleMafia, RoleVillager)
	act(gs, 1, ActionFramerFrame, 3)
	act(gs, 2, ActionDetectiveCheck, 3)

	gs, _ = resolveNight(gs)

	if gs.Players[2].Stats.CorrectChecks != 0 {
		t.Error("investigating a framed villager should not count as a correct check")
	}
}

func TestGodfatherReadsAsTown(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleGodfather, RoleDetective, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionMafiaKill, 3)
	act(gs, 2, ActionDetectiveCheck, 1)

	gs, _ = resolveNight(gs)

	if gs.LastCheckResult.ResultTeam != TeamTown {
		t.Errorf("the godfather should read as town, got %q", gs.LastCheckResult.ResultTeam)
	}
	if gs.Players[2].Stats.CorrectChecks != 0 {
		t.Error("a town read on the godfather is not a correct check")
	}
}

func TestBodyguardDiesInPlaceAndTakesTheAttacker(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleBodyguard, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionBodyguardGuard, 3)
	act(gs, 2, ActionMafiaKill, 3)

	gs, effects := resolveNight(gs)

	if !gs.Players[3].Alive {
		t.Error("the guarded player should survive")
	}
	if gs.Players[1].Alive {
		t.Error("the bodyguard should die in their place")
	}
	if gs.Players[2].Alive {
		t.Error("the attacker should be cut down by the bodyguard")
	}
	if gs.Players[1].DeathCause != CauseBodyguard {
		t.Errorf("bodyguard death cause = %q, want %q", gs.Players[1].DeathCause, CauseBodyguard)
	}
	if gs.Players[1].Stats.Saves != 1 {
		t.Error("the bodyguard should be credited with the save")
	}
	if gs.Players[1].Stats.Kills != 1 {
		t.Error("the bodyguard should be credited with the counter-kill")
	}
	if !containsSubstring(dmsTo(effects, 3), "died in your place") {
		t.Error("the protected player should learn they were guarded")
	}
}

// One bodyguard can only absorb one attack; a second killer that same night
// still gets through.
func TestOneBodyguardStopsOnlyOneAttack(t *testing.T) {
	cfg := DefaultConfig()
	gs := nightGame(t, cfg, RoleBodyguard, RoleMafia, RoleSerialKiller, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionBodyguardGuard, 4)
	act(gs, 2, ActionMafiaKill, 4)
	act(gs, 3, ActionSerialKill, 4)

	gs, _ = resolveNight(gs)

	if gs.Players[4].Alive {
		t.Error("the second attacker of the night should get through")
	}
}

func TestDoctorSaveCreditsTheHealer(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleDoctor, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionDoctorProtect, 3)
	act(gs, 2, ActionMafiaKill, 3)

	gs, effects := resolveNight(gs)

	if !gs.Players[3].Alive {
		t.Fatal("the treated player should survive")
	}
	if gs.Players[1].Stats.Saves != 1 {
		t.Error("the doctor should be credited with the save")
	}
	if gs.Players[2].Stats.Kills != 0 {
		t.Error("a prevented kill should not be credited to the attacker")
	}
	if !containsSubstring(dmsTo(effects, 1), "and lived") {
		t.Error("the doctor should be told their patient was attacked")
	}
}

// The lookout sees the member who pulled the trigger, not the whole family:
// one stakeout should not expose the entire mafia.
func TestLookoutSeesOnlyTheTriggerman(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleLookout, RoleMafia, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionLookoutWatch, 4)
	act(gs, 2, ActionMafiaKill, 4)
	act(gs, 3, ActionMafiaKill, 4)

	_, effects := resolveNight(gs)

	report := dmsTo(effects, 1)
	if !containsSubstring(report, "p2") {
		t.Errorf("the lookout should see the triggerman, got %v", report)
	}
	if containsSubstring(report, "p3") {
		t.Errorf("the lookout should not see the whole mafia, got %v", report)
	}
}

// A lookout watching a quiet house learns that too, which is real information.
func TestLookoutReportsAnEmptyNight(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleLookout, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionLookoutWatch, 3)
	act(gs, 2, ActionMafiaKill, 4)

	_, effects := resolveNight(gs)

	if !containsSubstring(dmsTo(effects, 1), "Nobody came or went") {
		t.Error("the lookout should be told when nobody visited")
	}
}

// A split mafia vote kills nobody, which is what makes coordination matter.
func TestSplitMafiaVoteKillsNobody(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleMafia, RoleVillager, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionMafiaKill, 3)
	act(gs, 2, ActionMafiaKill, 4)

	gs, _ = resolveNight(gs)

	if len(gs.LastNightDeaths) != 0 {
		t.Errorf("a tied mafia vote should kill nobody, got %v", gs.LastNightDeaths)
	}
}

// ---------------------------------------------------------------------------
// Lovers
// ---------------------------------------------------------------------------

func TestLoversDieTogetherAtNight(t *testing.T) {
	cfg := DefaultConfig()
	gs := nightGame(t, cfg, RoleMafia, RoleVillager, RoleVillager, RoleVillager, RoleVillager)
	gs.Players[2].LoverID = 3
	gs.Players[3].LoverID = 2
	act(gs, 1, ActionMafiaKill, 2)

	gs, _ = resolveNight(gs)

	if gs.Players[3].Alive {
		t.Error("a lover should follow their partner into the grave")
	}
	if gs.Players[3].DeathCause != CauseGrief {
		t.Errorf("the surviving lover's cause = %q, want %q", gs.Players[3].DeathCause, CauseGrief)
	}
	if len(gs.LastNightDeaths) != 2 {
		t.Errorf("both lovers should appear in the dawn report, got %v", gs.LastNightDeaths)
	}
}

// Grief must not recurse forever if the pairing is ever malformed.
func TestKillingBothLoversTerminates(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleVillager)
	gs.Players[1].LoverID = 2
	gs.Players[2].LoverID = 1

	dead := killPlayer(gs, 1, CauseLynch)

	if len(dead) != 2 {
		t.Fatalf("expected both lovers dead, got %v", dead)
	}
	if gs.Players[1].Alive || gs.Players[2].Alive {
		t.Error("both lovers should be dead")
	}
}

// Lovers are only paired when the group asked for them.
func TestLoversStayOffWhenDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableLovers = false
	gs := nightGame(t, cfg, RoleVillager, RoleVillager, RoleMafia, RoleVillager, RoleVillager)

	if pairs := pairLovers(gs, &deterministicReader{}); pairs != nil {
		t.Errorf("no pairs should be created when lovers are off, got %v", pairs)
	}
}

func TestPairLoversPicksExactlyOnePair(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableLovers = true
	gs := nightGame(t, cfg, RoleVillager, RoleVillager, RoleMafia, RoleVillager, RoleVillager)

	pairs := pairLovers(gs, &deterministicReader{})

	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %d", len(pairs))
	}
	a, b := pairs[0][0], pairs[0][1]
	if a == b {
		t.Fatal("a player cannot be their own lover")
	}
	if gs.Players[a].LoverID != b || gs.Players[b].LoverID != a {
		t.Error("the pairing should be recorded on both players")
	}
	paired := 0
	for _, p := range gs.Players {
		if p.LoverID != 0 {
			paired++
		}
	}
	if paired != 2 {
		t.Errorf("exactly two players should be paired, got %d", paired)
	}
}

// ---------------------------------------------------------------------------
// Win conditions with a third killer faction
// ---------------------------------------------------------------------------

func TestWinConditionsWithAKillerFaction(t *testing.T) {
	tests := []struct {
		name  string
		roles []Role
		want  Team
	}{
		{"town clears both threats", []Role{RoleVillager, RoleDetective, RoleDoctor}, TeamTown},
		{"mafia at parity with no rival", []Role{RoleMafia, RoleVillager}, TeamMafia},
		{"serial killer at parity", []Role{RoleSerialKiller, RoleVillager}, TeamKiller},
		{"mafia and killer both alive is not over", []Role{RoleMafia, RoleSerialKiller, RoleVillager}, ""},
		{"mafia outnumbered keeps playing", []Role{RoleMafia, RoleVillager, RoleVillager, RoleVillager}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gs := nightGame(t, DefaultConfig(), tt.roles...)
			gs.Phase = PhaseDiscussion

			result := checkWinCondition(gs)

			if tt.want == "" {
				if result != nil {
					t.Fatalf("expected the game to continue, got %q", result.Winner)
				}
				return
			}
			if result == nil {
				t.Fatalf("expected %q to win, but the game continued", tt.want)
			}
			if result.Winner != tt.want {
				t.Errorf("winner = %q, want %q", result.Winner, tt.want)
			}
		})
	}
}

// The mafia holding parity does not end the game while a serial killer is
// still hunting them, because the killer can still take it.
func TestMafiaParityDoesNotWinWhileAKillerLives(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleMafia, RoleSerialKiller, RoleVillager)
	gs.Phase = PhaseDiscussion

	if result := checkWinCondition(gs); result != nil {
		t.Errorf("game should continue, got winner %q", result.Winner)
	}
}

func TestSurvivorWinsAlongsideWhoeverWon(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleSurvivor, RoleMafia, RoleVillager)
	gs.Phase = PhaseDiscussion

	gs, _ = endGame(gs, &WinResult{Winner: TeamMafia, Description: "mafia"}, nil)
	summary := BuildGameSummary(gs)

	if !summary.WonFor(1) {
		t.Error("a living survivor should win regardless of the faction result")
	}
	if !summary.WonFor(2) {
		t.Error("the mafia should win their own game")
	}
	if summary.WonFor(3) {
		t.Error("the villager should not win a mafia victory")
	}
}

func TestDeadSurvivorLoses(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleSurvivor, RoleMafia, RoleVillager)
	gs.Players[1].Alive = false

	gs, _ = endGame(gs, &WinResult{Winner: TeamMafia, Description: "mafia"}, nil)

	if BuildGameSummary(gs).WonFor(1) {
		t.Error("a survivor who did not survive should not win")
	}
}

// ---------------------------------------------------------------------------
// Mayor
// ---------------------------------------------------------------------------

func TestMayorRevealWeightsTheirVote(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MayorVoteWeight = 3
	gs := nightGame(t, cfg, RoleMayor, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	gs.Phase = PhaseDiscussion

	gs, effects := Reduce(gs, RevealEvent{PlayerID: 1})

	if gs.Players[1].VoteWeight() != 3 {
		t.Errorf("a revealed mayor's vote weight = %d, want 3", gs.Players[1].VoteWeight())
	}
	if !gs.Players[1].RoleRevealed {
		t.Error("revealing should make the role public")
	}
	if !containsSubstring(groupTexts(effects), "MAYOR") {
		t.Error("the group should be told about the reveal")
	}

	// The threshold is measured against total weight, so revealing raises the
	// bar for everyone rather than only helping the mayor.
	if got, want := gs.TotalVoteWeight(), 7; got != want {
		t.Errorf("total vote weight = %d, want %d", got, want)
	}
	if got, want := gs.LynchThreshold(), 4; got != want {
		t.Errorf("lynch threshold = %d, want %d", got, want)
	}
}

func TestMayorCannotRevealTwice(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMayor, RoleMafia, RoleVillager)
	gs.Phase = PhaseDiscussion

	gs, _ = Reduce(gs, RevealEvent{PlayerID: 1})
	weight := gs.Players[1].VoteWeight()
	gs, effects := Reduce(gs, RevealEvent{PlayerID: 1})

	if gs.Players[1].VoteWeight() != weight {
		t.Error("a second reveal should not stack more weight")
	}
	if !containsSubstring(dmsTo(effects, 1), "already revealed") {
		t.Error("a repeat reveal should say so")
	}
}

// Revealing at night would hand the mafia a target before the town could get
// any value from it.
func TestMayorCannotRevealAtNight(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMayor, RoleMafia, RoleVillager)

	gs, effects := Reduce(gs, RevealEvent{PlayerID: 1})

	if gs.Players[1].RoleRevealed {
		t.Error("the mayor should not be able to reveal at night")
	}
	if !containsSubstring(dmsTo(effects, 1), "during the day") {
		t.Error("the mayor should be told when they may reveal")
	}
}

func TestOnlyTheMayorCanReveal(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleMafia, RoleVillager)
	gs.Phase = PhaseDiscussion

	gs, effects := Reduce(gs, RevealEvent{PlayerID: 1})

	if gs.Players[1].RoleRevealed {
		t.Error("a villager must not be able to fake a mayor reveal")
	}
	if !containsSubstring(dmsTo(effects, 1), "Only the Mayor") {
		t.Error("a non-mayor should be told they cannot reveal")
	}
}

func TestWeightedVoteReachesTheThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MayorVoteWeight = 3
	cfg.AllowNoLynch = false
	cfg.AllowLastWords = false
	gs := nightGame(t, cfg, RoleMayor, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	gs.Phase = PhaseDiscussion
	gs, _ = Reduce(gs, RevealEvent{PlayerID: 1})
	gs.Phase = PhaseVoting

	// Mayor (3) plus one villager (1) is 4 of the 7 total weight.
	gs, _ = Reduce(gs, VoteEvent{Vote: Vote{VoterID: 1, TargetID: 2}})
	gs, _ = Reduce(gs, VoteEvent{Vote: Vote{VoterID: 3, TargetID: 2}})

	if got := gs.VoteCounts()[2]; got != 4 {
		t.Fatalf("weighted tally for the target = %d, want 4", got)
	}

	gs, _ = Reduce(gs, TimeoutEvent{Phase: PhaseVoting})

	if gs.Players[2].Alive {
		t.Error("a weighted majority should carry the lynch")
	}
}

// ---------------------------------------------------------------------------
// Reactions
// ---------------------------------------------------------------------------

func TestReactionsAreOnePerPlayerAndChangeable(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleMafia, RoleVillager)
	gs.Phase = PhaseDiscussion
	moods := MoodEmojis()

	gs, _ = Reduce(gs, ReactEvent{PlayerID: 1, Emoji: moods[0]})
	gs, _ = Reduce(gs, ReactEvent{PlayerID: 1, Emoji: moods[0]})
	if gs.Reactions[moods[0]] != 1 {
		t.Errorf("tapping the same mood twice should count once, got %d", gs.Reactions[moods[0]])
	}

	gs, _ = Reduce(gs, ReactEvent{PlayerID: 1, Emoji: moods[1]})
	if _, still := gs.Reactions[moods[0]]; still {
		t.Error("changing a reaction should release the previous one")
	}
	if gs.Reactions[moods[1]] != 1 {
		t.Errorf("the new mood should hold the vote, got %d", gs.Reactions[moods[1]])
	}
}

func TestReactionsRejectArbitraryInput(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleMafia, RoleVillager)
	gs.Phase = PhaseDiscussion

	gs, _ = Reduce(gs, ReactEvent{PlayerID: 1, Emoji: "*bold*"})

	if len(gs.Reactions) != 0 {
		t.Errorf("only the offered moods should be accepted, got %v", gs.Reactions)
	}
}

func TestReactionsResetEachDay(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleVillager, RoleVillager, RoleVillager, RoleVillager)
	gs.Phase = PhaseDiscussion
	gs, _ = Reduce(gs, ReactEvent{PlayerID: 2, Emoji: MoodEmojis()[0]})

	gs, _ = transitionToNight(gs)
	act(gs, 1, ActionMafiaKill, 5)
	gs, _ = resolveNight(gs)

	if len(gs.Reactions) != 0 {
		t.Errorf("the mood bar should start fresh each day, got %v", gs.Reactions)
	}
}

// ---------------------------------------------------------------------------
// Private channels
// ---------------------------------------------------------------------------

func TestMafiaChatReachesTeammatesOnly(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleGodfather, RoleVillager, RoleDoctor, RoleVillager)

	_, effects := Reduce(gs, MafiaChatEvent{FromID: 1, Message: "take the doctor"})

	if len(dmsTo(effects, 2)) == 0 {
		t.Error("a teammate should receive the message")
	}
	for _, id := range []PlayerID{3, 4, 5} {
		if len(dmsTo(effects, id)) != 0 {
			t.Errorf("player %d is not mafia but received the mafia chat", id)
		}
	}
	for _, eff := range effects {
		if _, ok := eff.(SendGroupEffect); ok {
			t.Error("mafia chat must never reach the group")
		}
	}
}

func TestMafiaChatIsClosedDuringTheDay(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleGodfather, RoleVillager)
	gs.Phase = PhaseDiscussion

	_, effects := Reduce(gs, MafiaChatEvent{FromID: 1, Message: "hello"})

	if len(dmsTo(effects, 2)) != 0 {
		t.Error("the mafia should not be able to coordinate during the day")
	}
	if !containsSubstring(dmsTo(effects, 1), "only open at night") {
		t.Error("the sender should be told why the message did not go through")
	}
}

func TestMafiaChatRejectsTownspeople(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleMafia, RoleVillager)

	_, effects := Reduce(gs, MafiaChatEvent{FromID: 1, Message: "let me in"})

	if len(dmsTo(effects, 2)) != 0 {
		t.Error("a villager must not be able to inject a message into mafia chat")
	}
	if !containsSubstring(dmsTo(effects, 1), "Only the mafia") {
		t.Error("the villager should be refused explicitly")
	}
}

func TestMafiaChatEscapesMarkdown(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleGodfather, RoleVillager)

	_, effects := Reduce(gs, MafiaChatEvent{FromID: 1, Message: "*not bold* [link](x)"})

	relayed := dmsTo(effects, 2)
	if len(relayed) == 0 {
		t.Fatal("the teammate received nothing")
	}
	if strings.Contains(relayed[0], "*not bold*") {
		t.Errorf("relayed text should be escaped, got %q", relayed[0])
	}
}

func TestGhostChatIsForTheDeadOnly(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleMafia, RoleVillager, RoleVillager)
	gs.Players[1].Alive = false
	gs.Players[3].Alive = false

	_, effects := Reduce(gs, GhostChatEvent{FromID: 1, Message: "it was p2"})

	if len(dmsTo(effects, 3)) == 0 {
		t.Error("another ghost should hear it")
	}
	for _, id := range []PlayerID{2, 4} {
		if len(dmsTo(effects, id)) != 0 {
			t.Errorf("living player %d must not see ghost chat", id)
		}
	}
}

func TestLivingPlayersCannotUseGhostChat(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleMafia, RoleVillager)
	gs.Players[3].Alive = false

	_, effects := Reduce(gs, GhostChatEvent{FromID: 1, Message: "hello"})

	if len(dmsTo(effects, 3)) != 0 {
		t.Error("a living player must not be able to talk to the dead")
	}
	if !containsSubstring(dmsTo(effects, 1), "still very much alive") {
		t.Error("the living sender should be refused")
	}
}

func TestDisabledChannelsSaySo(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MafiaNightChat = false
	cfg.GhostChat = false
	gs := nightGame(t, cfg, RoleMafia, RoleGodfather, RoleVillager)
	gs.Players[3].Alive = false

	_, mafiaEffects := Reduce(gs, MafiaChatEvent{FromID: 1, Message: "hi"})
	_, ghostEffects := Reduce(gs, GhostChatEvent{FromID: 3, Message: "hi"})

	if !containsSubstring(dmsTo(mafiaEffects, 1), "disabled") {
		t.Error("a disabled mafia chat should tell the sender")
	}
	if !containsSubstring(dmsTo(ghostEffects, 3), "disabled") {
		t.Error("a disabled ghost chat should tell the sender")
	}
}

// ---------------------------------------------------------------------------
// Settings and presets
// ---------------------------------------------------------------------------

// Every setting must survive a round trip through the panel: tapping it
// produces a value ApplySetting accepts and Display reflects.
func TestEverySettingRoundTrips(t *testing.T) {
	for _, setting := range Settings() {
		t.Run(setting.Key, func(t *testing.T) {
			if setting.Label == "" || setting.Help == "" {
				t.Error("a settings button needs a label and a help line")
			}
			cfg := DefaultConfig()
			before := setting.Get(cfg)
			next := setting.Next(cfg)
			if next == before {
				t.Fatalf("tapping %q did not change anything (%q)", setting.Key, before)
			}

			ApplySetting(&cfg, setting.Key, next)
			if got := setting.Get(cfg); got != next {
				t.Errorf("after applying %q: got %q, want %q", next, got, next)
			}
			if setting.Display(cfg) == "" {
				t.Error("the button needs something to display")
			}
		})
	}
}

// Cycling a choice setting through every option must return to the start, so
// no value can be reached but not left.
func TestChoiceSettingsCycleThroughEveryOption(t *testing.T) {
	for _, setting := range Settings() {
		if setting.Kind != SettingChoice {
			continue
		}
		cfg := DefaultConfig()
		seen := map[string]bool{}
		for range setting.Choices {
			value := setting.Next(cfg)
			if seen[value] {
				t.Errorf("%s: revisited %q before covering every choice", setting.Key, value)
				break
			}
			seen[value] = true
			ApplySetting(&cfg, setting.Key, value)
		}
		if len(seen) != len(setting.Choices) {
			t.Errorf("%s: reached %d of %d choices", setting.Key, len(seen), len(setting.Choices))
		}
	}
}

// A stored setting from an older build, or an outright hostile callback, must
// be ignored rather than corrupting the config.
func TestApplySettingIgnoresBadInput(t *testing.T) {
	cfg := DefaultConfig()
	original := cfg

	ApplySetting(&cfg, "no_such_setting", "true")
	ApplySetting(&cfg, "night", "not-a-number")
	ApplySetting(&cfg, "night", "999999")
	ApplySetting(&cfg, "lovers", "maybe")

	if cfg.NightTimeoutSec != original.NightTimeoutSec {
		t.Errorf("night timeout changed to %d", cfg.NightTimeoutSec)
	}
	if cfg.EnableLovers != original.EnableLovers {
		t.Error("lovers changed on an unparseable value")
	}
}

func TestEveryPresetIsPlayable(t *testing.T) {
	for _, name := range PresetNames() {
		cfg := PresetConfig(name)
		if cfg.PresetName != name {
			t.Errorf("preset %q reports itself as %q", name, cfg.PresetName)
		}
		if err := ValidateConfig(cfg); err != nil {
			t.Errorf("preset %q is not playable: %v", name, err)
		}
		label, pitch := PresetLabel(name)
		if label == "" || pitch == "" {
			t.Errorf("preset %q has no label or pitch", name)
		}
	}
}

func TestUnknownPresetFallsBackToDefault(t *testing.T) {
	cfg := PresetConfig("something-we-removed")

	if err := ValidateConfig(cfg); err != nil {
		t.Errorf("the fallback config must be playable: %v", err)
	}
	if cfg.PresetName != PresetClassic {
		t.Errorf("fallback preset = %q, want %q", cfg.PresetName, PresetClassic)
	}
}

// Every setting must survive being applied on top of every preset, because
// that is exactly what a group's stored overrides do.
func TestSettingsApplyOnTopOfEveryPreset(t *testing.T) {
	for _, name := range PresetNames() {
		for _, setting := range Settings() {
			cfg := PresetConfig(name)
			ApplySetting(&cfg, setting.Key, setting.Next(cfg))
			if err := ValidateConfig(cfg); err != nil {
				t.Errorf("preset %q with %q toggled is unplayable: %v", name, setting.Key, err)
			}
		}
	}
}

func TestSetSettingValueAcceptsCustomNumbers(t *testing.T) {
	cfg := DefaultConfig()

	if err := SetSettingValue(&cfg, "night", "75"); err != nil {
		t.Fatalf("custom night: %v", err)
	}
	if cfg.NightTimeoutSec != 75 {
		t.Errorf("night = %d, want 75", cfg.NightTimeoutSec)
	}

	if err := SetSettingValue(&cfg, "night", "10"); err == nil {
		t.Error("night below minimum should fail")
	}
	if err := SetSettingValue(&cfg, "lovers", "on"); err != nil {
		t.Fatalf("lovers on: %v", err)
	}
	if !cfg.EnableLovers {
		t.Error("lovers should be on")
	}
	if err := SetSettingValue(&cfg, "lovers", "off"); err != nil {
		t.Fatalf("lovers off: %v", err)
	}
	if cfg.EnableLovers {
		t.Error("lovers should be off")
	}
}

func TestLobbyConfigOnlyWhileOpen(t *testing.T) {
	gs := NewGameState("g1", -100, 1, DefaultConfig())
	gs.Config.MinPlayers = 1
	gs, _ = Reduce(gs, GameCreatedEvent{})

	gs, effects := Reduce(gs, ConfigPresetEvent{PlayerID: 1, Preset: PresetChaos})
	if gs.Config.PresetName != PresetChaos {
		t.Fatalf("preset = %q, want %q", gs.Config.PresetName, PresetChaos)
	}
	if len(effects) == 0 {
		t.Fatal("expected lobby refresh effects")
	}

	beforeLovers := gs.Config.EnableLovers
	gs, effects = Reduce(gs, ConfigSettingEvent{PlayerID: 2, Key: "lovers"})
	if gs.Config.EnableLovers != beforeLovers {
		t.Fatal("non-host should not change lobby config")
	}
	if len(effects) != 0 {
		t.Fatal("unauthorized config change should emit nothing")
	}

	gs, _ = Reduce(gs, BeginEvent{PlayerID: 1})
	if gs.Phase == PhaseLobby {
		// Begin needs a full roster to deal roles; for the lock test, move past
		// the lobby explicitly once lobby-only behaviour has been exercised.
		gs.Phase = PhaseRoleAssign
	}
	gs, effects = Reduce(gs, ConfigPresetEvent{PlayerID: 1, Preset: PresetSpeed})
	if gs.Config.PresetName != PresetChaos {
		t.Fatalf("config after begin = %q, want locked %q", gs.Config.PresetName, PresetChaos)
	}
	if len(effects) != 0 {
		t.Fatal("config changes after begin should be ignored")
	}
}

func TestValidateConfigRejectsBrokenCombinations(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(*GameConfig)
	}{
		{"mayor weight below one", func(c *GameConfig) { c.MayorVoteWeight = 0 }},
		{"min above max", func(c *GameConfig) { c.MinPlayers = 30 }},
		{"no night time", func(c *GameConfig) { c.NightTimeoutSec = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.break_(&cfg)
			if err := ValidateConfig(cfg); err == nil {
				t.Error("expected the config to be rejected")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Summary, awards, and timeline
// ---------------------------------------------------------------------------

func TestSummaryRecordsFirstBloodAndDeathCause(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleVillager, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionMafiaKill, 2)
	gs, _ = resolveNight(gs)
	gs, _ = endGame(gs, &WinResult{Winner: TeamTown, Description: "town"}, nil)

	summary := BuildGameSummary(gs)

	victim, ok := summary.Result(2)
	if !ok {
		t.Fatal("the victim is missing from the summary")
	}
	if !victim.DiedFirst {
		t.Error("the first player to die should be marked as such")
	}
	if victim.DeathCause != CauseMafia {
		t.Errorf("death cause = %q, want %q", victim.DeathCause, CauseMafia)
	}
	if victim.Survived {
		t.Error("a dead player did not survive")
	}

	var keys []string
	for _, a := range summary.Awards {
		keys = append(keys, a.Key)
	}
	if !containsSubstring(keys, "first_blood") {
		t.Errorf("expected a first blood award, got %v", keys)
	}
}

// An award with a tie has no clear winner, and inventing one reads worse than
// handing out nothing.
func TestTiedAwardsAreNotHandedOut(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleDoctor, RoleDoctor, RoleMafia, RoleVillager)
	gs.Players[1].Stats.Saves = 2
	gs.Players[2].Stats.Saves = 2

	summary := BuildGameSummary(gs)

	for _, a := range summary.Awards {
		if a.Key == "guardian" {
			t.Error("a tied save count should not produce a guardian award")
		}
	}
}

func TestSummaryIsAbortedWhenThereIsNoWinner(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleVillager, RoleVillager)
	gs, _ = endGame(gs, &WinResult{Winner: "", Description: "cancelled"}, nil)

	summary := BuildGameSummary(gs)

	if !summary.Aborted {
		t.Error("a game with no faction winner is aborted")
	}
	for _, p := range summary.Players {
		if p.Won {
			t.Errorf("player %d should not win an aborted game", p.ID)
		}
	}
}

func TestTimelineTellsTheStory(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleVillager, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionMafiaKill, 2)

	gs, _ = resolveNight(gs)

	if len(gs.Timeline) == 0 {
		t.Fatal("a night with a death should leave a timeline entry")
	}
	last := gs.Timeline[len(gs.Timeline)-1]
	if last.Day != gs.DayNumber {
		t.Errorf("timeline entry recorded day %d, want %d", last.Day, gs.DayNumber)
	}
	if !strings.Contains(last.Text, gs.Players[2].PlainName()) {
		t.Errorf("the entry should name the victim, got %q", last.Text)
	}
}

func TestQuietNightIsRecordedInTheTimeline(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleDoctor, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	act(gs, 1, ActionDoctorProtect, 3)
	act(gs, 2, ActionMafiaKill, 3)

	gs, _ = resolveNight(gs)

	found := false
	for _, entry := range gs.Timeline {
		if strings.Contains(entry.Text, "quiet night") {
			found = true
		}
	}
	if !found {
		t.Errorf("a night with no deaths should be recorded, got %v", gs.Timeline)
	}
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

func TestProgressBarStaysWithinItsWidth(t *testing.T) {
	tests := []struct{ value, total, width int }{
		{0, 10, 8}, {1, 10, 8}, {5, 10, 8}, {10, 10, 8},
		{-1, 10, 8}, {99, 10, 8}, {3, 0, 8}, {3, -1, 8},
	}
	for _, tt := range tests {
		bar := ProgressBar(tt.value, tt.total, tt.width)
		if got := len([]rune(bar)); got != tt.width {
			t.Errorf("ProgressBar(%d, %d, %d) has %d cells, want %d",
				tt.value, tt.total, tt.width, got, tt.width)
		}
	}
	if ProgressBar(1, 100, 10) == ProgressBar(0, 100, 10) {
		t.Error("a single unit of progress should be visible")
	}
	if got := ProgressBar(1, 10, 0); got != "" {
		t.Errorf("a zero-width bar should be empty, got %q", got)
	}
}

func TestGraveyardListsTheDeadInOrder(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleVillager, RoleVillager, RoleVillager)

	if !strings.Contains(FormatGraveyard(gs), "Nobody has died yet") {
		t.Error("an empty graveyard should say so")
	}

	killPlayer(gs, 3, CauseMafia)
	killPlayer(gs, 2, CauseLynch)
	gs.Players[2].RoleRevealed = true

	panel := FormatGraveyard(gs)

	firstIndex := strings.Index(panel, gs.Players[3].PlainName())
	secondIndex := strings.Index(panel, gs.Players[2].PlainName())
	if firstIndex < 0 || secondIndex < 0 {
		t.Fatalf("both dead players should be listed, got %q", panel)
	}
	if firstIndex > secondIndex {
		t.Error("the graveyard should list the dead in the order they died")
	}
	// Only roles already made public may be shown.
	if !strings.Contains(panel, RoleTitle(RoleVillager)) {
		t.Error("a revealed role should be shown")
	}
	if !strings.Contains(panel, "role unknown") {
		t.Error("an unrevealed role must stay hidden")
	}
}

func TestVoteBoardShowsTallyAndThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowNoLynch = false
	gs := nightGame(t, cfg, RoleVillager, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	gs.Phase = PhaseVoting
	gs.Votes[1] = Vote{VoterID: 1, TargetID: 2}
	gs.Votes[3] = Vote{VoterID: 3, TargetID: 2}

	board := FormatVoteBoard(gs)

	if !strings.Contains(board, "needed to lynch") {
		t.Error("the board should state the threshold")
	}
	if !strings.Contains(board, "2/5 voted") {
		t.Errorf("the board should show turnout, got %q", board)
	}
	if !strings.Contains(board, gs.Players[2].PlainName()) {
		t.Error("the board should name the leading candidate")
	}
}

func TestFinalBoardHandlesAnEmptyVote(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleMafia, RoleVillager)
	gs.Phase = PhaseVoting

	if !strings.Contains(FormatFinalVoteBoard(gs, voteTally(gs)), "not a single vote") {
		t.Error("an empty final tally should say so plainly")
	}
}

func TestFormatDurationReadsLikeAPersonWouldSayIt(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m"},
		{45 * time.Minute, "45m"},
		{90 * time.Minute, "1h 30m"},
	}
	for _, tt := range tests {
		if got := FormatDuration(tt.in); got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRoleListCoversEveryRole(t *testing.T) {
	list := FormatRoleList()

	for _, info := range AllRoles() {
		if !strings.Contains(list, info.Title) {
			t.Errorf("the /roles reference is missing %q", info.Title)
		}
	}
}

// The role reference groups by faction, so factions must appear in a stable
// order rather than Go's random map order.
func TestAllRolesIsStablyOrdered(t *testing.T) {
	first := AllRoles()
	for i := 0; i < 5; i++ {
		next := AllRoles()
		for j := range first {
			if first[j].Role != next[j].Role {
				t.Fatalf("AllRoles is not stable at index %d: %q then %q",
					j, first[j].Role, next[j].Role)
			}
		}
	}
	if first[0].Team != TeamTown {
		t.Errorf("town should be listed first, got %q", first[0].Team)
	}
}

// ---------------------------------------------------------------------------
// Night action validation
// ---------------------------------------------------------------------------

// The keyboard is not a security boundary: a hand-crafted callback must not
// reach a target the role was never offered.
func TestNightActionRejectsAnUnofferedTarget(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleMafia, RoleGodfather, RoleVillager, RoleVillager)

	gs, effects := Reduce(gs, NightActionEvent{
		Action: NightAction{ActorID: 1, Kind: ActionMafiaKill, TargetID: 2},
	})

	if _, submitted := gs.NightActions[1]; submitted {
		t.Error("the mafia should not be able to target a teammate")
	}
	if !containsSubstring(dmsTo(effects, 1), "not a valid target") {
		t.Error("the actor should be told the target was refused")
	}
}

func TestNightActionRejectsTheWrongActionKind(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVillager, RoleMafia, RoleVillager, RoleVillager)

	gs, _ = Reduce(gs, NightActionEvent{
		Action: NightAction{ActorID: 1, Kind: ActionMafiaKill, TargetID: 3},
	})

	if _, submitted := gs.NightActions[1]; submitted {
		t.Error("a villager must not be able to submit a mafia kill")
	}
}

func TestSpentOneShotIsNotWaitedOn(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVigilante, RoleMafia, RoleVillager, RoleVillager)
	gs.Players[1].UsedAbility = true

	if roleNeedsAction(gs, gs.Players[1]) {
		t.Error("a vigilante who already fired should not hold up the night")
	}
	if targets := roleTargets(gs, gs.Players[1]); targets != nil {
		t.Errorf("a spent vigilante should be offered nothing, got %v", targets)
	}
}

// A vigilante whose shot was blocked keeps the bullet, because the shot never
// happened.
func TestBlockedVigilanteKeepsTheirBullet(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVigilante, RoleEscort, RoleMafia, RoleVillager, RoleVillager)
	act(gs, 1, ActionVigilanteKill, 4)
	act(gs, 2, ActionEscortBlock, 1)

	gs, _ = resolveNight(gs)

	if !gs.Players[4].Alive {
		t.Error("a blocked vigilante should not land their shot")
	}
	if gs.Players[1].UsedAbility {
		t.Error("a blocked vigilante keeps their one bullet")
	}
}

// A vigilante whose target was saved has still fired, so the bullet is gone.
func TestSavedTargetStillCostsTheBullet(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleVigilante, RoleDoctor, RoleMafia, RoleVillager, RoleVillager)
	act(gs, 1, ActionVigilanteKill, 4)
	act(gs, 2, ActionDoctorProtect, 4)

	gs, _ = resolveNight(gs)

	if !gs.Players[4].Alive {
		t.Error("the doctor should have saved the target")
	}
	if !gs.Players[1].UsedAbility {
		t.Error("firing at a saved target still spends the bullet")
	}
}

// A night where nobody has anything to submit must resolve immediately rather
// than sitting out the clock in silence.
func TestNightWithNoActorsResolvesImmediately(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FirstNightKill = false
	gs := nightGame(t, cfg, RoleMafia, RoleVillager, RoleVillager, RoleVillager, RoleVillager)
	gs.Phase = PhaseDiscussion
	gs.DayNumber = 0

	gs, _ = transitionToNight(gs)

	if gs.Phase != PhaseDiscussion {
		t.Errorf("a night with no actions should roll straight into the day, got %s", gs.Phase)
	}
}

// The pending-actor list is the most useful nudge the night warning can give,
// and it leaks nothing: the roster is already public.
func TestNightWarningNamesWhoIsStillMissing(t *testing.T) {
	gs := nightGame(t, DefaultConfig(), RoleDetective, RoleDoctor, RoleMafia, RoleVillager, RoleVillager)
	act(gs, 1, ActionDetectiveCheck, 3)

	_, effects := Reduce(gs, TimerWarningEvent{Phase: PhaseNight, SecondsLeft: 10})

	texts := groupTexts(effects)
	if !containsSubstring(texts, gs.Players[2].PlainName()) {
		t.Errorf("the warning should name the doctor who has not submitted, got %v", texts)
	}
	if containsSubstring(texts, gs.Players[1].PlainName()) {
		t.Error("a player who already submitted should not be listed as pending")
	}
}

// During a vote the countdown belongs on the board that is already there.
func TestVotingWarningRefreshesTheBoardInPlace(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LiveVoteBoard = true
	gs := nightGame(t, cfg, RoleVillager, RoleMafia, RoleVillager, RoleVillager, RoleVillager)
	gs.Phase = PhaseVoting

	_, effects := Reduce(gs, TimerWarningEvent{Phase: PhaseVoting, SecondsLeft: 10})

	if len(effects) != 1 {
		t.Fatalf("expected a single board refresh, got %d effects", len(effects))
	}
	if _, ok := effects[0].(UpdateVoteBoardEffect); !ok {
		t.Errorf("expected the board to be edited, got %T", effects[0])
	}
}
