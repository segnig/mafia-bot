package engine

import (
	"fmt"
	"sort"
	"time"
)

type GameID string

type PlayerID int64

type Team string

const (
	TeamTown  Team = "town"
	TeamMafia Team = "mafia"
	// TeamKiller is the lone-wolf killer faction. It is kept separate from
	// neutral because it has a kill and a win condition of its own, so it
	// blocks a town win the same way the mafia does.
	TeamKiller  Team = "killer"
	TeamNeutral Team = "neutral"
)

// TeamLabel is the human-readable faction name used in announcements.
func TeamLabel(t Team) string {
	switch t {
	case TeamTown:
		return "Town"
	case TeamMafia:
		return "Mafia"
	case TeamKiller:
		return "Serial Killer"
	case TeamNeutral:
		return "Neutral"
	}
	return "nobody"
}

type Role string

const (
	RoleUnassigned Role = ""
	RoleVillager   Role = "villager"
	RoleMafia      Role = "mafia"
	RoleDetective  Role = "detective"
	RoleDoctor     Role = "doctor"
	RoleGodfather  Role = "godfather"
	RoleVigilante  Role = "vigilante"
	RoleJester     Role = "jester"

	RoleBodyguard    Role = "bodyguard"
	RoleEscort       Role = "escort"
	RoleFramer       Role = "framer"
	RoleLookout      Role = "lookout"
	RoleMayor        Role = "mayor"
	RoleSurvivor     Role = "survivor"
	RoleSerialKiller Role = "serial_killer"
)

func RoleTeam(r Role) Team {
	return RoleInfoFor(r).Team
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
	PhaseNomination   Phase = "nomination" // nomination+second phase (optional)
	PhaseVoting       Phase = "voting"
	PhaseLastWords    Phase = "last_words" // lynched player's final statement
	PhaseLynchResolve Phase = "lynch_resolve"
	PhaseGameOver     Phase = "game_over"
)

// IsTerminal reports whether the game can no longer progress on its own.
func (p Phase) IsTerminal() bool {
	return p == PhaseGameOver || p == PhaseIdle
}

// IsValid guards against a persisted game whose phase this build no longer
// understands. Such a state has no timeout handler, so it would sit forever.
func (p Phase) IsValid() bool {
	switch p {
	case PhaseIdle, PhaseLobby, PhaseRoleAssign, PhaseNight, PhaseNightResolve,
		PhaseDayAnnounce, PhaseDiscussion, PhaseNomination, PhaseVoting,
		PhaseLastWords, PhaseLynchResolve, PhaseGameOver:
		return true
	}
	return false
}

type Player struct {
	ID       PlayerID
	Username string // Telegram @username; may be empty
	// DisplayName is the fallback shown when a player has no @username.
	DisplayName      string
	Role             Role
	Alive            bool
	JoinedAt         time.Time
	ProtectedTonight bool
	UsedAbility      bool
	DMConfirmed      bool
	Disconnected     bool
	// RoleRevealed is set when this player's role has been made public, so
	// later summaries can echo the reveal without re-deciding the rule.
	RoleRevealed bool

	// BlockedTonight is set by the Escort and clears every night. A blocked
	// player's own night action does not resolve.
	BlockedTonight bool
	// FramedTonight makes an investigation report Mafia for this player.
	FramedTonight bool
	// LoverID links two players: when one dies the other dies of grief.
	LoverID PlayerID
	// ExtraVotes is added to this player's ballot, set when a Mayor reveals.
	ExtraVotes int
	// DiedOnDay records the day number the player was removed, 0 while alive.
	DiedOnDay int
	// DeathCause is how they were removed: mafia, lynch, grief, and so on.
	DeathCause string
	// Stats are the per-game counters that feed the end-of-game awards.
	Stats PlayerGameStats
}

