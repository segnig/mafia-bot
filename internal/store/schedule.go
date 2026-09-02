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
}

// Schedule bounds exposed for help text and validation.
const (
	MinScheduleLead = 5 * time.Minute
	MaxScheduleLead = 7 * 24 * time.Hour
)
