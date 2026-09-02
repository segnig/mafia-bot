package telegram

import (
	"strconv"
	"testing"
	"time"

	"github.com/segni/mafia-bot/internal/store"
)

func TestParseScheduleIn(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	got, err := parseScheduleTime("in 2h", now)
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(2 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseScheduleInRejectsTooSoon(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if _, err := parseScheduleTime("in 1m", now); err == nil {
		t.Fatal("expected error for schedule under MinScheduleLead")
	}
}

func TestParseScheduleAtUsesNextOccurrence(t *testing.T) {
	// 20:00 EAT = 17:00 UTC
	now := time.Date(2026, 9, 2, 17, 0, 0, 0, time.UTC)
	got, err := parseScheduleTime("at 21:00", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC) // 21:00 EAT
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseScheduleAtRollsToTomorrow(t *testing.T) {
	now := time.Date(2026, 9, 2, 17, 0, 0, 0, time.UTC)
	got, err := parseScheduleTime("at 19:00", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestFormatScheduleCountdown(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if got := formatScheduleCountdown(now.Add(2*time.Hour+15*time.Minute), now); got != "2h 15m" {
		t.Fatalf("got %q want 2h 15m", got)
	}
	if got := formatScheduleCountdown(now.Add(3*time.Minute+20*time.Second), now); got != "3m 20s" {
		t.Fatalf("got %q want 3m 20s", got)
	}
}

func TestParseScheduleRejectsUnknownForm(t *testing.T) {
	now := time.Now().UTC()
	if _, err := parseScheduleTime("tomorrow evening", now); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseScheduleInRespectsMaxLead(t *testing.T) {
	now := time.Now().UTC()
	days := int(store.MaxScheduleLead/(24*time.Hour)) + 1
	if _, err := parseScheduleTime("in "+strconv.Itoa(days)+"d", now); err == nil {
		t.Fatal("expected max lead error")
	}
}