// PlayerGameStats accumulates the small facts that make an end-of-game recap
// interesting. They are scoped to one game and copied into the summary.
type PlayerGameStats struct {
	VotesOnEvil   int // votes cast against a player who really was evil
	VotesCast     int
	Saves         int // kills prevented by this player's protection
	Kills         int // players this player personally killed
	CorrectChecks int // investigations that returned Mafia on a real threat
	Whispers      int
	Messages      int
	Accusations   int
}

// VoteWeight is how many votes this player's ballot is worth.
func (p *Player) VoteWeight() int {
	return 1 + p.ExtraVotes
}

// PlainName is the player's name without any markup escaping. Use it for
// inline keyboard button labels, which Telegram renders literally.
func (p *Player) PlainName() string {
	if p.Username != "" {
		return "@" + p.Username
	}
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return fmt.Sprintf("player %d", p.ID)
}

// Label is the player's name escaped for interpolation into a Markdown
// message body.
func (p *Player) Label() string {
	return EscapeMD(p.PlainName())
}

// CanAct reports whether the player is able to take part right now.
func (p *Player) CanAct() bool {
	return p.Alive && !p.Disconnected
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
	ActionBodyguardGuard = "bodyguard_guard"
	ActionEscortBlock    = "escort_block"
	ActionFramerFrame    = "framer_frame"
	ActionLookoutWatch   = "lookout_watch"
	ActionSerialKill     = "serial_kill"
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
	RoleAssignTimeoutSec int // backstop if the role-delivery ack never arrives
	NightTimeoutSec      int
	DiscussionTimeoutSec int
	NominationTimeoutSec int // time to second a nomination before the day ends
	VotingTimeoutSec     int
	LastWordsSec         int // time for lynched player to speak last words

	// RevealOnLynch and RevealOnNightKill are separate because most groups
	// reveal the role of a lynched player but keep night victims secret.
	RevealOnLynch     bool
	RevealOnNightKill bool

	AllowNoLynch bool
	// LynchRequiresMajority requires more than half of the eligible voters to
	// agree. Without it a single vote decides the lynch when turnout is low.
	LynchRequiresMajority bool
	MafiaRatioDivisor     int
	SpecialRoleDivisor    int
	DoctorSelfProtect     bool
	FirstNightKill        bool // if false, mafia cannot kill on Night 1 (classic variant)
	NominationSystem      bool // if true, use nominate+second before voting
	AllowLastWords        bool // if true, lynched player gets a brief moment to speak
	// SimultaneousNightActions treats the whole night as one instant, so a
	// player killed during the night still completes their own action. When
	// false, dying first cancels your action.
	SimultaneousNightActions bool
	OptionalRoles            []RoleDefinition

	// MayorVoteWeight is the total worth of a revealed Mayor's ballot.
	MayorVoteWeight int
	// EnableLovers pairs two players at the deal; when one dies, so does the
	// other.
	EnableLovers bool
	// LiveVoteBoard keeps a single vote message updated in place instead of
	// announcing every ballot, which is both calmer and easier to read.
	LiveVoteBoard bool
	// MafiaNightChat lets the mafia talk privately through the bot at night.
	MafiaNightChat bool
	// GhostChat lets eliminated players talk to each other.
	GhostChat bool
	// DayReactions attaches a one-tap mood bar to the day announcement.
	DayReactions bool
	// PresetName records which preset this config came from, for display.
	PresetName string
}

func DefaultConfig() GameConfig {
	return GameConfig{
		MinPlayers:               5,
		MaxPlayers:               20,
		LobbyTimeoutSec:          300,
		RoleAssignTimeoutSec:     45,
		NightTimeoutSec:          90,
		DiscussionTimeoutSec:     120,
		NominationTimeoutSec:     45,
		VotingTimeoutSec:         60,
		LastWordsSec:             15,
		RevealOnLynch:            true,
		RevealOnNightKill:        false,
		AllowNoLynch:             true,
		LynchRequiresMajority:    true,
		MafiaRatioDivisor:        4,
		SpecialRoleDivisor:       3,
		DoctorSelfProtect:        false,
		FirstNightKill:           true,
		NominationSystem:         false,
		AllowLastWords:           true,
		SimultaneousNightActions: true,
		OptionalRoles:            DefaultOptionalRoles(),
		MayorVoteWeight:          3,
		EnableLovers:             false,
		LiveVoteBoard:            true,
		MafiaNightChat:           true,
		GhostChat:                true,
		DayReactions:             true,
		PresetName:               PresetClassic,
	}
}

