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

// ConfigPresetEvent switches the lobby to a named preset. Only valid while the
// game is still in the lobby, before /begin locks the ruleset.
type ConfigPresetEvent struct {
	PlayerID PlayerID
	Preset   string
}

func (ConfigPresetEvent) eventTag() {}

// ConfigSettingEvent changes one lobby setting. An empty Value cycles through
// presets; a non-empty Value sets a custom amount (via /set or the panel).
type ConfigSettingEvent struct {
	PlayerID PlayerID
	Key      string
	Value    string
}

func (ConfigSettingEvent) eventTag() {}

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

// RolesDeliveredEvent is dispatched by the transport once every role DM of one
// deal has resolved.
//
// Deal identifies which deal it is reporting on. A redeal supersedes the
// previous one, and the DMs of the abandoned attempt keep resolving afterwards,
// so without this the completion of an old batch could start Night 1 while the
// current deal's DMs were still in flight.
type RolesDeliveredEvent struct {
	Deal int
}

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

// RoleDeliveryFailedEvent — DM delivery failed for a player during role
// assignment (§8.3). Deal names the deal whose DM failed, so a failure from an
// attempt that has already been superseded is ignored rather than triggering a
// second redeal for a player who has since been sent a fresh role.
type RoleDeliveryFailedEvent struct {
	PlayerID PlayerID
	Deal     int
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

// RevealEvent — a Mayor goes public, trading secrecy for voting power.
type RevealEvent struct {
	PlayerID PlayerID
}

func (RevealEvent) eventTag() {}

// ReactEvent — a player taps the day's mood bar.
type ReactEvent struct {
	PlayerID PlayerID
	Emoji    string
}

func (ReactEvent) eventTag() {}

// MafiaChatEvent — a private message from one mafia member to the rest.
type MafiaChatEvent struct {
	FromID  PlayerID
	Message string
}

func (MafiaChatEvent) eventTag() {}

// GhostChatEvent — a message from an eliminated player to the other ghosts.
type GhostChatEvent struct {
	FromID  PlayerID
	Message string
}

func (GhostChatEvent) eventTag() {}

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
	// Deal is the generation this DM belongs to, echoed back on the resulting
	// event so the reducer can tell a current outcome from a superseded one.
	Deal int
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
// it back into a RolesDeliveredEvent once the preceding role DMs have actually
// resolved, which is what advances the game into Night 1.
type RolesDeliveredEffect struct {
	GameID GameID
	// Deal must match the SendRoleDMEffects that precede it: it is what tells
	// the transport which batch this seals.
	Deal int
}

func (RolesDeliveredEffect) effectTag() {}

type GameOverEffect struct {
	GameID GameID
	Result WinResult
	// Summary is everything the transport needs to write history, update
	// player records, and hand out achievements.
	Summary GameSummary
}

func (GameOverEffect) effectTag() {}

// UpdateVoteBoardEffect edits the live vote message in place. The transport
// falls back to sending a fresh board if it has no message to edit.
type UpdateVoteBoardEffect struct {
	ChatID       int64
	GameID       GameID
	Targets      []PlayerID
	AllowNoLynch bool
	Text         string
}

func (UpdateVoteBoardEffect) effectTag() {}

// ReactionBarEffect posts the day's one-tap mood bar.
type ReactionBarEffect struct {
	ChatID int64
	GameID GameID
	Text   string
}

func (ReactionBarEffect) effectTag() {}

// SendGroupWithRematchEffect posts a message carrying a rematch button. Used
// for the final recap so a group can go straight into another game.
type SendGroupWithRematchEffect struct {
	ChatID int64
	Text   string
}

func (SendGroupWithRematchEffect) effectTag() {}

// SendLobbyStatusEffect — displays the lobby card with player list and join button
type SendLobbyStatusEffect struct {
	ChatID     int64
	GameID     GameID
	HostName   string
	Players    []string // display names of current players
	MinPlayers int
	MaxPlayers int
	// Preset names the ruleset this game will use, so players can see what
	// they are joining before it starts.
	Preset string
}

func (SendLobbyStatusEffect) effectTag() {}

// LobbyConfigUpdatedEffect tells the transport to persist the lobby config as
// this group's default for the next /startgame or rematch.
type LobbyConfigUpdatedEffect struct {
	ChatID int64
	Config GameConfig
}

func (LobbyConfigUpdatedEffect) effectTag() {}
