package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// roleDMDeals reports which deal each role DM in the batch was tagged with.
func roleDMDeals(effects []SideEffect) map[PlayerID]int {
	out := map[PlayerID]int{}
	for _, eff := range effects {
		if dm, ok := eff.(SendRoleDMEffect); ok {
			out[dm.PlayerID] = dm.Deal
		}
	}
	return out
}

func sealDeal(effects []SideEffect) (int, bool) {
	for _, eff := range effects {
		if rd, ok := eff.(RolesDeliveredEffect); ok {
			return rd.Deal, true
		}
	}
	return 0, false
}

// dealRoles takes a fresh game into role assignment without the driver's
// automatic delivery loop, so the delivery events can be applied by hand in the
// order a real batch would produce them.
func dealRoles(t *testing.T, n int) (*GameState, []SideEffect) {
	t.Helper()
	d := newDriver(t, DefaultConfig(), n)
	gs, _ := Reduce(d.gs, GameCreatedEvent{})
	gs, effects := Reduce(gs, BeginEvent{PlayerID: 1})
	if gs.Phase != PhaseRoleAssign {
		t.Fatalf("expected role_assign, got %s", gs.Phase)
	}
	return gs, effects
}

// ---------------------------------------------------------------------------
// Deal generations
// ---------------------------------------------------------------------------

func TestEveryRoleDMCarriesTheCurrentDeal(t *testing.T) {
	gs, effects := dealRoles(t, 6)

	deals := roleDMDeals(effects)
	if len(deals) != 6 {
		t.Fatalf("expected a role DM per player, got %d", len(deals))
	}
	for pid, deal := range deals {
		if deal != gs.DealNumber {
			t.Errorf("role DM for %d is tagged deal %d, want %d", pid, deal, gs.DealNumber)
		}
	}
	if seal, ok := sealDeal(effects); !ok || seal != gs.DealNumber {
		t.Errorf("the seal is tagged deal %d (present=%v), want %d", seal, ok, gs.DealNumber)
	}
}

// The completion of a batch that has been superseded says nothing about whether
// the roles now in play were delivered, so it must not start the night.
func TestSupersededDealCannotStartTheNight(t *testing.T) {
	gs, effects := dealRoles(t, 6)
	firstDeal, _ := sealDeal(effects)

	// One DM of the first batch failed, so the roles were redealt.
	gs, effects = Reduce(gs, RoleDeliveryFailedEvent{PlayerID: 3, Deal: firstDeal})
	if _, still := gs.Players[3]; still {
		t.Fatal("the player whose DM failed should have been removed")
	}
	secondDeal, ok := sealDeal(effects)
	if !ok || secondDeal == firstDeal {
		t.Fatalf("the redeal should start a new deal, got %d after %d", secondDeal, firstDeal)
	}

	// The abandoned batch finishes draining and reports itself complete.
	gs, _ = Reduce(gs, RolesDeliveredEvent{Deal: firstDeal})
	if gs.Phase != PhaseRoleAssign {
		t.Fatalf("a superseded deal started the night; phase is %s while deal %d is still in flight",
			gs.Phase, secondDeal)
	}

	gs, _ = Reduce(gs, RolesDeliveredEvent{Deal: secondDeal})
	if gs.Phase != PhaseNight {
		t.Errorf("the current deal should start the night, got %s", gs.Phase)
	}
}

// Two DMs of one batch can fail together. The first failure redeals and sends
// the second player a fresh role, so acting on their stale failure as well
// would eject a player who is reachable.
func TestSecondFailureFromTheSameBatchIsIgnored(t *testing.T) {
	gs, effects := dealRoles(t, 7)
	firstDeal, _ := sealDeal(effects)

	gs, effects = Reduce(gs, RoleDeliveryFailedEvent{PlayerID: 3, Deal: firstDeal})
	secondDeal, _ := sealDeal(effects)

	gs, _ = Reduce(gs, RoleDeliveryFailedEvent{PlayerID: 5, Deal: firstDeal})

	if _, still := gs.Players[5]; !still {
		t.Error("a stale failure removed a player who had already been sent a fresh role")
	}
	if gs.DealNumber != secondDeal {
		t.Errorf("a stale failure triggered another redeal: deal is %d, want %d", gs.DealNumber, secondDeal)
	}
	if gs.Phase != PhaseRoleAssign {
		t.Errorf("expected to still be dealing roles, got %s", gs.Phase)
	}
}

