package engine

import (
	"fmt"
	"time"
)

type GameID string

type PlayerID int64

type Team string

const (
	TeamTown    Team = "town"
	TeamMafia   Team = "mafia"
	TeamNeutral Team = "neutral"
)

type Role string

const (
	RoleVillager  Role = "villager"
	RoleMafia     Role = "mafia"
	RoleDetective Role = "detective"
	RoleDoctor    Role = "doctor"
	RoleGodfather Role = "godfather"
	RoleVigilante Role = "vigilante"
	RoleJester    Role = "jester"
)

func RoleTeam(r Role) Team {
	switch r {
	case RoleMafia, RoleGodfather:
		return TeamMafia
	case RoleJester:
		return TeamNeutral
	default:
		return TeamTown
	}
}

type Phase string

const (
	PhaseIdle         Phase = "idle"
	PhaseLobby        Phase = "lobby"
	PhaseRoleAssign   Phase = "role_assign"
	PhaseNight        Phase = "night"
	PhaseNightResolve Phase = "night_resolve"
	PhaseDayAnnounce  Phase = "day_announce"
	PhaseDiscussion   Phase = "discussion"
	PhaseNomination   Phase = "nomination"  // nomination+second phase (optional)
	PhaseTrial        Phase = "trial"       // accused player defends themselves
	PhaseVoting       Phase = "voting"
	PhaseLastWords    Phase = "last_words"  // lynched player's final statement
	PhaseLynchResolve Phase = "lynch_resolve"
	PhaseGameOver     Phase = "game_over"
)

type Player struct {
	ID               PlayerID
	Username         string
	Role             Role
	Alive            bool
	JoinedAt         time.Time
	ProtectedTonight bool
	UsedAbility      bool
	DMConfirmed      bool
	Disconnected     bool
}

type NightAction struct {
	ActorID     PlayerID
	Kind        string
	TargetID    PlayerID
	SubmittedAt time.Time
}

const (
	ActionMafiaKill      = "mafia_kill"
	ActionDetectiveCheck = "detective_check"
	ActionDoctorProtect  = "doctor_protect"
	ActionVigilanteKill  = "vigilante_kill"
)

type Vote struct {
	VoterID   PlayerID
	TargetID  PlayerID // 0 = no lynch
	Timestamp time.Time
}

const NoLynchTarget PlayerID = 0

type GameConfig struct {
	MinPlayers           int
	MaxPlayers           int
	LobbyTimeoutSec      int
	NightTimeoutSec      int
	DiscussionTimeoutSec int
	VotingTimeoutSec     int
	LastWordsSec         int // time for lynched player to speak last words
	RevealRoleOnDeath    bool
	AllowNoLynch         bool
	MafiaRatioDivisor    int
	SpecialRoleDivisor   int
	DoctorSelfProtect    bool
	FirstNightKill       bool // if false, mafia cannot kill on Night 1 (classic variant)
	NominationSystem     bool // if true, use nominate+second before voting
	AllowLastWords       bool // if true, lynched player gets a brief moment to speak
	OptionalRoles        []RoleDefinition
}

func DefaultConfig() GameConfig {
	return GameConfig{
		MinPlayers:           5,
		MaxPlayers:           20,
		LobbyTimeoutSec:      120,
		NightTimeoutSec:      90,
		DiscussionTimeoutSec: 120,
		VotingTimeoutSec:     60,
		LastWordsSec:         15,
		RevealRoleOnDeath:    true,
		AllowNoLynch:         true,
		MafiaRatioDivisor:    4,
		SpecialRoleDivisor:   3,
		DoctorSelfProtect:    false,
		FirstNightKill:       true,
		NominationSystem:     false,
		AllowLastWords:       true,
		OptionalRoles:        DefaultOptionalRoles(),
	}
}

type RoleDefinition struct {
	Role              Role
	Team              Team
	MinPlayers        int
	Weight            float64
	ReplacesMafiaSlot bool
}

func DefaultOptionalRoles() []RoleDefinition {
	return []RoleDefinition{
		{RoleDetective, TeamTown, 6, 1.0, false},
		{RoleDoctor, TeamTown, 7, 0.9, false},
		{RoleVigilante, TeamTown, 10, 0.4, false},
		{RoleJester, TeamNeutral, 8, 0.3, false},
		{RoleGodfather, TeamMafia, 9, 0.5, true},
	}
}

type GameEvent struct {
	Timestamp time.Time
	Type      string
	Payload   map[string]interface{}
}

