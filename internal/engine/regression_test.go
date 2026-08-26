package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// driver mirrors what the transport layer does with effects: it feeds
// RolesDeliveredEffect back in as an event. Without that loop the game cannot
// leave role assignment, which is exactly what F1 was.
type driver struct {
	t  *testing.T
	gs *GameState
}

func (d *driver) send(ev Event) []SideEffect {
	d.t.Helper()
	var all []SideEffect
	queue := []Event{ev}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		var effects []SideEffect
		d.gs, effects = Reduce(d.gs, next)
		all = append(all, effects...)
		for _, eff := range effects {
			if _, ok := eff.(RolesDeliveredEffect); ok {
				queue = append(queue, RolesDeliveredEvent{})
			}
		}
	}
	return all
}

func newDriver(t *testing.T, cfg GameConfig, n int) *driver {
	t.Helper()
	gs := NewGameState("test", 123, 1, cfg)
	base := time.Now()
	for i := PlayerID(1); i <= PlayerID(n); i++ {
		gs.Players[i] = &Player{
			ID:       i,
			Username: fmt.Sprintf("player%d", i),
			Alive:    true,
			JoinedAt: base.Add(time.Duration(i) * time.Millisecond),
		}
	}
	return &driver{t: t, gs: gs}
}

func groupTexts(effects []SideEffect) []string {
	var out []string
	for _, eff := range effects {
		if g, ok := eff.(SendGroupEffect); ok {
			out = append(out, g.Text)
		}
	}
	return out
}