// The backstop timer can start the night before a role DM's failure is
// reported. Redealing is no longer possible then, but the failure still means
// the player never saw their role, so they must not be left holding it.
func TestFailureArrivingAfterTheNightStartsMarksThePlayerUnreachable(t *testing.T) {
	gs, effects := dealRoles(t, 7)
	deal, _ := sealDeal(effects)

	gs, _ = Reduce(gs, RolesDeliveredEvent{Deal: deal})
	if gs.Phase != PhaseNight {
		t.Fatalf("expected night, got %s", gs.Phase)
	}

	gs, _ = Reduce(gs, RoleDeliveryFailedEvent{PlayerID: 4, Deal: deal})

	p, ok := gs.Players[4]
	if !ok {
		t.Fatal("the player should stay on the roster once the game is under way")
	}
	if !p.Disconnected {
		t.Error("a role DM that never arrived must leave the player marked unreachable")
	}
	if p.CanAct() {
		t.Error("an unreachable player must not be counted among those the night waits for")
	}
}

// A failure reported after the roster fell back to the lobby has no redeal to
// trigger either, and the same rule applies.
func TestFailureArrivingAfterTheLobbyFallbackStillLandsSomewhere(t *testing.T) {
	gs, effects := dealRoles(t, 5)
	deal, _ := sealDeal(effects)

	// Five players is the minimum, so removing one returns to the lobby.
	gs, _ = Reduce(gs, RoleDeliveryFailedEvent{PlayerID: 5, Deal: deal})
	if gs.Phase != PhaseLobby {
		t.Fatalf("expected the lobby fallback, got %s", gs.Phase)
	}

	before := len(gs.Players)
	gs, _ = Reduce(gs, RoleDeliveryFailedEvent{PlayerID: 4, Deal: deal})
	if len(gs.Players) != before-1 {
		t.Errorf("a player who cannot be reached should be dropped from the lobby, roster went %d -> %d",
			before, len(gs.Players))
	}
}

// A failure can still be in flight when the game ends. The unreachable path it
// now falls through to re-runs the win check, which must not declare a second
// winner for a game that already has one.
func TestFailureArrivingAfterTheGameEndsChangesNothing(t *testing.T) {
	gs, effects := dealRoles(t, 7)
	deal, _ := sealDeal(effects)
	gs, _ = Reduce(gs, RolesDeliveredEvent{Deal: deal})
	gs, _ = Reduce(gs, EndGameEvent{PlayerID: 1})
	if !gs.Phase.IsTerminal() {
		t.Fatalf("expected a finished game, got %s", gs.Phase)
	}

	gs, effects = Reduce(gs, RoleDeliveryFailedEvent{PlayerID: 4, Deal: deal})

	for _, eff := range effects {
		if _, ok := eff.(GameOverEffect); ok {
			t.Error("a late failure declared a second winner for a finished game")
		}
	}
	if p := gs.Players[4]; p != nil && p.Disconnected {
		t.Error("a finished game should not be modified at all")
	}
}

