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
	Phase          Phase
	SecondsLeft    int
}

func (TimerWarningEvent) eventTag() {}

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