func hasTimerFor(effects []SideEffect, phase Phase) bool {
	for _, eff := range effects {
		if t, ok := eff.(SetTimerEffect); ok && t.Phase == phase {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// F1 — role assignment must hand off to Night 1
// ---------------------------------------------------------------------------

func TestF1RoleAssignReachesNight(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 7)
	d.send(GameCreatedEvent{})

	effects := d.send(BeginEvent{PlayerID: 1})

	if d.gs.Phase != PhaseNight {
		t.Fatalf("expected night after roles were delivered, got %s", d.gs.Phase)
	}
	if d.gs.DayNumber != 1 {
		t.Errorf("expected day 1, got %d", d.gs.DayNumber)
	}
	if !hasTimerFor(effects, PhaseNight) {
		t.Error("night phase was entered without a timer")
	}

	// Role DMs are a distinct effect so the transport can wait for real
	// delivery before closing the phase.
	roleDMs := map[PlayerID]bool{}
	for _, eff := range effects {
		if dm, ok := eff.(SendRoleDMEffect); ok {
			roleDMs[dm.PlayerID] = true
		}
	}
	if len(roleDMs) != 7 {
		t.Errorf("expected a tracked role DM per player, got %d", len(roleDMs))
	}
}

func TestF1RoleAssignHasBackstopTimer(t *testing.T) {
	// Even if the acknowledgement never comes back, the phase must time out.
	gs := NewGameState("test", 123, 1, DefaultConfig())
	base := time.Now()
	for i := PlayerID(1); i <= 7; i++ {
		gs.Players[i] = &Player{ID: i, Username: fmt.Sprintf("p%d", i), Alive: true, JoinedAt: base}
	}

	gs, effects := Reduce(gs, BeginEvent{PlayerID: 1})
	if !hasTimerFor(effects, PhaseRoleAssign) {
		t.Fatal("role assignment armed no backstop timer")
	}

	gs, _ = Reduce(gs, TimeoutEvent{Phase: PhaseRoleAssign})
	if gs.Phase != PhaseNight {
		t.Errorf("role_assign timeout should start the night, got %s", gs.Phase)
	}
}

// A full game must actually finish. This is the guard the old suite lacked.
func TestFullGameReachesAWinner(t *testing.T) {
	cfg := DefaultConfig()
	d := newDriver(t, cfg, 9)
	d.send(GameCreatedEvent{})
	d.send(BeginEvent{PlayerID: 1})

	for step := 0; step < 400 && d.gs.Phase != PhaseGameOver; step++ {
		switch d.gs.Phase {
		case PhaseNight:
			d.playNight()
		case PhaseDiscussion, PhaseNomination:
			d.send(TimeoutEvent{Phase: d.gs.Phase})
		case PhaseVoting:
			d.playVote()
		case PhaseLastWords:
			d.send(TimeoutEvent{Phase: PhaseLastWords})
		default:
			t.Fatalf("game parked in unexpected phase %s", d.gs.Phase)
		}
	}

	if d.gs.Phase != PhaseGameOver {
		t.Fatalf("game never finished; stuck in %s on day %d", d.gs.Phase, d.gs.DayNumber)
	}
	if d.gs.Winner == nil {
		t.Fatal("game over with no winner recorded")
	}
	t.Logf("finished on day %d: %s", d.gs.DayNumber, d.gs.Winner.Description)
}

func (d *driver) playNight() {
	acted := false
	for _, p := range playersByID(d.gs) {
		if d.gs.Phase != PhaseNight {
			return
		}
		if !p.CanAct() {
			continue
		}
		if _, done := d.gs.NightActions[p.ID]; done {
			continue
		}
		var kind string
		switch p.Role {
		case RoleMafia, RoleGodfather:
			if d.gs.DayNumber == 1 && !d.gs.Config.FirstNightKill {
				continue
			}
			kind = ActionMafiaKill
		case RoleDetective:
			kind = ActionDetectiveCheck
		case RoleDoctor:
			kind = ActionDoctorProtect
		case RoleVigilante:
			if p.UsedAbility {
				continue
			}
			kind = ActionVigilanteKill
		default:
			continue
		}
		target := d.pickTarget(p, kind)
		if target == 0 {
			continue
		}
		d.send(NightActionEvent{Action: NightAction{ActorID: p.ID, Kind: kind, TargetID: target}})
		acted = true
	}
	if !acted && d.gs.Phase == PhaseNight {
		d.send(TimeoutEvent{Phase: PhaseNight})
	}
}

func (d *driver) pickTarget(actor *Player, kind string) PlayerID {
	for _, p := range playersByID(d.gs) {
		if !p.Alive || p.ID == actor.ID {
			continue
		}
		if kind == ActionMafiaKill && RoleTeam(p.Role) == TeamMafia {
			continue
		}
		return p.ID
	}
	return 0
}

func (d *driver) playVote() {
	var target PlayerID
	for _, p := range playersByID(d.gs) {
		if p.Alive {
			target = p.ID
			break
		}
	}
	if target == 0 {
		d.send(TimeoutEvent{Phase: PhaseVoting})
		return
	}
	for _, p := range playersByID(d.gs) {
		if d.gs.Phase != PhaseVoting {
			return
		}
		if !p.CanAct() || p.ID == target {
			continue
		}
		d.send(VoteEvent{Vote: Vote{VoterID: p.ID, TargetID: target}})
	}
	if d.gs.Phase == PhaseVoting {
		d.send(TimeoutEvent{Phase: PhaseVoting})
	}
}

// ---------------------------------------------------------------------------
// F2 / F3 — the lobby is not a game, so nobody can "win" it
// ---------------------------------------------------------------------------

func TestF2DisconnectInLobbyDoesNotEndGame(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 6)
	d.send(GameCreatedEvent{})

	d.send(PlayerDisconnectedEvent{PlayerID: 4})

	if d.gs.Phase != PhaseLobby {
		t.Fatalf("lobby should survive a player leaving, got phase %s", d.gs.Phase)
	}
	if d.gs.Winner != nil {
		t.Errorf("no winner should be declared in the lobby, got %q", d.gs.Winner.Description)
	}
	if _, still := d.gs.Players[4]; still {
		t.Error("the departing player should be removed from the roster, not left as a ghost")
	}
	if len(d.gs.Players) != 5 {
		t.Errorf("expected 5 players remaining, got %d", len(d.gs.Players))
	}
}

func TestF2LobbyEmptiesToIdle(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 2)
	d.send(GameCreatedEvent{})
	d.send(PlayerDisconnectedEvent{PlayerID: 1})
	d.send(PlayerDisconnectedEvent{PlayerID: 2})

	if d.gs.Phase != PhaseIdle {
		t.Errorf("an empty lobby should close as idle, got %s", d.gs.Phase)
	}
	if d.gs.Winner != nil {
		t.Error("an empty lobby has no winner")
	}
}

func TestF3KickInLobbyRemovesPlayer(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 6)
	d.send(GameCreatedEvent{})

	d.send(KickEvent{HostID: 1, TargetID: 3})

	if d.gs.Phase != PhaseLobby {
		t.Fatalf("kicking in the lobby should not end the game, got %s", d.gs.Phase)
	}
	if _, still := d.gs.Players[3]; still {
		t.Error("kicked lobby player should be deleted from the roster")
	}
	if len(d.gs.AlivePlayers()) != len(d.gs.Players) {
		t.Error("lobby roster should contain no dead players")
	}
}

func TestF3KickHostReassignsHost(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 4)
	d.send(GameCreatedEvent{})
	d.send(KickEvent{HostID: 1, TargetID: 1, IsAdmin: true})

	if d.gs.HostID == 1 {
		t.Error("host should be reassigned after being removed")
	}
	if _, ok := d.gs.Players[d.gs.HostID]; !ok {
		t.Error("new host must be a player still in the lobby")
	}
}