// A player the bot could not reach during the deal stays on the roster, so the
// current deal must still be able to start the night once its DMs resolve.
func TestDisconnectDuringRoleAssignDoesNotBlockTheDeal(t *testing.T) {
	gs, effects := dealRoles(t, 7)
	deal, _ := sealDeal(effects)

	gs, _ = Reduce(gs, PlayerDisconnectedEvent{PlayerID: 4})
	if gs.Phase != PhaseRoleAssign {
		t.Fatalf("a disconnect mid-deal should leave assignment running, got %s", gs.Phase)
	}
	if !gs.Players[4].Disconnected {
		t.Fatal("the unreachable player should be marked silent")
	}

	gs, _ = Reduce(gs, RolesDeliveredEvent{Deal: deal})
	if gs.Phase != PhaseNight {
		t.Errorf("the current deal should still start the night, got %s", gs.Phase)
	}
	if gs.Players[4].CanAct() {
		t.Error("the silent player must not count toward night actions")
	}
}

// ---------------------------------------------------------------------------
// Resume during role assignment
// ---------------------------------------------------------------------------

// A restart loses track of which role DMs went out. Reporting the phase
// complete without sending any would start the night for players who never
// learned their role, with no redeal left to catch it.
func TestResumeDuringRoleAssignmentResendsTheRoleDMs(t *testing.T) {
	gs, effects := dealRoles(t, 6)
	firstDeal, _ := sealDeal(effects)
	gs.PhaseDeadline = time.Now().Add(30 * time.Second)

	gs, effects = Reduce(gs, ResumeEvent{})

	if gs.Phase != PhaseRoleAssign {
		t.Fatalf("resume should stay in role assignment, got %s", gs.Phase)
	}
	deals := roleDMDeals(effects)
	if len(deals) != 6 {
		t.Fatalf("resume re-sent %d role DMs, want one per player", len(deals))
	}
	if gs.DealNumber == firstDeal {
		t.Error("the re-sent DMs belong to a new deal, so outcomes from before the restart cannot close it")
	}
	for pid, deal := range deals {
		if deal != gs.DealNumber {
			t.Errorf("re-sent role DM for %d is tagged deal %d, want %d", pid, deal, gs.DealNumber)
		}
	}
	seal, ok := sealDeal(effects)
	if !ok || seal != gs.DealNumber {
		t.Errorf("the resumed seal is tagged deal %d (present=%v), want %d", seal, ok, gs.DealNumber)
	}
	if !hasTimerFor(effects, PhaseRoleAssign) {
		t.Error("resume must re-arm the backstop timer")
	}
}

func TestResumedRoleAssignmentStillReachesTheNight(t *testing.T) {
	gs, _ := dealRoles(t, 6)
	gs.PhaseDeadline = time.Now().Add(30 * time.Second)

	gs, effects := Reduce(gs, ResumeEvent{})
	deal, _ := sealDeal(effects)

	gs, _ = Reduce(gs, RolesDeliveredEvent{Deal: deal})
	if gs.Phase != PhaseNight {
		t.Errorf("expected night once the re-sent roles were delivered, got %s", gs.Phase)
	}
}

// ---------------------------------------------------------------------------
// Day actions require a player the bot can reach
// ---------------------------------------------------------------------------

func dayGame(t *testing.T, n int) *GameState {
	t.Helper()
	cfg := DefaultConfig()
	cfg.NominationSystem = true
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseDiscussion
	gs.DayNumber = 1
	for i := PlayerID(1); i <= PlayerID(n); i++ {
		gs.Players[i] = &Player{
			ID:       i,
			Username: fmt.Sprintf("player%d", i),
			Role:     RoleVillager,
			Alive:    true,
		}
	}
	gs.Players[1].Role = RoleMafia
	return gs
}

// A player marked unreachable is excluded from the vote, so allowing them to
// second a nomination would let them start a trial they cannot take part in.
func TestUnreachablePlayerCannotForceATrial(t *testing.T) {
	gs := dayGame(t, 7)
	gs.Players[2].Disconnected = true

	gs, _ = Reduce(gs, NominateEvent{NominatorID: 3, TargetID: 4})
	if gs.Phase != PhaseNomination {
		t.Fatalf("expected the nomination window, got %s", gs.Phase)
	}

	gs, _ = Reduce(gs, SecondEvent{PlayerID: 2, NominationTarget: 4})

	if gs.Phase == PhaseVoting {
		t.Error("an unreachable player put a trial in motion")
	}
	if gs.ActiveTrial != nil {
		t.Error("an unreachable player's second installed a trial")
	}
}

