package engine

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"testing"
	"time"
)

// deterministicReader returns a deterministic io.Reader for testing
type deterministicReader struct {
	counter uint64
}

func (d *deterministicReader) Read(p []byte) (int, error) {
	for i := range p {
		d.counter++
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, d.counter)
		p[i] = buf[i%8]
	}
	return len(p), nil
}

func TestComputeMafiaCount(t *testing.T) {
	tests := []struct {
		n, divisor, want int
	}{
		{5, 4, 1},
		{6, 4, 1},
		{7, 4, 1},
		{8, 4, 2},
		{9, 4, 2},
		{12, 4, 3},
		{16, 4, 4},
		{20, 4, 5},
		{3, 4, 1},
		{2, 4, 1}, // max allowed = (2-1)/2 = 0, but min is 1... wait
	}
	for _, tt := range tests {
		got := ComputeMafiaCount(tt.n, tt.divisor)
		if tt.n == 2 {
			// Special: maxAllowed=(2-1)/2=0, but we clamp to min 1 first, then clamp to max 0
			// So result should be 0
			if got != 0 {
				t.Errorf("ComputeMafiaCount(%d, %d) = %d, want 0", tt.n, tt.divisor, got)
			}
			continue
		}
		if got != tt.want {
			t.Errorf("ComputeMafiaCount(%d, %d) = %d, want %d", tt.n, tt.divisor, got, tt.want)
		}
	}
}

func TestComputeMafiaCountNeverReachesHalf(t *testing.T) {
	for n := 3; n <= 50; n++ {
		count := ComputeMafiaCount(n, 4)
		half := (n + 1) / 2
		if count >= half {
			t.Errorf("n=%d: mafia count %d >= ceil(n/2)=%d", n, count, half)
		}
	}
}

func TestGenerateRoleSet(t *testing.T) {
	cfg := DefaultConfig()
	for n := 5; n <= 30; n++ {
		roles, err := GenerateRoleSet(n, cfg, rand.Reader)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if len(roles) != n {
			t.Errorf("n=%d: got %d roles", n, len(roles))
		}
		// Validate balance
		if err := ValidateBalance(roles, n); err != nil {
			t.Errorf("n=%d: validation failed: %v", n, err)
		}
	}
}

func TestAllocateRoles(t *testing.T) {
	cfg := DefaultConfig()
	players := []PlayerID{1, 2, 3, 4, 5, 6, 7}
	assignment, err := AllocateRoles(players, cfg, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignment) != len(players) {
		t.Errorf("expected %d assignments, got %d", len(players), len(assignment))
	}
	for _, pid := range players {
		if _, ok := assignment[pid]; !ok {
			t.Errorf("player %d has no role", pid)
		}
	}
}

func TestAllocateRolesTooFewPlayers(t *testing.T) {
	cfg := DefaultConfig()
	players := []PlayerID{1, 2, 3}
	_, err := AllocateRoles(players, cfg, rand.Reader)
	if err == nil {
		t.Error("expected error for too few players")
	}
}

func TestFisherYatesDeterministic(t *testing.T) {
	rng := &deterministicReader{}
	players := []PlayerID{1, 2, 3, 4, 5}
	result1 := FisherYatesShuffle(players, rng)

	rng2 := &deterministicReader{}
	result2 := FisherYatesShuffle(players, rng2)

	for i := range result1 {
		if result1[i] != result2[i] {
			t.Error("deterministic shuffle produced different results")
			break
		}
	}
}

func TestMinimalSafeRoleSet(t *testing.T) {
	cfg := DefaultConfig()
	for n := 5; n <= 20; n++ {
		roles := MinimalSafeRoleSet(n, cfg)
		if len(roles) != n {
			t.Errorf("n=%d: got %d roles", n, len(roles))
		}
		if err := ValidateBalance(roles, n); err != nil {
			t.Errorf("n=%d: minimal safe set failed validation: %v", n, err)
		}
	}
}