// Preset names offered by the /settings panel.
const (
	PresetClassic = "classic"
	PresetSpeed   = "speed"
	PresetChaos   = "chaos"
	PresetRanked  = "ranked"
)

// PresetNames lists the presets in the order the settings panel shows them.
func PresetNames() []string {
	return []string{PresetClassic, PresetSpeed, PresetChaos, PresetRanked}
}

// PresetLabel is the display name and one-line pitch for a preset.
func PresetLabel(name string) (string, string) {
	switch name {
	case PresetSpeed:
		return "⚡ Speed", "Half-length phases for a quick game"
	case PresetChaos:
		return "🎲 Chaos", "Every role in play, lovers, and a serial killer"
	case PresetRanked:
		return "🏅 Ranked", "Strict rules: no last words, nothing revealed"
	default:
		return "🎭 Classic", "The balanced default ruleset"
	}
}

// PresetConfig returns a complete config for a named preset. An unknown name
// falls back to the default, so a stale stored setting can never break a game.
func PresetConfig(name string) GameConfig {
	cfg := DefaultConfig()
	switch name {
	case PresetSpeed:
		cfg.PresetName = PresetSpeed
		cfg.LobbyTimeoutSec = 180
		cfg.NightTimeoutSec = 45
		cfg.DiscussionTimeoutSec = 60
		cfg.NominationTimeoutSec = 30
		cfg.VotingTimeoutSec = 30
		cfg.LastWordsSec = 10

	case PresetChaos:
		cfg.PresetName = PresetChaos
		cfg.SpecialRoleDivisor = 2 // twice as many special roles
		cfg.EnableLovers = true
		cfg.RevealOnNightKill = true
		cfg.DoctorSelfProtect = true

	case PresetRanked:
		cfg.PresetName = PresetRanked
		cfg.RevealOnLynch = false
		cfg.RevealOnNightKill = false
		cfg.AllowLastWords = false
		cfg.AllowNoLynch = false
		cfg.FirstNightKill = false
		cfg.GhostChat = false
	}
	return cfg
}

// PhaseTimeout returns the configured duration for a phase, or zero when the
// phase is not timed.
func (c GameConfig) PhaseTimeout(p Phase) time.Duration {
	var sec int
	switch p {
	case PhaseLobby:
		sec = c.LobbyTimeoutSec
	case PhaseRoleAssign:
		sec = c.RoleAssignTimeoutSec
	case PhaseNight:
		sec = c.NightTimeoutSec
	case PhaseDiscussion:
		sec = c.DiscussionTimeoutSec
	case PhaseNomination:
		sec = c.NominationTimeoutSec
	case PhaseVoting:
		sec = c.VotingTimeoutSec
	case PhaseLastWords:
		sec = c.LastWordsSec
	}
	return time.Duration(sec) * time.Second
}

type RoleDefinition struct {
	Role              Role
	Team              Team
	MinPlayers        int
	Weight            float64
	ReplacesMafiaSlot bool
}