func TestUnreachablePlayerCannotNominate(t *testing.T) {
	gs := dayGame(t, 7)
	gs.Players[2].Disconnected = true

	gs, _ = Reduce(gs, NominateEvent{NominatorID: 2, TargetID: 4})

	if len(gs.Nominations) != 0 {
		t.Error("an unreachable player opened a nomination")
	}
	if gs.Phase != PhaseDiscussion {
		t.Errorf("the day should not have advanced, got %s", gs.Phase)
	}
}

// An unreachable player is still in the game and can still be put on trial.
func TestUnreachablePlayerCanStillBeNominated(t *testing.T) {
	gs := dayGame(t, 7)
	gs.Players[4].Disconnected = true

	gs, _ = Reduce(gs, NominateEvent{NominatorID: 3, TargetID: 4})

	if _, ok := gs.Nominations[4]; !ok {
		t.Error("an unreachable player should still be nominable — the town can lynch them")
	}
}

// The denominator has to be the population that can actually accuse, which is
// the same one every other majority in the game is measured against.
func TestAccusationMajorityCountsOnlyEligibleAccusers(t *testing.T) {
	gs := dayGame(t, 7)
	gs.Players[6].Disconnected = true
	gs.Players[7].Disconnected = true

	eligible := gs.EligibleVoterCount()
	if eligible != 5 {
		t.Fatalf("expected 5 eligible players, got %d", eligible)
	}

	gs, effects := Reduce(gs, AccuseEvent{AccuserID: 2, TargetID: 4})

	joined := strings.Join(groupTexts(effects), "\n")
	if !strings.Contains(joined, fmt.Sprintf("1/%d", eligible)) {
		t.Errorf("accusation count should be out of %d eligible accusers: %q", eligible, joined)
	}

	// Three of five is a majority; under the old count of seven it was not.
	for _, accuser := range []PlayerID{3, 5} {
		gs, effects = Reduce(gs, AccuseEvent{AccuserID: accuser, TargetID: 4})
	}
	joined = strings.Join(groupTexts(effects), "\n")
	if !strings.Contains(joined, "accused by the majority") {
		t.Errorf("3 of %d eligible accusers is a majority: %q", eligible, joined)
	}
}

func TestUnreachablePlayerCannotAccuseOrDefend(t *testing.T) {
	gs := dayGame(t, 7)
	gs.Players[2].Disconnected = true

	gs, _ = Reduce(gs, AccuseEvent{AccuserID: 2, TargetID: 4})
	if len(gs.Accusations[4]) != 0 {
		t.Error("an unreachable player registered an accusation")
	}

	gs, _ = Reduce(gs, DefendEvent{PlayerID: 2, Statement: "it wasn't me"})
	if gs.DefenseUsed[2] {
		t.Error("an unreachable player made a defense the group would see")
	}
}

// Whispers are DMs, so a recipient who has blocked the bot would never see one.
func TestWhisperToAnUnreachablePlayerIsRefused(t *testing.T) {
	gs := dayGame(t, 7)
	gs.Players[4].Disconnected = true

	gs, effects := Reduce(gs, WhisperEvent{FromID: 2, ToID: 4, Message: "hello"})

	if len(gs.Whispers) != 0 {
		t.Error("a whisper was recorded for a recipient who cannot receive DMs")
	}
	var told bool
	for _, eff := range effects {
		if dm, ok := eff.(SendDMEffect); ok && dm.PlayerID == 2 {
			told = true
		}
	}
	if !told {
		t.Error("the sender should be told why the whisper did not go through")
	}
}