func TestReduceJoin(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Players[1] = &Player{ID: 1, Username: "host", Alive: true, JoinedAt: time.Now()}

	// Join new player
	gs, effects := Reduce(gs, JoinEvent{PlayerID: 2, Username: "player2", Time: time.Now()})
	if len(gs.Players) != 2 {
		t.Errorf("expected 2 players, got %d", len(gs.Players))
	}
	if len(effects) != 1 {
		t.Errorf("expected 1 effect, got %d", len(effects))
	}

	// Idempotent join
	gs, _ = Reduce(gs, JoinEvent{PlayerID: 2, Username: "player2", Time: time.Now()})
	if len(gs.Players) != 2 {
		t.Errorf("idempotent join failed: got %d players", len(gs.Players))
	}
}

func TestReduceJoinAfterStart(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseNight

	gs, effects := Reduce(gs, JoinEvent{PlayerID: 99, Username: "latecomer", Time: time.Now()})
	if _, ok := gs.Players[99]; ok {
		t.Error("player should not be added after game start")
	}
	if len(effects) != 1 {
		t.Error("expected rejection message")
	}
}

func TestReduceBeginNotEnoughPlayers(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Players[1] = &Player{ID: 1, Username: "host", Alive: true, JoinedAt: time.Now()}

	gs, effects := Reduce(gs, BeginEvent{PlayerID: 1})
	if gs.Phase != PhaseLobby {
		t.Error("should stay in lobby with insufficient players")
	}
	_ = effects
}

func TestReduceBeginSuccess(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	now := time.Now()
	for i := PlayerID(1); i <= 7; i++ {
		gs.Players[i] = &Player{ID: i, Username: "p" + string(rune('0'+i)), Alive: true, JoinedAt: now}
	}

	gs, effects := Reduce(gs, BeginEvent{PlayerID: 1})
	if gs.Phase != PhaseRoleAssign {
		t.Errorf("expected role_assign phase, got %s", gs.Phase)
	}
	// Should have DM effects for each player + group message
	if len(effects) < 7 {
		t.Errorf("expected at least 7 effects (DMs), got %d", len(effects))
	}
}

func TestReduceNightAction(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseNight
	gs.Players[1] = &Player{ID: 1, Username: "mafia1", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "villager1", Role: RoleVillager, Alive: true}
	gs.NightActions = make(map[PlayerID]NightAction)

	gs, _ = Reduce(gs, NightActionEvent{Action: NightAction{
		ActorID:  1,
		Kind:     ActionMafiaKill,
		TargetID: 2,
	}})

	if _, ok := gs.NightActions[1]; !ok {
		t.Error("night action not recorded")
	}
}

func TestReduceVote(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false // disable last words for deterministic test
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseVoting
	gs.Players[1] = &Player{ID: 1, Username: "p1", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "p2", Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "p3", Role: RoleVillager, Alive: true}
	gs.Votes = make(map[PlayerID]Vote)

	gs, _ = Reduce(gs, VoteEvent{Vote: Vote{VoterID: 2, TargetID: 1}})
	gs, _ = Reduce(gs, VoteEvent{Vote: Vote{VoterID: 3, TargetID: 1}})
	gs, _ = Reduce(gs, VoteEvent{Vote: Vote{VoterID: 1, TargetID: 2}})

	if gs.Players[1].Alive {
		t.Error("player 1 should have been lynched (had majority votes)")
	}
}

func TestReduceVoteWithLastWords(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = true
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseVoting
	gs.Players[1] = &Player{ID: 1, Username: "p1", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "p2", Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "p3", Role: RoleVillager, Alive: true}
	gs.Votes = make(map[PlayerID]Vote)

	gs, _ = Reduce(gs, VoteEvent{Vote: Vote{VoterID: 2, TargetID: 1}})
	gs, _ = Reduce(gs, VoteEvent{Vote: Vote{VoterID: 3, TargetID: 1}})
	gs, _ = Reduce(gs, VoteEvent{Vote: Vote{VoterID: 1, TargetID: 2}})

	// Should be in last words phase, player still alive
	if gs.Phase != PhaseLastWords {
		t.Errorf("expected last_words phase, got %s", gs.Phase)
	}
	if !gs.Players[1].Alive {
		t.Error("player should still be alive during last words")
	}

	// Complete last words
	gs, _ = Reduce(gs, LastWordsCompleteEvent{})
	if gs.Players[1].Alive {
		t.Error("player 1 should be dead after last words complete")
	}
}