func TestF3KickAllowsGroupAdmin(t *testing.T) {
	cfg := DefaultConfig()
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseDiscussion
	gs.Players[1] = &Player{ID: 1, Username: "host", Role: RoleVillager, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "admin", Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "afk", Role: RoleVillager, Alive: true}
	gs.Players[4] = &Player{ID: 4, Username: "mafia", Role: RoleMafia, Alive: true}

	gs, _ = Reduce(gs, KickEvent{HostID: 2, TargetID: 3, IsAdmin: true})
	if gs.Players[3].Alive {
		t.Error("a group admin should be able to kick")
	}
}

// ---------------------------------------------------------------------------
// F4 — the nomination window needs its own clock
// ---------------------------------------------------------------------------

func TestF4NominationArmsItsOwnTimer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NominationSystem = true
	d := newDriver(t, cfg, 5)
	d.gs.Phase = PhaseDiscussion
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia

	effects := d.send(NominateEvent{NominatorID: 2, TargetID: 3})

	if d.gs.Phase != PhaseNomination {
		t.Fatalf("expected nomination phase, got %s", d.gs.Phase)
	}
	if !hasTimerFor(effects, PhaseNomination) {
		t.Fatal("nomination phase was entered with no timer armed")
	}
	if d.gs.PhaseDeadline.IsZero() {
		t.Error("nomination phase has no deadline, so a restart could not restore it")
	}
}

func TestF4UnsecondedNominationEndsTheDay(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NominationSystem = true
	d := newDriver(t, cfg, 5)
	d.gs.Phase = PhaseDiscussion
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia

	d.send(NominateEvent{NominatorID: 2, TargetID: 3})
	d.send(TimeoutEvent{Phase: PhaseNomination})

	if d.gs.Phase != PhaseNight {
		t.Errorf("an unseconded nomination should end the day, got %s", d.gs.Phase)
	}
}

// ---------------------------------------------------------------------------
// F5 — Markdown injection through names and free text
// ---------------------------------------------------------------------------

