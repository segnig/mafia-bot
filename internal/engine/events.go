package engine

import "time"

// Event types that drive state transitions
type Event interface {
	eventTag()
}

type JoinEvent struct {
	PlayerID    PlayerID
	Username    string
	DisplayName string
	Time        time.Time
}

func (JoinEvent) eventTag() {}

type LeaveEvent struct {
	PlayerID PlayerID
}

func (LeaveEvent) eventTag() {}

// GameCreatedEvent is sent once after the actor starts so the lobby gets its
// deadline, its countdown timer, and its first status card from the reducer.
type GameCreatedEvent struct{}

func (GameCreatedEvent) eventTag() {}

// ResumeEvent re-arms timers and re-sends the prompts for the current phase.
// Used after a restart, where timers only ever lived in memory.
type ResumeEvent struct{}

func (ResumeEvent) eventTag() {}

type BeginEvent struct {
	PlayerID PlayerID
	IsAdmin  bool
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

// RolesDeliveredEvent is dispatched by the transport once every role DM for
// this game has been handed to the sender.
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
	PlayerID         PlayerID
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
	IsAdmin      bool
}

func (HostTransferEvent) eventTag() {}

// KickEvent — host kicks an AFK player
type KickEvent struct {
	HostID   PlayerID
	TargetID PlayerID
	IsAdmin  bool
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

// SendRoleDMEffect is a role assignment DM. It is distinct from SendDMEffect
// because the transport has to know when these specific messages have actually
// been delivered: that is what closes the role-assignment phase, and a failure
// means the role has to be redealt rather than the player merely muted.
type SendRoleDMEffect struct {
	GameID   GameID
	PlayerID PlayerID
	Text     string
}

func (SendRoleDMEffect) effectTag() {}

type SendGroupEffect struct {
	ChatID int64
	Text   string
}

func (SendGroupEffect) effectTag() {}

type SendVotingKeyboardEffect struct {
	ChatID       int64
	GameID       GameID
	Targets      []PlayerID
	AllowNoLynch bool
	Prompt       string
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

// RolesDeliveredEffect closes the role-assignment phase. The transport turns
// it back into a RolesDeliveredEvent once the preceding role DMs have been
// queued, which is what advances the game into Night 1.
type RolesDeliveredEffect struct {
	GameID GameID
}

func (RolesDeliveredEffect) effectTag() {}

type GameOverEffect struct {
	GameID GameID
	Result WinResult
}

func (GameOverEffect) effectTag() {}

// SendLobbyStatusEffect — displays the lobby card with player list and join button
type SendLobbyStatusEffect struct {
	ChatID     int64
	GameID     GameID
	HostName   string
	Players    []string // display names of current players
	MinPlayers int
	MaxPlayers int
}

func (SendLobbyStatusEffect) effectTag() {}