func TestCheckWinConditionTownWins(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Players[1] = &Player{ID: 1, Role: RoleMafia, Alive: false}
	gs.Players[2] = &Player{ID: 2, Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Role: RoleVillager, Alive: true}

	result := checkWinCondition(gs)
	if result == nil || result.Winner != TeamTown {
		t.Error("town should win when all mafia are dead")
	}
}

func TestCheckWinConditionMafiaWins(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Players[1] = &Player{ID: 1, Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Role: RoleVillager, Alive: false}

	result := checkWinCondition(gs)
	if result == nil || result.Winner != TeamMafia {
		t.Error("mafia should win when they reach parity")
	}
}

func TestNightResolutionDoctorSaves(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseNight
	gs.DayNumber = 1
	gs.Players[1] = &Player{ID: 1, Username: "mafia", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "doctor", Role: RoleDoctor, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "victim", Role: RoleVillager, Alive: true}
	gs.Players[4] = &Player{ID: 4, Username: "v2", Role: RoleVillager, Alive: true}
	gs.NightActions = map[PlayerID]NightAction{
		1: {ActorID: 1, Kind: ActionMafiaKill, TargetID: 3},
		2: {ActorID: 2, Kind: ActionDoctorProtect, TargetID: 3},
	}

	gs, _ = resolveNight(gs)
	if !gs.Players[3].Alive {
		t.Error("doctor should have saved the victim")
	}
}

func TestGodfatherAppearsInnocent(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseNight
	gs.DayNumber = 1
	gs.Players[1] = &Player{ID: 1, Username: "gf", Role: RoleGodfather, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "det", Role: RoleDetective, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "v", Role: RoleVillager, Alive: true}
	gs.Players[4] = &Player{ID: 4, Username: "v2", Role: RoleVillager, Alive: true}
	gs.NightActions = map[PlayerID]NightAction{
		1: {ActorID: 1, Kind: ActionMafiaKill, TargetID: 3},
		2: {ActorID: 2, Kind: ActionDetectiveCheck, TargetID: 1},
	}

	gs, _ = resolveNight(gs)
	if gs.LastCheckResult == nil {
		t.Fatal("expected check result")
	}
	if gs.LastCheckResult.ResultTeam != TeamTown {
		t.Error("godfather should appear as town to detective")
	}
}

func TestHostReassignment(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	now := time.Now()
	gs.Players[1] = &Player{ID: 1, Username: "host", Alive: true, JoinedAt: now}
	gs.Players[2] = &Player{ID: 2, Username: "p2", Alive: true, JoinedAt: now.Add(time.Second)}

	gs, _ = Reduce(gs, LeaveEvent{PlayerID: 1})
	if gs.HostID != 2 {
		t.Errorf("host should be reassigned to player 2, got %d", gs.HostID)
	}
}

func TestEndGame(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Players[1] = &Player{ID: 1, Username: "host", Alive: true}
	gs.Phase = PhaseNight

	gs, _ = Reduce(gs, EndGameEvent{PlayerID: 1})
	if gs.Phase != PhaseGameOver {
		t.Error("game should be over after /endgame by host")
	}
}

func TestEndGameNonHost(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Players[1] = &Player{ID: 1, Username: "host", Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "other", Alive: true}
	gs.Phase = PhaseNight

	gs, _ = Reduce(gs, EndGameEvent{PlayerID: 2})
	if gs.Phase == PhaseGameOver {
		t.Error("non-host should not be able to end the game")
	}
}

// Verify the weighted sampling doesn't panic with edge cases
func TestSampleOptionalRolesEmpty(t *testing.T) {
	result := SampleOptionalRoles(nil, 3, &bytes.Reader{})
	if len(result) != 0 {
		t.Error("empty pool should return empty result")
	}
}

func TestSpecialRoleBudget(t *testing.T) {
	tests := []struct {
		n, divisor, want int
	}{
		{5, 3, 1},
		{6, 3, 2},
		{9, 3, 3},
		{12, 3, 4},
	}
	for _, tt := range tests {
		got := SpecialRoleBudget(tt.n, tt.divisor)
		if got != tt.want {
			t.Errorf("SpecialRoleBudget(%d, %d) = %d, want %d", tt.n, tt.divisor, got, tt.want)
		}
	}
}