// DefaultOptionalRoles is the pool special roles are drawn from. MinPlayers
// gates a role until the game is big enough to absorb it, and Weight decides
// how often it shows up relative to the rest of the eligible pool.
//
// Roles that join the mafia must set ReplacesMafiaSlot, otherwise they would
// add an extra enemy on top of the computed mafia count and skew the balance.
func DefaultOptionalRoles() []RoleDefinition {
	return []RoleDefinition{
		{RoleDetective, TeamTown, 6, 1.0, false},
		{RoleDoctor, TeamTown, 7, 0.9, false},
		{RoleEscort, TeamTown, 8, 0.5, false},
		{RoleLookout, TeamTown, 8, 0.5, false},
		{RoleBodyguard, TeamTown, 9, 0.45, false},
		{RoleMayor, TeamTown, 9, 0.35, false},
		{RoleVigilante, TeamTown, 10, 0.4, false},
		{RoleJester, TeamNeutral, 8, 0.3, false},
		{RoleSurvivor, TeamNeutral, 10, 0.25, false},
		{RoleSerialKiller, TeamKiller, 11, 0.3, false},
		{RoleGodfather, TeamMafia, 9, 0.5, true},
		{RoleFramer, TeamMafia, 11, 0.35, true},
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
	Nominations map[PlayerID]*Nomination // target -> nomination info
	ActiveTrial *PlayerID                // player currently on trial (nomination system)

	// Last words
	LastWordsPlayer *PlayerID // player currently giving last words before lynch

	// Discussion interaction state
	Accusations map[PlayerID][]PlayerID // target -> list of accusers
	DefenseUsed map[PlayerID]bool       // players who have used their defense statement
	Whispers    []Whisper               // private whisper log for the day
	SpeakCount  map[PlayerID]int        // track who spoke during discussion (for AFK detection)

	// Win tracking
	Winner    *WinResult
	JesterWon []PlayerID

	// Statistics
	ConsecutiveNoKillNights int

	// StartedAt is when roles were dealt, so the recap can report how long
	// the game actually ran rather than how long the lobby sat open.
	StartedAt time.Time
	// DealNumber counts how many times roles have been dealt. Every role DM
	// and every event reporting on one carries the deal it belongs to, so a
	// redeal cleanly supersedes the attempt before it: outcomes from the
	// abandoned deal keep arriving and must be ignored rather than allowed to
	// close the phase or trigger a second removal.
	DealNumber int
	// NightVisits maps a visited player to everyone who came to their door
	// last night. It is what the Lookout reads.
	NightVisits map[PlayerID][]PlayerID
	// Reactions counts the day's one-tap mood taps, and ReactedBy holds each
	// player's current pick so they can change it but not stuff the ballot.
	Reactions map[string]int
	ReactedBy map[PlayerID]string
	// DeathOrder is every elimination in the order it happened, which drives
	// the first-blood award and the graveyard listing.
	DeathOrder []PlayerID
	// Timeline is the human-readable story of the game, shown in the recap.
	Timeline []TimelineEntry
}

// TimelineEntry is one beat of the recap: what happened, and when.
type TimelineEntry struct {
	Day  int
	Icon string
	Text string
}

type Whisper struct {
	FromID  PlayerID
	ToID    PlayerID
	Message string
	Time    time.Time
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
		Nominations:  make(map[PlayerID]*Nomination),
		Accusations:  make(map[PlayerID][]PlayerID),
		DefenseUsed:  make(map[PlayerID]bool),
		SpeakCount:   make(map[PlayerID]int),
		NightVisits:  make(map[PlayerID][]PlayerID),
		Reactions:    make(map[string]int),
		ReactedBy:    make(map[PlayerID]string),
		Config:       cfg,
		CreatedAt:    time.Now(),
		Log:          []GameEvent{},
	}
}

// maxTimelineEntries bounds the recap. A very long game would otherwise grow
// the stored document without limit.
const maxTimelineEntries = 120

