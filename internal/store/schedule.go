package store

import (
	"time"

	"github.com/segni/mafia-bot/internal/engine"
)

// ScheduledGame is a future lobby opening for one group chat. At most one
// schedule exists per chat; scheduling again replaces the previous one.
type ScheduledGame struct {
	ChatID      int64             `json:"chat_id" bson:"chat_id"`
	HostID      engine.PlayerID   `json:"host_id" bson:"host_id"`
	HostUsername string           `json:"host_username,omitempty" bson:"host_username,omitempty"`
	HostName    string            `json:"host_name,omitempty" bson:"host_name,omitempty"`
	ScheduledAt time.Time         `json:"scheduled_at" bson:"scheduled_at"`
	CreatedAt   time.Time         `json:"created_at" bson:"created_at"`
	// Reminder flags so countdown pings fire once each.
	Reminder1h  bool              `json:"reminder_1h,omitempty" bson:"reminder_1h,omitempty"`
	Reminder15m bool              `json:"reminder_15m,omitempty" bson:"reminder_15m,omitempty"`
	Reminder5m  bool              `json:"reminder_5m,omitempty" bson:"reminder_5m,omitempty"`
	// Signups are players who /join while waiting for this schedule to open.
	Signups     []ScheduleSignup  `json:"signups,omitempty" bson:"signups,omitempty"`
	// CardMessageID is the group's scheduled-game card with the Join button.
	CardMessageID int             `json:"card_message_id,omitempty" bson:"card_message_id,omitempty"`
}

// ScheduleSignup records someone who wants in before the lobby opens.
type ScheduleSignup struct {
	PlayerID    engine.PlayerID `json:"player_id" bson:"player_id"`
	Username    string          `json:"username,omitempty" bson:"username,omitempty"`
	DisplayName string          `json:"display_name,omitempty" bson:"display_name,omitempty"`
}

// AddSignup registers a player unless they are already signed up. Returns
// false when they were already on the list.
func (sg *ScheduledGame) AddSignup(id engine.PlayerID, username, displayName string) bool {
	for _, s := range sg.Signups {
		if s.PlayerID == id {
			return false
		}
	}
	sg.Signups = append(sg.Signups, ScheduleSignup{
		PlayerID: id, Username: username, DisplayName: displayName,
	})
	return true
}

// PlayerCount is everyone expected: the host plus signed-up players.
func (sg *ScheduledGame) PlayerCount() int {
	if sg == nil {
		return 0
	}
	return 1 + len(sg.Signups)
}
const (
	MinScheduleLead = 5 * time.Minute
	MaxScheduleLead = 7 * 24 * time.Hour
)