type WinResult struct {
	Winner      Team
	JesterWin   *PlayerID
	Description string
}

type GameState struct {
	ID            GameID
	ChatID        int64
	HostID        PlayerID
	Phase         Phase
	DayNumber     int
	Players       map[PlayerID]*Player
	NightActions  map[PlayerID]NightAction
	Votes         map[PlayerID]Vote
	Config        GameConfig
	CreatedAt     time.Time
	PhaseDeadline time.Time
	Log           []GameEvent
	RosterLocked  bool

	// Night resolution results, consumed by day announcement
	LastNightDeaths []PlayerID
	LastCheckResult *CheckResult

	// Nomination system state
	Nominations    map[PlayerID]*Nomination // target -> nomination info
	ActiveTrial    *PlayerID                // player currently on trial (nomination system)

	// Last words
	LastWordsPlayer *PlayerID // player currently giving last words before lynch

	// Win tracking
	Winner    *WinResult
	JesterWon []PlayerID

	// Statistics
	ConsecutiveNoKillNights int
}

type CheckResult struct {
	DetectiveID PlayerID
	TargetID    PlayerID
	ResultTeam  Team
}

type Nomination struct {
	NominatorID PlayerID
	TargetID    PlayerID
	SecondedBy  PlayerID // 0 if not yet seconded
	Time        time.Time
}

func NewGameState(gameID GameID, chatID int64, hostID PlayerID, cfg GameConfig) *GameState {
	return &GameState{
		ID:           gameID,
		ChatID:       chatID,
		HostID:       hostID,
		Phase:        PhaseLobby,
		DayNumber:    0,
		Players:      make(map[PlayerID]*Player),
		NightActions: make(map[PlayerID]NightAction),
		Votes:        make(map[PlayerID]Vote),
		Config:       cfg,
		CreatedAt:    time.Now(),
		Log:          []GameEvent{},
	}
}

func (gs *GameState) AlivePlayers() []*Player {
	var alive []*Player
	for _, p := range gs.Players {
		if p.Alive {
			alive = append(alive, p)
		}
	}
	return alive
}

func (gs *GameState) AlivePlayerIDs() []PlayerID {
	var ids []PlayerID
	for _, p := range gs.Players {
		if p.Alive {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

func (gs *GameState) AliveMafiaCount() int {
	count := 0
	for _, p := range gs.Players {
		if p.Alive && RoleTeam(p.Role) == TeamMafia {
			count++
		}
	}
	return count
}

func (gs *GameState) AliveTownCount() int {
	count := 0
	for _, p := range gs.Players {
		if p.Alive && RoleTeam(p.Role) == TeamTown {
			count++
		}
	}
	return count
}

func (gs *GameState) AliveNeutralCount() int {
	count := 0
	for _, p := range gs.Players {
		if p.Alive && RoleTeam(p.Role) == TeamNeutral {
			count++
		}
	}
	return count
}

// ValidateConfig checks that the game config can produce a winnable game (§8.8)
func ValidateConfig(cfg GameConfig) error {
	if cfg.MinPlayers < 3 {
		return fmt.Errorf("MinPlayers must be at least 3")
	}
	if cfg.MaxPlayers < cfg.MinPlayers {
		return fmt.Errorf("MaxPlayers must be >= MinPlayers")
	}
	if cfg.MafiaRatioDivisor <= 0 {
		return fmt.Errorf("MafiaRatioDivisor must be positive")
	}
	if cfg.SpecialRoleDivisor <= 0 {
		return fmt.Errorf("SpecialRoleDivisor must be positive")
	}
	// Verify that at minimum player count, mafia can eventually win
	minMafia := ComputeMafiaCount(cfg.MinPlayers, cfg.MafiaRatioDivisor)
	if minMafia < 1 {
		return fmt.Errorf("config produces 0 mafia at MinPlayers=%d", cfg.MinPlayers)
	}
	// Verify mafia never starts at parity
	for n := cfg.MinPlayers; n <= cfg.MaxPlayers; n++ {
		mc := ComputeMafiaCount(n, cfg.MafiaRatioDivisor)
		if mc >= (n+1)/2 {
			return fmt.Errorf("mafia starts at parity/majority for n=%d (mafia=%d)", n, mc)
		}
	}
	return nil
}

func (gs *GameState) AppendLog(eventType string, payload map[string]interface{}) {
	gs.Log = append(gs.Log, GameEvent{
		Timestamp: time.Now(),
		Type:      eventType,
		Payload:   payload,
	})
}
