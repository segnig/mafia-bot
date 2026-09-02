package store

import (
	"testing"

	"github.com/segni/mafia-bot/internal/engine"
)

func TestScheduledGamePlayerCount(t *testing.T) {
	sg := &ScheduledGame{ChatID: 1, HostID: 1}
	if sg.PlayerCount() != 1 {
		t.Fatalf("host only: got %d want 1", sg.PlayerCount())
	}
	sg.AddSignup(2, "bob", "Bob")
	if sg.PlayerCount() != 2 {
		t.Fatalf("host + 1: got %d want 2", sg.PlayerCount())
	}
}

func TestScheduledGameAddSignup(t *testing.T) {
	sg := &ScheduledGame{ChatID: 1}
	if !sg.AddSignup(10, "alice", "Alice") {
		t.Fatal("first signup should succeed")
	}
	if sg.AddSignup(10, "alice", "Alice") {
		t.Fatal("duplicate signup should be rejected")
	}
	if len(sg.Signups) != 1 {
		t.Fatalf("want 1 signup, got %d", len(sg.Signups))
	}
	if sg.Signups[0].PlayerID != engine.PlayerID(10) {
		t.Fatalf("unexpected player id %d", sg.Signups[0].PlayerID)
	}
}