// AddTimeline records one beat of the story for the end-of-game recap.
func (gs *GameState) AddTimeline(icon, text string) {
	gs.Timeline = append(gs.Timeline, TimelineEntry{Day: gs.DayNumber, Icon: icon, Text: text})
	if len(gs.Timeline) > maxTimelineEntries {
		gs.Timeline = append([]TimelineEntry(nil), gs.Timeline[len(gs.Timeline)-maxTimelineEntries:]...)
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

// EligibleVoterCount counts players who are able to cast a vote. Disconnected
// players are excluded so a single dropout cannot stall every vote.
func (gs *GameState) EligibleVoterCount() int {
	count := 0
	for _, p := range gs.Players {
		if p.CanAct() {
			count++
		}
	}
	return count
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

// AliveKillerCount counts the lone-wolf killer faction, which is neither town
// nor mafia but still has to die before the town can win.
func (gs *GameState) AliveKillerCount() int {
	count := 0
	for _, p := range gs.Players {
		if p.Alive && RoleTeam(p.Role) == TeamKiller {
			count++
		}
	}
	return count
}

// TeammatesOf lists the other living members of a player's faction. Only the
// mafia are told who they are, so this is used for their coordination DMs.
func (gs *GameState) TeammatesOf(id PlayerID) []*Player {
	self, ok := gs.Players[id]
	if !ok {
		return nil
	}
	team := RoleTeam(self.Role)
	var mates []*Player
	for _, p := range gs.Players {
		if p.ID != id && p.Alive && RoleTeam(p.Role) == team {
			mates = append(mates, p)
		}
	}
	sort.Slice(mates, func(i, j int) bool { return mates[i].ID < mates[j].ID })
	return mates
}

// TotalVoteWeight is the combined worth of every ballot that could be cast,
// which is what a majority is measured against.
func (gs *GameState) TotalVoteWeight() int {
	total := 0
	for _, p := range gs.Players {
		if p.CanAct() {
			total += p.VoteWeight()
		}
	}
	return total
}

// DeadPlayers lists eliminated players in the order they died, which is how
// the graveyard reads best.
func (gs *GameState) DeadPlayers() []*Player {
	var dead []*Player
	seen := make(map[PlayerID]bool, len(gs.DeathOrder))
	for _, id := range gs.DeathOrder {
		if p, ok := gs.Players[id]; ok && !p.Alive && !seen[id] {
			dead = append(dead, p)
			seen[id] = true
		}
	}
	// A player removed by a path that predates death tracking still belongs
	// in the graveyard.
	for _, p := range playersByJoinTime(gs) {
		if !p.Alive && !seen[p.ID] {
			dead = append(dead, p)
			seen[p.ID] = true
		}
	}
	return dead
}

// RolesAssigned reports whether roles have been dealt. Before that every role
// is the empty string, which RoleTeam maps to town — so win checks would see
// zero mafia and declare a town victory.
func (gs *GameState) RolesAssigned() bool {
	for _, p := range gs.Players {
		if p.Role != RoleUnassigned {
			return true
		}
	}
	return false
}

// Started reports whether the game has progressed past the lobby into a state
// where win conditions are meaningful.
func (gs *GameState) Started() bool {
	if gs.Phase == PhaseIdle || gs.Phase == PhaseLobby {
		return false
	}
	return gs.RolesAssigned()
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
	// Every non-terminal phase must be able to time out, otherwise the game
	// can park in that phase forever.
	for _, p := range []Phase{
		PhaseLobby, PhaseRoleAssign, PhaseNight,
		PhaseDiscussion, PhaseNomination, PhaseVoting,
	} {
		if cfg.PhaseTimeout(p) <= 0 {
			return fmt.Errorf("phase %s has no timeout configured", p)
		}
	}
	if cfg.AllowLastWords && cfg.PhaseTimeout(PhaseLastWords) <= 0 {
		return fmt.Errorf("last words enabled but LastWordsSec is not set")
	}
	if cfg.MayorVoteWeight < 1 {
		return fmt.Errorf("MayorVoteWeight must be at least 1")
	}
	// A role that joins the mafia must take an existing mafia slot. Adding
	// one on top of the computed count would hand the mafia a free extra
	// member and break every balance guarantee below.
	for _, r := range cfg.OptionalRoles {
		if RoleTeam(r.Role) == TeamMafia && !r.ReplacesMafiaSlot {
			return fmt.Errorf("optional mafia role %s must set ReplacesMafiaSlot", r.Role)
		}
		if r.MinPlayers < 1 {
			return fmt.Errorf("optional role %s has no minimum player count", r.Role)
		}
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

// maxLogEntries bounds the in-memory event log. The whole state is serialised
// on every persist, so an unbounded log grows the stored document forever.
const maxLogEntries = 500

func (gs *GameState) AppendLog(eventType string, payload map[string]interface{}) {
	gs.Log = append(gs.Log, GameEvent{
		Timestamp: time.Now(),
		Type:      eventType,
		Payload:   payload,
	})
	if len(gs.Log) > maxLogEntries {
		gs.Log = append([]GameEvent(nil), gs.Log[len(gs.Log)-maxLogEntries:]...)
	}
}

// Clone returns a deep copy safe to read from another goroutine while the
// owning actor keeps mutating the original.
func (gs *GameState) Clone() *GameState {
	if gs == nil {
		return nil
	}
	c := *gs

	c.Players = make(map[PlayerID]*Player, len(gs.Players))
	for id, p := range gs.Players {
		cp := *p
		c.Players[id] = &cp
	}

	c.NightActions = make(map[PlayerID]NightAction, len(gs.NightActions))
	for id, a := range gs.NightActions {
		c.NightActions[id] = a
	}

	c.Votes = make(map[PlayerID]Vote, len(gs.Votes))
	for id, v := range gs.Votes {
		c.Votes[id] = v
	}

	c.Nominations = make(map[PlayerID]*Nomination, len(gs.Nominations))
	for id, n := range gs.Nominations {
		cn := *n
		c.Nominations[id] = &cn
	}

	c.Accusations = make(map[PlayerID][]PlayerID, len(gs.Accusations))
	for id, list := range gs.Accusations {
		c.Accusations[id] = append([]PlayerID(nil), list...)
	}

	c.DefenseUsed = make(map[PlayerID]bool, len(gs.DefenseUsed))
	for id, v := range gs.DefenseUsed {
		c.DefenseUsed[id] = v
	}

	c.SpeakCount = make(map[PlayerID]int, len(gs.SpeakCount))
	for id, v := range gs.SpeakCount {
		c.SpeakCount[id] = v
	}

	c.NightVisits = make(map[PlayerID][]PlayerID, len(gs.NightVisits))
	for id, list := range gs.NightVisits {
		c.NightVisits[id] = append([]PlayerID(nil), list...)
	}

	c.Reactions = make(map[string]int, len(gs.Reactions))
	for k, v := range gs.Reactions {
		c.Reactions[k] = v
	}

	c.ReactedBy = make(map[PlayerID]string, len(gs.ReactedBy))
	for id, v := range gs.ReactedBy {
		c.ReactedBy[id] = v
	}

	c.Whispers = append([]Whisper(nil), gs.Whispers...)
	c.LastNightDeaths = append([]PlayerID(nil), gs.LastNightDeaths...)
	c.JesterWon = append([]PlayerID(nil), gs.JesterWon...)
	c.DeathOrder = append([]PlayerID(nil), gs.DeathOrder...)
	c.Timeline = append([]TimelineEntry(nil), gs.Timeline...)
	c.Config.OptionalRoles = append([]RoleDefinition(nil), gs.Config.OptionalRoles...)

	// Each entry carries its own payload map, which the persist goroutine
	// serialises while the actor keeps appending.
	c.Log = make([]GameEvent, len(gs.Log))
	for i, entry := range gs.Log {
		c.Log[i] = entry
		if entry.Payload != nil {
			payload := make(map[string]interface{}, len(entry.Payload))
			for k, v := range entry.Payload {
				payload[k] = v
			}
			c.Log[i].Payload = payload
		}
	}

	if gs.LastCheckResult != nil {
		v := *gs.LastCheckResult
		c.LastCheckResult = &v
	}
	if gs.ActiveTrial != nil {
		v := *gs.ActiveTrial
		c.ActiveTrial = &v
	}
	if gs.LastWordsPlayer != nil {
		v := *gs.LastWordsPlayer
		c.LastWordsPlayer = &v
	}
	if gs.Winner != nil {
		v := *gs.Winner
		if gs.Winner.JesterWin != nil {
			jw := *gs.Winner.JesterWin
			v.JesterWin = &jw
		}
		c.Winner = &v
	}

	return &c
}