func TestFirstNightImmunity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FirstNightKill = false
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseNight
	gs.DayNumber = 1
	gs.Players[1] = &Player{ID: 1, Username: "mafia", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "v1", Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "v2", Role: RoleVillager, Alive: true}
	gs.Players[4] = &Player{ID: 4, Username: "v3", Role: RoleVillager, Alive: true}
	gs.NightActions = make(map[PlayerID]NightAction)

	// Mafia tries to submit a kill on Night 1
	gs, effects := Reduce(gs, NightActionEvent{Action: NightAction{
		ActorID:  1,
		Kind:     ActionMafiaKill,
		TargetID: 2,
	}})
	// Should be rejected since night actions aren't prompted for mafia on Night 1
	// The action validator should still reject it since it's technically valid for the role
	// but the game should resolve with no kill
	_ = effects

	// Force timeout to resolve night
	gs, _ = Reduce(gs, TimeoutEvent{Phase: PhaseNight})

	// No one should have died
	for _, p := range gs.Players {
		if !p.Alive {
			t.Errorf("player %s should be alive on Night 1 with FirstNightKill=false", p.Username)
		}
	}
}

func TestNominationSystem(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NominationSystem = true
	cfg.AllowLastWords = false
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseDiscussion
	gs.Nominations = make(map[PlayerID]*Nomination)
	gs.Players[1] = &Player{ID: 1, Username: "p1", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "p2", Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "p3", Role: RoleVillager, Alive: true}

	// Player 2 nominates Player 1
	gs, _ = Reduce(gs, NominateEvent{NominatorID: 2, TargetID: 1})
	if gs.Phase != PhaseNomination {
		t.Errorf("expected nomination phase, got %s", gs.Phase)
	}

	// Player 3 seconds
	gs, _ = Reduce(gs, SecondEvent{PlayerID: 3, NominationTarget: 1})
	if gs.Phase != PhaseVoting {
		t.Errorf("expected voting phase after second, got %s", gs.Phase)
	}
}

func TestDisconnectVoidsGame(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseNight
	gs.DayNumber = 1
	gs.Players[1] = &Player{ID: 1, Username: "mafia", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "v1", Role: RoleVillager, Alive: true}
	gs.NightActions = make(map[PlayerID]NightAction)

	gs, _ = Reduce(gs, PlayerDisconnectedEvent{PlayerID: 1})
	gs, _ = Reduce(gs, PlayerDisconnectedEvent{PlayerID: 2})

	if gs.Phase != PhaseGameOver {
		t.Error("game should be over when all players disconnect")
	}
}

func TestHostTransfer(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Players[1] = &Player{ID: 1, Username: "host", Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "newhost", Alive: true}

	gs, _ = Reduce(gs, HostTransferEvent{FromPlayerID: 1, ToPlayerID: 2})
	if gs.HostID != 2 {
		t.Errorf("host should be 2, got %d", gs.HostID)
	}
}

func TestHostTransferNonHost(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Players[1] = &Player{ID: 1, Username: "host", Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "other", Alive: true}

	gs, _ = Reduce(gs, HostTransferEvent{FromPlayerID: 2, ToPlayerID: 1})
	if gs.HostID != 1 {
		t.Error("non-host should not be able to transfer")
	}
}

func TestKickPlayer(t *testing.T) {
	cfg := DefaultConfig()
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseDiscussion
	gs.Players[1] = &Player{ID: 1, Username: "host", Role: RoleVillager, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "afk", Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "mafia", Role: RoleMafia, Alive: true}

	gs, _ = Reduce(gs, KickEvent{HostID: 1, TargetID: 2})
	if gs.Players[2].Alive {
		t.Error("kicked player should be dead")
	}
}

