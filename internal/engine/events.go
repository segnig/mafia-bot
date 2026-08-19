package engine

import "time"

// Event types that drive state transitions
type Event interface {
	eventTag()
}

type JoinEvent struct {
	PlayerID PlayerID
	Username string
	Time     time.Time
}

func (JoinEvent) eventTag() {}

type LeaveEvent struct {
	PlayerID PlayerID
}

func (LeaveEvent) eventTag() {}

type BeginEvent struct {
	PlayerID PlayerID
}

func (BeginEvent) eventTag() {}

type EndGameEvent struct {
	PlayerID PlayerID
	IsAdmin  bool
}

func (EndGameEvent) eventTag() {}

type NightActionEvent struct {
	Action NightAction
}

func (NightActionEvent) eventTag() {}

type VoteEvent struct {
	Vote Vote
}

func (VoteEvent) eventTag() {}

type TimeoutEvent struct {
	Phase Phase
}

func (TimeoutEvent) eventTag() {}

type RolesDeliveredEvent struct{}

func (RolesDeliveredEvent) eventTag() {}

// PlayerDisconnectedEvent fires when a player blocks the bot or leaves the group
type PlayerDisconnectedEvent struct {
	PlayerID PlayerID
}

func (PlayerDisconnectedEvent) eventTag() {}

// TimerWarningEvent fires at 60s and 10s before phase deadline
type TimerWarningEvent struct {
	Phase       Phase
	SecondsLeft int
}

func (TimerWarningEvent) eventTag() {}

// NominateEvent — a player nominates another for lynching (nomination system)
type NominateEvent struct {
	NominatorID PlayerID
	TargetID    PlayerID
}

func (NominateEvent) eventTag() {}

// SecondEvent — a player seconds an existing nomination
type SecondEvent struct {
	PlayerID     PlayerID
	NominationTarget PlayerID
}

func (SecondEvent) eventTag() {}

// LastWordsCompleteEvent — lynched player's last words time expired or they finished
type LastWordsCompleteEvent struct{}

func (LastWordsCompleteEvent) eventTag() {}

// HostTransferEvent — transfer host to another player
type HostTransferEvent struct {
	FromPlayerID PlayerID
	ToPlayerID   PlayerID
}

func (HostTransferEvent) eventTag() {}

// KickEvent — host kicks an AFK player
type KickEvent struct {
	HostID   PlayerID
	TargetID PlayerID
}

func (KickEvent) eventTag() {}

// RoleDeliveryFailedEvent — DM delivery failed for a player during role assignment (§8.3)
type RoleDeliveryFailedEvent struct {
	PlayerID PlayerID
}

func (RoleDeliveryFailedEvent) eventTag() {}

// AccuseEvent — player publicly accuses another during discussion
type AccuseEvent struct {
	AccuserID PlayerID
	TargetID  PlayerID
}

func (AccuseEvent) eventTag() {}

// DefendEvent — player makes a public defense statement during discussion
type DefendEvent struct {
	PlayerID  PlayerID
	Statement string
}

func (DefendEvent) eventTag() {}

// WhisperEvent — player sends a private whisper to another (visible to both, logged for audit)
type WhisperEvent struct {
	FromID  PlayerID
	ToID    PlayerID
	Message string
}

func (WhisperEvent) eventTag() {}

// PlayerSpokeEvent — tracks that a player spoke during discussion (for AFK detection)
type PlayerSpokeEvent struct {
	PlayerID PlayerID
}

func (PlayerSpokeEvent) eventTag() {}

// Side effects emitted by the reducer for the transport layer to execute
type SideEffect interface {
	effectTag()
}

type SendDMEffect struct {
	PlayerID PlayerID
	Text     string
}

func (SendDMEffect) effectTag() {}

type SendGroupEffect struct {
	ChatID int64
	Text   string
}

func (SendGroupEffect) effectTag() {}

type SendVotingKeyboardEffect struct {
	ChatID       int64
	Targets      []PlayerID
	AllowNoLynch bool
}

func (SendVotingKeyboardEffect) effectTag() {}

type SendNightActionEffect struct {
	PlayerID PlayerID
	Role     Role
	Targets  []PlayerID
	GameID   GameID
}

func (SendNightActionEffect) effectTag() {}

type SetTimerEffect struct {
	Duration time.Duration
	Phase    Phase
}

func (SetTimerEffect) effectTag() {}

type SetWarningTimerEffect struct {
	Duration    time.Duration
	Phase       Phase
	SecondsLeft int
}

func (SetWarningTimerEffect) effectTag() {}

type GameOverEffect struct {
	Result WinResult
}

func (GameOverEffect) effectTag() {}

type RemovePlayerEffect struct {
	PlayerID PlayerID
	GameID   GameID
	Reason   string
}

func (RemovePlayerEffect) effectTag() {}

type SendNominationKeyboardEffect struct {
	ChatID  int64
	Targets []PlayerID
	GameID  GameID
}

func (SendNominationKeyboardEffect) effectTag() {}

type SendTrialEffect struct {
	ChatID   int64
	Accused  PlayerID
	GameID   GameID
}

func (SendTrialEffect) effectTag() {}

type SendLastWordsEffect struct {
	ChatID   int64
	PlayerID PlayerID
}

func (SendLastWordsEffect) effectTag() {}

type SendWhisperEffect struct {
	FromID  PlayerID
	ToID    PlayerID
	Message string
}

func (SendWhisperEffect) effectTag() {}

type SendAccusationSummaryEffect struct {
	ChatID      int64
	Accusations map[PlayerID]int // target -> count
}

func (SendAccusationSummaryEffect) effectTag() {}

// SendLobbyStatusEffect — displays the lobby card with player list and join button
type SendLobbyStatusEffect struct {
	ChatID     int64
	GameID     GameID
	HostName   string
	Players    []string // usernames of current players
	MinPlayers int
	MaxPlayers int
}

func (SendLobbyStatusEffect) effectTag() {}