func TestF5EscapeMD(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"cool_user":   `cool\_user`,
		"a*b":         `a\*b`,
		"back`tick":   "back\\`tick",
		"link[text":   `link\[text`,
		"_all_*of*it": `\_all\_\*of\*it`,
		// The escape character itself must be escaped, otherwise `a\_b`
		// becomes `a\\_b` and leaves the underscore opening an entity.
		`a\_b`:  `a\\\_b`,
		`back\`: `back\\`,
	}
	for in, want := range cases {
		if got := EscapeMD(in); got != want {
			t.Errorf("EscapeMD(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestF5UsernamesAreEscapedInMessages(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseVoting
	gs.Players[1] = &Player{ID: 1, Username: "mafia_boss", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "town_folk", Role: RoleVillager, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "third_wheel", Role: RoleVillager, Alive: true}

	_, effects := Reduce(gs, VoteEvent{Vote: Vote{VoterID: 2, TargetID: 1}})

	joined := strings.Join(groupTexts(effects), "\n")
	if joined == "" {
		t.Fatal("expected a group announcement")
	}
	if strings.Contains(joined, "town_folk") {
		t.Errorf("underscore in username was not escaped: %q", joined)
	}
	if !strings.Contains(joined, `town\_folk`) {
		t.Errorf("expected escaped username in output: %q", joined)
	}
}

func TestF5UserTextIsEscaped(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseDiscussion
	gs.Players[1] = &Player{ID: 1, Username: "accused", Role: RoleMafia, Alive: true}

	_, effects := Reduce(gs, DefendEvent{PlayerID: 1, Statement: "I am *not* the _mafia_"})
	joined := strings.Join(groupTexts(effects), "\n")
	if strings.Contains(joined, "*not*") || strings.Contains(joined, "_mafia_") {
		t.Errorf("user-supplied markup was not escaped: %q", joined)
	}
}

func TestF5TruncateDoesNotSplitRunes(t *testing.T) {
	// Byte slicing here would emit invalid UTF-8, which Telegram rejects.
	got := TruncateRunes(strings.Repeat("é", 10), 4)
	if got != "éééé..." {
		t.Errorf("got %q", got)
	}
	if !isValidUTF8(got) {
		t.Error("truncation produced invalid UTF-8")
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// F6 — snapshots must be independent of the live state
// ---------------------------------------------------------------------------

func TestF6CloneIsDeep(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseVoting
	gs.Players[1] = &Player{ID: 1, Username: "a", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "b", Role: RoleVillager, Alive: true}
	gs.Votes[1] = Vote{VoterID: 1, TargetID: 2}
	gs.Accusations[2] = []PlayerID{1}
	gs.Whispers = []Whisper{{FromID: 1, ToID: 2, Message: "hi"}}
	gs.Nominations[2] = &Nomination{NominatorID: 1, TargetID: 2}
	trial := PlayerID(2)
	gs.ActiveTrial = &trial
	gs.AppendLog("test", nil)

	snap := gs.Clone()

	gs.Players[1].Alive = false
	gs.Players[3] = &Player{ID: 3, Username: "c", Alive: true}
	gs.Votes[2] = Vote{VoterID: 2, TargetID: 1}
	gs.Accusations[2] = append(gs.Accusations[2], 3)
	gs.Whispers[0].Message = "changed"
	gs.Nominations[2].SecondedBy = 3
	*gs.ActiveTrial = 99
	gs.AppendLog("second", nil)

	if !snap.Players[1].Alive {
		t.Error("clone shares Player pointers with the original")
	}
	if len(snap.Players) != 2 {
		t.Error("clone shares the Players map")
	}
	if len(snap.Votes) != 1 {
		t.Error("clone shares the Votes map")
	}
	if len(snap.Accusations[2]) != 1 {
		t.Error("clone shares an Accusations slice")
	}
	if snap.Whispers[0].Message != "hi" {
		t.Error("clone shares the Whispers backing array")
	}
	if snap.Nominations[2].SecondedBy != 0 {
		t.Error("clone shares Nomination pointers")
	}
	if *snap.ActiveTrial != 2 {
		t.Error("clone shares the ActiveTrial pointer")
	}
	if len(snap.Log) != 1 {
		t.Error("clone shares the Log backing array")
	}
}

func TestF6CloneCopiesLogPayloads(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.AppendLog("vote_cast", map[string]interface{}{"voter": PlayerID(1)})

	snap := gs.Clone()
	// The persist goroutine serialises this map while the actor keeps going.
	gs.Log[0].Payload["voter"] = PlayerID(99)

	if snap.Log[0].Payload["voter"] != PlayerID(1) {
		t.Error("clone shares log payload maps with the live state")
	}
}

// ---------------------------------------------------------------------------
// F7 / F12 — who gets to decide a lynch
// ---------------------------------------------------------------------------

func TestF7SingleVoteCannotLynch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false
	d := newDriver(t, cfg, 8)
	d.gs.Phase = PhaseVoting
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia

	d.send(VoteEvent{Vote: Vote{VoterID: 1, TargetID: 2}})
	d.send(TimeoutEvent{Phase: PhaseVoting})

	if !d.gs.Players[2].Alive {
		t.Error("one vote out of eight must not be enough to execute somebody")
	}
}

func TestF7MajorityLynches(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false
	d := newDriver(t, cfg, 8)
	d.gs.Phase = PhaseVoting
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia

	if got := lynchThreshold(d.gs); got != 5 {
		t.Fatalf("threshold for 8 eligible voters = %d, want 5", got)
	}
	for v := PlayerID(3); v <= 7; v++ {
		d.send(VoteEvent{Vote: Vote{VoterID: v, TargetID: 2}})
	}
	d.send(TimeoutEvent{Phase: PhaseVoting})

	if d.gs.Players[2].Alive {
		t.Error("a clear majority should execute the target")
	}
}

func TestF12DisconnectedExcludedFromQuorum(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false
	cfg.LynchRequiresMajority = false
	d := newDriver(t, cfg, 4)
	d.gs.Phase = PhaseVoting
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia
	d.gs.Players[3].Disconnected = true

	if got := d.gs.EligibleVoterCount(); got != 3 {
		t.Fatalf("eligible voters = %d, want 3 (one player is disconnected)", got)
	}

	d.send(VoteEvent{Vote: Vote{VoterID: 1, TargetID: 2}})
	d.send(VoteEvent{Vote: Vote{VoterID: 2, TargetID: 1}})
	d.send(VoteEvent{Vote: Vote{VoterID: 4, TargetID: 1}})

	if d.gs.Phase == PhaseVoting {
		t.Error("voting should resolve once everyone who can vote has voted")
	}
}

func TestF12DisconnectResolvesPendingVote(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false
	cfg.LynchRequiresMajority = false
	d := newDriver(t, cfg, 4)
	d.gs.Phase = PhaseVoting
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia

	d.send(VoteEvent{Vote: Vote{VoterID: 1, TargetID: 2}})
	d.send(VoteEvent{Vote: Vote{VoterID: 2, TargetID: 1}})
	d.send(VoteEvent{Vote: Vote{VoterID: 4, TargetID: 1}})
	if d.gs.Phase != PhaseVoting {
		t.Fatal("expected to still be waiting on player 3")
	}

	// The player everyone was waiting on drops out.
	d.send(PlayerDisconnectedEvent{PlayerID: 3})
	if d.gs.Phase == PhaseVoting {
		t.Error("losing the last outstanding voter should resolve the vote")
	}
}

func TestF7TieDoesNotLynch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false
	cfg.LynchRequiresMajority = false
	d := newDriver(t, cfg, 4)
	d.gs.Phase = PhaseVoting
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia

	d.send(VoteEvent{Vote: Vote{VoterID: 1, TargetID: 2}})
	d.send(VoteEvent{Vote: Vote{VoterID: 2, TargetID: 1}})
	d.send(TimeoutEvent{Phase: PhaseVoting})

	if !d.gs.Players[1].Alive || !d.gs.Players[2].Alive {
		t.Error("a tied vote must not execute anybody")
	}
}

// ---------------------------------------------------------------------------
// F8 / F14 — timers survive a restart, and the lobby has one at all
// ---------------------------------------------------------------------------

func TestF14LobbyIsTimed(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 3)
	effects := d.send(GameCreatedEvent{})

	if !hasTimerFor(effects, PhaseLobby) {
		t.Fatal("the lobby was created without a countdown")
	}
	if d.gs.PhaseDeadline.IsZero() {
		t.Error("lobby has no deadline, so a restart could not restore its timer")
	}
}

func TestF14LobbyTimeoutCancelsWhenShort(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 3)
	d.send(GameCreatedEvent{})
	d.send(TimeoutEvent{Phase: PhaseLobby})

	if d.gs.Phase != PhaseIdle {
		t.Errorf("an under-filled lobby should be cancelled, got %s", d.gs.Phase)
	}
}

func TestF14LobbyTimeoutAutoStartsWhenFull(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 7)
	d.send(GameCreatedEvent{})
	d.send(TimeoutEvent{Phase: PhaseLobby})

	if d.gs.Phase != PhaseNight {
		t.Errorf("a full lobby should start the game on timeout, got %s", d.gs.Phase)
	}
}

func TestF8ResumeRearmsRemainingTime(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 7)
	d.send(GameCreatedEvent{})
	d.send(BeginEvent{PlayerID: 1})
	if d.gs.Phase != PhaseNight {
		t.Fatalf("setup failed, phase = %s", d.gs.Phase)
	}

	// Simulate a restart 30 seconds into the night.
	d.gs.PhaseDeadline = time.Now().Add(30 * time.Second)
	effects := d.send(ResumeEvent{})

	var armed *SetTimerEffect
	for i := range effects {
		if timer, ok := effects[i].(SetTimerEffect); ok {
			armed = &timer
		}
	}
	if armed == nil {
		t.Fatal("resume armed no timer, so the restored game would hang")
	}
	if armed.Phase != PhaseNight {
		t.Errorf("resume armed a timer for %s, want night", armed.Phase)
	}
	if armed.Duration > 31*time.Second || armed.Duration < 25*time.Second {
		t.Errorf("resume timer duration %v should be close to the 30s remaining", armed.Duration)
	}

	// Players need their action prompts back too.
	prompts := 0
	for _, eff := range effects {
		if _, ok := eff.(SendNightActionEffect); ok {
			prompts++
		}
	}
	if prompts == 0 {
		t.Error("resume did not re-send any night action prompts")
	}
}

func TestF8ResumePastDeadlineAdvances(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 7)
	d.send(GameCreatedEvent{})
	d.send(BeginEvent{PlayerID: 1})

	d.gs.PhaseDeadline = time.Now().Add(-time.Minute)
	d.send(ResumeEvent{})

	if d.gs.Phase == PhaseNight {
		t.Error("a night whose deadline already passed should resolve on resume")
	}
}

func TestF8EveryLivePhaseHasADeadline(t *testing.T) {
	// A phase with no deadline cannot be restored after a restart.
	d := newDriver(t, DefaultConfig(), 9)
	d.send(GameCreatedEvent{})
	d.send(BeginEvent{PlayerID: 1})

	for step := 0; step < 200 && d.gs.Phase != PhaseGameOver; step++ {
		if !d.gs.Phase.IsTerminal() && d.gs.PhaseDeadline.IsZero() {
			t.Fatalf("phase %s has no deadline", d.gs.Phase)
		}
		switch d.gs.Phase {
		case PhaseNight:
			d.playNight()
		case PhaseDiscussion, PhaseNomination:
			d.send(TimeoutEvent{Phase: d.gs.Phase})
		case PhaseVoting:
			d.playVote()
		case PhaseLastWords:
			d.send(TimeoutEvent{Phase: PhaseLastWords})
		default:
			t.Fatalf("unexpected phase %s", d.gs.Phase)
		}
	}
}

// ---------------------------------------------------------------------------
// F9 — an undelivered role must redistribute, not linger
// ---------------------------------------------------------------------------

func TestF9RoleDeliveryFailureRedeals(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 7)
	d.send(GameCreatedEvent{})

	// Stop at role assignment by reducing Begin without the delivery ack.
	gs, _ := Reduce(d.gs, BeginEvent{PlayerID: 1})
	d.gs = gs
	if d.gs.Phase != PhaseRoleAssign {
		t.Fatalf("expected role_assign, got %s", d.gs.Phase)
	}
	originalRole := d.gs.Players[2].Role

	// The failure path must both redeal and keep the game moving.
	d.send(RoleDeliveryFailedEvent{PlayerID: 7})

	if _, still := d.gs.Players[7]; still {
		t.Error("player whose DM failed should be removed")
	}
	if d.gs.Phase != PhaseNight {
		t.Errorf("game should continue to night after redealing, got %s", d.gs.Phase)
	}
	for id, p := range d.gs.Players {
		if p.Role == RoleUnassigned {
			t.Errorf("player %d has no role after the redeal", id)
		}
	}
	_ = originalRole
}

func TestF9RoleDeliveryFailureBelowMinimumReturnsToLobby(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 5)
	d.send(GameCreatedEvent{})
	gs, _ := Reduce(d.gs, BeginEvent{PlayerID: 1})
	d.gs = gs

	effects := d.send(RoleDeliveryFailedEvent{PlayerID: 5})

	if d.gs.Phase != PhaseLobby {
		t.Fatalf("dropping below the minimum should return to the lobby, got %s", d.gs.Phase)
	}
	if !hasTimerFor(effects, PhaseLobby) {
		t.Error("returning to the lobby must re-arm the lobby timer")
	}
	for id, p := range d.gs.Players {
		if p.Role != RoleUnassigned {
			t.Errorf("player %d kept a stale role after returning to the lobby", id)
		}
	}
	if d.gs.RosterLocked {
		t.Error("roster should be unlocked back in the lobby")
	}
}

// ---------------------------------------------------------------------------
// F13 — simultaneous vs sequential night resolution
// ---------------------------------------------------------------------------

func nightScenario(t *testing.T, simultaneous bool) *GameState {
	t.Helper()
	cfg := DefaultConfig()
	cfg.SimultaneousNightActions = simultaneous
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseNight
	gs.DayNumber = 2
	gs.Players[1] = &Player{ID: 1, Username: "mafia", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "vigi", Role: RoleVigilante, Alive: true}
	gs.Players[3] = &Player{ID: 3, Username: "det", Role: RoleDetective, Alive: true}
	for i := PlayerID(4); i <= 8; i++ {
		gs.Players[i] = &Player{ID: i, Username: fmt.Sprintf("v%d", i), Role: RoleVillager, Alive: true}
	}
	gs.NightActions = map[PlayerID]NightAction{
		1: {ActorID: 1, Kind: ActionMafiaKill, TargetID: 2},      // mafia kills the vigilante
		2: {ActorID: 2, Kind: ActionVigilanteKill, TargetID: 4},  // vigilante shoots
		3: {ActorID: 3, Kind: ActionDetectiveCheck, TargetID: 1}, // detective investigates
	}
	return gs
}

func TestF13SimultaneousNightKeepsDyingActorsShot(t *testing.T) {
	gs, _ := resolveNight(nightScenario(t, true))
	if gs.Players[4].Alive {
		t.Error("with simultaneous resolution the dying vigilante's shot should still land")
	}
	if len(gs.LastNightDeaths) != 2 {
		t.Errorf("expected 2 deaths, got %d", len(gs.LastNightDeaths))
	}
}

func TestF13SequentialNightCancelsDeadActors(t *testing.T) {
	gs, effects := resolveNight(nightScenario(t, false))
	if !gs.Players[4].Alive {
		t.Error("with sequential resolution a dead vigilante should not shoot")
	}
	if len(gs.LastNightDeaths) != 1 {
		t.Errorf("expected only the vigilante to die, got %d deaths", len(gs.LastNightDeaths))
	}
	// The detective survived here, so they still get their result.
	dms := 0
	for _, eff := range effects {
		if dm, ok := eff.(SendDMEffect); ok && dm.PlayerID == 3 {
			dms++
		}
	}
	if dms == 0 {
		t.Error("a surviving detective should receive their investigation result")
	}
}

func TestF13DeadDetectiveGetsNoDM(t *testing.T) {
	cfg := DefaultConfig()
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseNight
	gs.DayNumber = 2
	gs.Players[1] = &Player{ID: 1, Username: "mafia", Role: RoleMafia, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "det", Role: RoleDetective, Alive: true}
	for i := PlayerID(3); i <= 7; i++ {
		gs.Players[i] = &Player{ID: i, Username: fmt.Sprintf("v%d", i), Role: RoleVillager, Alive: true}
	}
	gs.NightActions = map[PlayerID]NightAction{
		1: {ActorID: 1, Kind: ActionMafiaKill, TargetID: 2},
		2: {ActorID: 2, Kind: ActionDetectiveCheck, TargetID: 1},
	}

	_, effects := resolveNight(gs)
	for _, eff := range effects {
		if dm, ok := eff.(SendDMEffect); ok && dm.PlayerID == 2 {
			t.Errorf("a detective killed that night should not be DMed a result: %q", dm.Text)
		}
	}
}

func TestF13NightResolutionIsDeterministic(t *testing.T) {
	// Map iteration order must not influence the outcome.
	first := ""
	for i := 0; i < 50; i++ {
		gs, _ := resolveNight(nightScenario(t, true))
		got := fmt.Sprint(gs.LastNightDeaths)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("night resolution is order-dependent: %q vs %q", first, got)
		}
	}
}

// A night where nobody has anything to submit should resolve straight away
// without dropping its own announcement.
func TestNightWithNoActionsStillAnnounces(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FirstNightKill = false
	gs := NewGameState("test", 123, 1, cfg)
	gs.Players[1] = &Player{ID: 1, Username: "mafia", Role: RoleMafia, Alive: true}
	for i := PlayerID(2); i <= 6; i++ {
		gs.Players[i] = &Player{ID: i, Username: fmt.Sprintf("v%d", i), Role: RoleVillager, Alive: true}
	}

	gs, effects := transitionToNight(gs)

	if gs.Phase != PhaseDiscussion {
		t.Errorf("a night with no possible actions should resolve immediately, got %s", gs.Phase)
	}
	joined := strings.Join(groupTexts(effects), "\n")
	if !strings.Contains(joined, "Night 1") {
		t.Errorf("the night announcement was dropped: %q", joined)
	}
	if hasTimerFor(effects, PhaseNight) {
		t.Error("no night timer should be armed for a night that already resolved")
	}
	if !hasTimerFor(effects, PhaseDiscussion) {
		t.Error("the resulting discussion phase needs a timer")
	}
}

func TestGameEndedFromLobbyReportsNoRoles(t *testing.T) {
	d := newDriver(t, DefaultConfig(), 4)
	d.send(GameCreatedEvent{})
	effects := d.send(EndGameEvent{PlayerID: 1})

	joined := strings.Join(groupTexts(effects), "\n")
	if strings.Contains(joined, "(town)") {
		t.Errorf("a lobby has no roles, so no team should be claimed: %q", joined)
	}
	if !strings.Contains(joined, "no role assigned") {
		t.Errorf("expected the summary to say roles were never dealt: %q", joined)
	}
}

// ---------------------------------------------------------------------------
// Eligibility must be consistent: whoever is excluded from a quorum must also
// be prevented from acting, or they can decide a round nobody is waiting on.
// ---------------------------------------------------------------------------

func TestDisconnectedPlayerCannotVote(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false
	d := newDriver(t, cfg, 6)
	d.gs.Phase = PhaseVoting
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia
	d.gs.Players[6].Disconnected = true

	d.send(VoteEvent{Vote: Vote{VoterID: 6, TargetID: 2}})

	if len(d.gs.Votes) != 0 {
		t.Error("a player excluded from the quorum must not be able to vote")
	}
}

func TestDisconnectedPlayerCannotActAtNight(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseNight
	gs.DayNumber = 2
	gs.Players[1] = &Player{ID: 1, Username: "mafia", Role: RoleMafia, Alive: true, Disconnected: true}
	for i := PlayerID(2); i <= 6; i++ {
		gs.Players[i] = &Player{ID: i, Username: fmt.Sprintf("v%d", i), Role: RoleVillager, Alive: true}
	}

	gs, _ = Reduce(gs, NightActionEvent{Action: NightAction{ActorID: 1, Kind: ActionMafiaKill, TargetID: 2}})

	if len(gs.NightActions) != 0 {
		t.Error("a disconnected player's night action must be rejected, since the phase never waits for it")
	}
}

func TestDisconnectDropsAlreadyCastVote(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = false
	cfg.LynchRequiresMajority = false
	d := newDriver(t, cfg, 6)
	d.gs.Phase = PhaseVoting
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia

	d.send(VoteEvent{Vote: Vote{VoterID: 5, TargetID: 2}})
	d.send(PlayerDisconnectedEvent{PlayerID: 5})

	// Leaving the ballot behind would let the tally exceed the eligible count
	// and resolve the vote before everyone had spoken.
	if _, still := d.gs.Votes[5]; still {
		t.Error("a disconnected player's ballot should be withdrawn")
	}
	if d.gs.Phase != PhaseVoting {
		t.Errorf("the vote should still be open for the remaining players, got %s", d.gs.Phase)
	}
}

func TestKickDuringLastWordsCancelsExecution(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowLastWords = true
	d := newDriver(t, cfg, 6)
	d.gs.Phase = PhaseVoting
	for _, p := range d.gs.Players {
		p.Role = RoleVillager
	}
	d.gs.Players[1].Role = RoleMafia
	d.gs.Players[2].Role = RoleJester

	for v := PlayerID(3); v <= 6; v++ {
		d.send(VoteEvent{Vote: Vote{VoterID: v, TargetID: 2}})
	}
	// Four of six is a majority, but not everyone has voted, so the clock
	// closes the ballot.
	d.send(TimeoutEvent{Phase: PhaseVoting})
	if d.gs.Phase != PhaseLastWords {
		t.Fatalf("expected last words, got %s", d.gs.Phase)
	}

	// The host kicks the condemned player mid-speech.
	d.send(KickEvent{HostID: 1, TargetID: 2})
	d.send(TimeoutEvent{Phase: PhaseLastWords})

	if len(d.gs.JesterWon) != 0 {
		t.Error("a kicked Jester was credited with a lynch win they did not earn")
	}
	if d.gs.Phase == PhaseLastWords {
		t.Error("the game should move on rather than stay in last words")
	}
}

// ---------------------------------------------------------------------------
// F15 — the persisted document must stay bounded
// ---------------------------------------------------------------------------

func TestF15LogIsCapped(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	for i := 0; i < maxLogEntries*3; i++ {
		gs.AppendLog("spam", map[string]interface{}{"i": i})
	}
	if len(gs.Log) > maxLogEntries {
		t.Errorf("log grew to %d entries, cap is %d", len(gs.Log), maxLogEntries)
	}
	// The newest entries are the ones worth keeping.
	last := gs.Log[len(gs.Log)-1]
	if last.Payload["i"] != maxLogEntries*3-1 {
		t.Error("log cap dropped the newest entry instead of the oldest")
	}
}

// ---------------------------------------------------------------------------
// F16 — host transfer and role reveal rules
// ---------------------------------------------------------------------------

func TestF16HostTransferRejectsDeadPlayer(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseDiscussion
	gs.Players[1] = &Player{ID: 1, Username: "host", Role: RoleVillager, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "corpse", Role: RoleVillager, Alive: false}

	gs, _ = Reduce(gs, HostTransferEvent{FromPlayerID: 1, ToPlayerID: 2})
	if gs.HostID != 1 {
		t.Error("host must not be transferred to a dead player")
	}
}

func TestF16HostTransferRejectsDisconnected(t *testing.T) {
	gs := NewGameState("test", 123, 1, DefaultConfig())
	gs.Phase = PhaseDiscussion
	gs.Players[1] = &Player{ID: 1, Username: "host", Role: RoleVillager, Alive: true}
	gs.Players[2] = &Player{ID: 2, Username: "gone", Role: RoleVillager, Alive: true, Disconnected: true}

	gs, _ = Reduce(gs, HostTransferEvent{FromPlayerID: 1, ToPlayerID: 2})
	if gs.HostID != 1 {
		t.Error("host must not be transferred to a disconnected player")
	}
}

func TestF16RevealFlagsAreIndependent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RevealOnNightKill = false
	cfg.RevealOnLynch = true
	cfg.AllowLastWords = false

	// Night kill: role stays hidden.
	gs := NewGameState("test", 123, 1, cfg)
	gs.Phase = PhaseNight
	gs.DayNumber = 2
	gs.Players[1] = &Player{ID: 1, Username: "mafia", Role: RoleMafia, Alive: true}
	for i := PlayerID(2); i <= 6; i++ {
		gs.Players[i] = &Player{ID: i, Username: fmt.Sprintf("v%d", i), Role: RoleVillager, Alive: true}
	}
	gs.NightActions = map[PlayerID]NightAction{
		1: {ActorID: 1, Kind: ActionMafiaKill, TargetID: 2},
	}
	gs, effects := resolveNight(gs)
	if gs.Players[2].RoleRevealed {
		t.Error("night victim's role should stay secret when RevealOnNightKill is off")
	}
	if strings.Contains(strings.Join(groupTexts(effects), "\n"), "villager") {
		t.Error("night death announcement leaked the role")
	}

	// Lynch: role is revealed.
	gs.Phase = PhaseVoting
	gs.Votes = map[PlayerID]Vote{}
	gs, effects = executeLynch(gs, 3, 3, nil)
	if !gs.Players[3].RoleRevealed {
		t.Error("lynched player's role should be revealed when RevealOnLynch is on")
	}
	if !strings.Contains(strings.Join(groupTexts(effects), "\n"), "villager") {
		t.Error("lynch announcement should include the role")
	}
}

// ---------------------------------------------------------------------------
// Config validation now covers the timers that used to be missing
// ---------------------------------------------------------------------------

func TestConfigRejectsUntimedPhases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*GameConfig)
	}{
		{"lobby", func(c *GameConfig) { c.LobbyTimeoutSec = 0 }},
		{"role assign", func(c *GameConfig) { c.RoleAssignTimeoutSec = 0 }},
		{"nomination", func(c *GameConfig) { c.NominationTimeoutSec = 0 }},
		{"voting", func(c *GameConfig) { c.VotingTimeoutSec = 0 }},
		{"last words", func(c *GameConfig) { c.LastWordsSec = 0 }},
	} {
		cfg := DefaultConfig()
		tc.mutate(&cfg)
		if err := ValidateConfig(cfg); err == nil {
			t.Errorf("%s with no timeout should be rejected", tc.name)
		}
	}
}
