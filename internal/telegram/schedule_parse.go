package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/segni/mafia-bot/internal/store"
)

// parseScheduleTime converts user input into an absolute UTC time.
//
// Supported forms:
//   - in 30m, in 2h, in 1d, in 90m
//   - at 20:00, at 08:30  (24-hour clock, UTC; next occurrence)
func parseScheduleTime(args string, now time.Time) (time.Time, error) {
	args = strings.TrimSpace(strings.ToLower(args))
	if args == "" {
		return time.Time{}, fmt.Errorf("empty schedule time")
	}

	if strings.HasPrefix(args, "in ") {
		return parseScheduleIn(args[3:], now)
	}
	if strings.HasPrefix(args, "at ") {
		return parseScheduleAt(args[3:], now)
	}
	return time.Time{}, fmt.Errorf("use `in 30m`, `in 2h`, or `at 20:00` (UTC)")
}

func parseScheduleIn(body string, now time.Time) (time.Time, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return time.Time{}, fmt.Errorf("missing duration after `in`")
	}

	var total time.Duration
	for body != "" {
		i := 0
		for i < len(body) && body[i] >= '0' && body[i] <= '9' {
			i++
		}
		if i == 0 {
			return time.Time{}, fmt.Errorf("duration must look like 30m, 2h, or 1d")
		}
		n, err := strconv.Atoi(body[:i])
		if err != nil || n <= 0 {
			return time.Time{}, fmt.Errorf("invalid duration number")
		}
		body = body[i:]
		if body == "" {
			return time.Time{}, fmt.Errorf("missing unit (m, h, or d)")
		}
		unit := body[0]
		body = strings.TrimSpace(body[1:])

		switch unit {
		case 'm':
			total += time.Duration(n) * time.Minute
		case 'h':
			total += time.Duration(n) * time.Hour
		case 'd':
			total += time.Duration(n) * 24 * time.Hour
		default:
			return time.Time{}, fmt.Errorf("use m (minutes), h (hours), or d (days)")
		}
	}

	if total < store.MinScheduleLead {
		return time.Time{}, fmt.Errorf("schedule at least %d minutes ahead", int(store.MinScheduleLead.Minutes()))
	}
	if total > store.MaxScheduleLead {
		return time.Time{}, fmt.Errorf("cannot schedule more than %d days ahead", int(store.MaxScheduleLead/(24*time.Hour)))
	}
	return now.Add(total), nil
}

func parseScheduleAt(body string, now time.Time) (time.Time, error) {
	body = strings.TrimSpace(body)
	parts := strings.Split(body, ":")
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("time must look like 20:00 or 08:30")
	}
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || hour < 0 || hour > 23 {
		return time.Time{}, fmt.Errorf("hour must be 0–23")
	}
	minute, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || minute < 0 || minute > 59 {
		return time.Time{}, fmt.Errorf("minute must be 0–59")
	}

	loc := time.UTC
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !candidate.After(now.Add(store.MinScheduleLead - time.Second)) {
		candidate = candidate.Add(24 * time.Hour)
	}
	if candidate.Sub(now) > store.MaxScheduleLead {
		return time.Time{}, fmt.Errorf("cannot schedule more than %d days ahead", int(store.MaxScheduleLead/(24*time.Hour)))
	}
	return candidate, nil
}

func formatScheduleWhen(t time.Time, now time.Time) string {
	if t.Sub(now) < 24*time.Hour {
		return fmt.Sprintf("in %s", shortDuration(t.Sub(now)))
	}
	return t.UTC().Format("Mon Jan 2, 15:04 UTC")
}

func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	if d < time.Hour {
		m := int(d.Round(time.Minute) / time.Minute)
		if m == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", m)
	}
	if d < 24*time.Hour {
		h := int(d.Round(time.Hour) / time.Hour)
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	days := int(d.Round(24*time.Hour) / (24 * time.Hour))
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
