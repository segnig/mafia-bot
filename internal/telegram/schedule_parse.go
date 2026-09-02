package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/segni/mafia-bot/internal/store"
)

// East Africa Time — UTC+3, no daylight saving.
var scheduleZone = time.FixedZone("EAT", 3*60*60)

const scheduleZoneLabel = "EAT (UTC+3)"

// parseScheduleTime converts user input into an absolute time (stored as UTC).
//
// Supported forms:
//   - in 30m, in 2h, in 1d, in 90m
//   - at 20:00, at 08:30  (24-hour clock in East Africa Time / UTC+3)
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
	return time.Time{}, fmt.Errorf("use `in 30m`, `in 2h`, or `at 20:00` (%s)", scheduleZoneLabel)
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

	loc := scheduleZone
	localNow := now.In(scheduleZone)
	candidate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, loc)
	if !candidate.After(now.Add(store.MinScheduleLead - time.Second)) {
		candidate = candidate.Add(24 * time.Hour)
	}
	if candidate.Sub(now) > store.MaxScheduleLead {
		return time.Time{}, fmt.Errorf("cannot schedule more than %d days ahead", int(store.MaxScheduleLead/(24*time.Hour)))
	}
	return candidate, nil
}

func formatScheduleInstant(t time.Time) string {
	return t.In(scheduleZone).Format("Mon Jan 2, 15:04 ") + scheduleZoneLabel
}

// formatScheduleCountdown returns a compact remaining-time string, e.g. "2d 5h 12m".
func formatScheduleCountdown(until, now time.Time) string {
	d := until.Sub(now)
	if d <= 0 {
		return "starting now"
	}
	if d < time.Minute {
		return "less than a minute"
	}

	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	seconds := int(d / time.Second)

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if len(parts) == 0 || d < 5*time.Minute {
		if seconds > 0 {
			parts = append(parts, fmt.Sprintf("%ds", seconds))
		}
	}
	if len(parts) == 0 {
		return "less than a minute"
	}
	return strings.Join(parts, " ")
}

func formatScheduleWhen(t time.Time, now time.Time) string {
	return formatScheduleCountdown(t, now)
}