func TestRoleDeliveryFailed(t *testing.T) {
	cfg := DefaultConfig()
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseRoleAssign
	gs.RosterLocked = true
	for i := PlayerID(1); i <= 6; i++ {
		gs.Players[i] = &Player{ID: i, Username: fmt.Sprintf("p%d", i), Alive: true, Role: RoleVillager}
	}
	gs.Players[1].Role = RoleMafia

	gs, _ = Reduce(gs, RoleDeliveryFailedEvent{PlayerID: 6})
	if _, exists := gs.Players[6]; exists {
		t.Error("player 6 should be removed")
	}
	if len(gs.Players) != 5 {
		t.Errorf("expected 5 players, got %d", len(gs.Players))
	}
}

func TestValidateConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := ValidateConfig(cfg); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}

	bad := cfg
	bad.MinPlayers = 2
	if err := ValidateConfig(bad); err == nil {
		t.Error("MinPlayers=2 should be invalid")
	}

	bad2 := cfg
	bad2.MafiaRatioDivisor = 0
	if err := ValidateConfig(bad2); err == nil {
		t.Error("MafiaRatioDivisor=0 should be invalid")
	}
}

func TestAccusation(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseDiscussion
	gs.Accusations = make(map[PlayerID][]PlayerID)
	gs.Players[1] = &Player{ID: 1, Username: "p1", Role: RoleVillager, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "p2", Role: RoleMafia, Alive: true}

	gs, effects := Reduce(gs, AccuseEvent{AccuserID: 1, TargetID: 2})
	if len(gs.Accusations[2]) != 1 {
		t.Error("accusation should be recorded")
	}
	if len(effects) == 0 {
		t.Error("expected group message effect")
	}

	// Duplicate accusation
	gs, _ = Reduce(gs, AccuseEvent{AccuserID: 1, TargetID: 2})
	if len(gs.Accusations[2]) != 1 {
		t.Error("duplicate accusation should be blocked")
	}
}

func TestDefend(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseDiscussion
	gs.DefenseUsed = make(map[PlayerID]bool)
	gs.Players[1] = &Player{ID: 1, Username: "accused", Role: RoleMafia, Alive: true}

	gs, effects := Reduce(gs, DefendEvent{PlayerID: 1, Statement: "I am innocent!"})
	if !gs.DefenseUsed[1] {
		t.Error("defense should be marked as used")
	}
	if len(effects) == 0 {
		t.Error("expected defense message")
	}

	// Can't defend twice
	gs, effects = Reduce(gs, DefendEvent{PlayerID: 1, Statement: "Again!"})
	if len(effects) != 1 {
		t.Error("expected rejection message for second defense")
	}
}

func TestWhisper(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseDiscussion
	gs.Players[1] = &Player{ID: 1, Username: "sender", Role: RoleVillager, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "receiver", Role: RoleVillager, Alive: true}

	gs, effects := Reduce(gs, WhisperEvent{FromID: 1, ToID: 2, Message: "trust me"})
	if len(gs.Whispers) != 1 {
		t.Error("whisper should be logged")
	}
	// Should have 3 effects: DM to receiver, DM to sender confirmation, group notification
	if len(effects) != 3 {
		t.Errorf("expected 3 effects, got %d", len(effects))
	}
}

func TestWhisperOutsideDiscussion(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseVoting
	gs.Players[1] = &Player{ID: 1, Username: "sender", Role: RoleVillager, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "receiver", Role: RoleVillager, Alive: true}

	gs, effects := Reduce(gs, WhisperEvent{FromID: 1, ToID: 2, Message: "hello"})
	if len(gs.Whispers) != 0 {
		t.Error("whisper should not work outside discussion")
	}
	if len(effects) != 1 {
		t.Error("expected rejection DM")
	}
}

func TestPlayerSpokeTracking(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseDiscussion
	gs.SpeakCount = make(map[PlayerID]int)
	gs.Players[1] = &Player{ID: 1, Username: "talker", Role: RoleVillager, Alive: true}

	gs, _ = Reduce(gs, PlayerSpokeEvent{PlayerID: 1})
	gs, _ = Reduce(gs, PlayerSpokeEvent{PlayerID: 1})
	if gs.SpeakCount[1] != 2 {
		t.Errorf("expected speak count 2, got %d", gs.SpeakCount[1])
	}
}
