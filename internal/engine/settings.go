package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// SettingKind distinguishes a plain on/off switch from one that cycles through
// a list of values.
type SettingKind int

const (
	SettingToggle SettingKind = iota
	SettingChoice
)

// Setting describes one knob a group can change from the /settings panel. The
// registry is the single source of truth: the panel renders from it, stored
// overrides are applied through it, and nothing else needs to know the field
// names.
type Setting struct {
	Key   string
	Label string
	Help  string
	Kind  SettingKind
	// Choices are preset values shown when cycling; custom values within
	// Min/Max are also accepted via /set.
	Choices []string
	Min     int // minimum for numeric settings; 0 means unused
	Max     int // maximum for numeric settings; 0 means unused
	// Get reads the current value as a string.
	Get func(cfg GameConfig) string
	// Set writes a value. It is only called with a value Validate accepted.
	Set func(cfg *GameConfig, value string)
}

// Display renders the current value for the panel button.
func (s Setting) Display(cfg GameConfig) string {
	value := s.Get(cfg)
	if s.Kind == SettingToggle {
		if value == "true" {
			return "✅ on"
		}
		return "⬜ off"
	}
	return value
}

// Next returns the value that follows the current one when the button is
// tapped, wrapping around at the end.
func (s Setting) Next(cfg GameConfig) string {
	current := s.Get(cfg)
	if s.Kind == SettingToggle {
		if current == "true" {
			return "false"
		}
		return "true"
	}
	for i, choice := range s.Choices {
		if choice == current {
			return s.Choices[(i+1)%len(s.Choices)]
		}
	}
	if len(s.Choices) > 0 {
		return s.Choices[0]
	}
	return current
}

func boolSetting(key, label, help string, get func(GameConfig) bool, set func(*GameConfig, bool)) Setting {
	return Setting{
		Key: key, Label: label, Help: help, Kind: SettingToggle,
		Get: func(cfg GameConfig) string { return strconv.FormatBool(get(cfg)) },
		Set: func(cfg *GameConfig, value string) { set(cfg, value == "true") },
	}
}

func secondsSetting(key, label, help string, choices []int, min, max int, get func(GameConfig) int, set func(*GameConfig, int)) Setting {
	options := make([]string, len(choices))
	for i, c := range choices {
		options[i] = strconv.Itoa(c)
	}
	return Setting{
		Key: key, Label: label, Help: help, Kind: SettingChoice,
		Choices: options, Min: min, Max: max,
		Get: func(cfg GameConfig) string { return strconv.Itoa(get(cfg)) },
		Set: func(cfg *GameConfig, value string) {
			if n, err := strconv.Atoi(value); err == nil {
				set(cfg, n)
			}
		},
	}
}

var settingsRegistry = []Setting{
	secondsSetting("night", "🌙 Night length", "Seconds for night actions (20–300)",
		[]int{30, 45, 60, 90, 120}, 20, 300,
		func(c GameConfig) int { return c.NightTimeoutSec },
		func(c *GameConfig, n int) { c.NightTimeoutSec = n }),

	secondsSetting("discussion", "💬 Discussion length", "Seconds to argue before voting (30–600)",
		[]int{45, 60, 90, 120, 180, 240}, 30, 600,
		func(c GameConfig) int { return c.DiscussionTimeoutSec },
		func(c *GameConfig, n int) { c.DiscussionTimeoutSec = n }),

	secondsSetting("voting", "🗳️ Voting length", "Seconds to cast ballots (15–180)",
		[]int{20, 30, 45, 60, 90}, 15, 180,
		func(c GameConfig) int { return c.VotingTimeoutSec },
		func(c *GameConfig, n int) { c.VotingTimeoutSec = n }),

	secondsSetting("lobby", "🚪 Lobby length", "Seconds the lobby stays open (60–1800)",
		[]int{120, 180, 300, 600}, 60, 1800,
		func(c GameConfig) int { return c.LobbyTimeoutSec },
		func(c *GameConfig, n int) { c.LobbyTimeoutSec = n }),

	boolSetting("reveal_lynch", "⚖️ Reveal on lynch", "Show the role of a lynched player",
		func(c GameConfig) bool { return c.RevealOnLynch },
		func(c *GameConfig, v bool) { c.RevealOnLynch = v }),

	boolSetting("reveal_night", "🌙 Reveal on night kill", "Show the role of a night victim",
		func(c GameConfig) bool { return c.RevealOnNightKill },
		func(c *GameConfig, v bool) { c.RevealOnNightKill = v }),

	boolSetting("majority", "📊 Majority to lynch", "Require more than half the votes",
		func(c GameConfig) bool { return c.LynchRequiresMajority },
		func(c *GameConfig, v bool) { c.LynchRequiresMajority = v }),

	boolSetting("no_lynch", "🕊️ Allow skipping", "Offer a 'skip today' vote option",
		func(c GameConfig) bool { return c.AllowNoLynch },
		func(c *GameConfig, v bool) { c.AllowNoLynch = v }),

	boolSetting("last_words", "🎤 Last words", "Let the condemned speak before dying",
		func(c GameConfig) bool { return c.AllowLastWords },
		func(c *GameConfig, v bool) { c.AllowLastWords = v }),

	boolSetting("first_night_kill", "🔪 Night 1 kill", "Let the mafia kill on the first night",
		func(c GameConfig) bool { return c.FirstNightKill },
		func(c *GameConfig, v bool) { c.FirstNightKill = v }),

	boolSetting("nomination", "⚖️ Trial mode", "Require nominate and second before voting",
		func(c GameConfig) bool { return c.NominationSystem },
		func(c *GameConfig, v bool) { c.NominationSystem = v }),

	boolSetting("lovers", "💞 Lovers", "Pair two players who die together",
		func(c GameConfig) bool { return c.EnableLovers },
		func(c *GameConfig, v bool) { c.EnableLovers = v }),

	boolSetting("mafia_chat", "🔪 Mafia night chat", "Let the mafia talk privately at night",
		func(c GameConfig) bool { return c.MafiaNightChat },
		func(c *GameConfig, v bool) { c.MafiaNightChat = v }),

	boolSetting("ghost_chat", "👻 Ghost chat", "Let eliminated players talk to each other",
		func(c GameConfig) bool { return c.GhostChat },
		func(c *GameConfig, v bool) { c.GhostChat = v }),

	boolSetting("live_board", "📊 Live vote board", "Update one vote message instead of many",
		func(c GameConfig) bool { return c.LiveVoteBoard },
		func(c *GameConfig, v bool) { c.LiveVoteBoard = v }),

	boolSetting("reactions", "🎭 Day reactions", "Attach a mood bar to the day announcement",
		func(c GameConfig) bool { return c.DayReactions },
		func(c *GameConfig, v bool) { c.DayReactions = v }),

	boolSetting("doctor_self", "💊 Doctor self-heal", "Let the doctor protect themselves",
		func(c GameConfig) bool { return c.DoctorSelfProtect },
		func(c *GameConfig, v bool) { c.DoctorSelfProtect = v }),

	secondsSetting("special_roles", "🎲 Special role density", "Lower = more special roles (2–6)",
		[]int{2, 3, 4}, 2, 6,
		func(c GameConfig) int { return c.SpecialRoleDivisor },
		func(c *GameConfig, n int) { c.SpecialRoleDivisor = n }),
}

// Settings returns the tweakable settings in panel order.
func Settings() []Setting {
	return settingsRegistry
}

// SettingByKey looks up one setting.
func SettingByKey(key string) (Setting, bool) {
	for _, s := range settingsRegistry {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// ApplySetting writes one stored override onto a config. An unknown key or an
// unusable value is ignored, so an old stored setting can never break a game.
func ApplySetting(cfg *GameConfig, key, value string) {
	_ = SetSettingValue(cfg, key, value)
}

// SetSettingValue applies one setting, accepting custom numeric values within
// each setting's allowed range. Returns an error for unknown keys or bad values.
func SetSettingValue(cfg *GameConfig, key, value string) error {
	s, ok := SettingByKey(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	if s.Kind == SettingToggle {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "on", "yes", "1":
			s.Set(cfg, "true")
			return nil
		case "false", "off", "no", "0":
			s.Set(cfg, "false")
			return nil
		default:
			return fmt.Errorf("use on or off")
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("must be a number")
	}
	if s.Min > 0 && n < s.Min {
		return fmt.Errorf("minimum is %d", s.Min)
	}
	if s.Max > 0 && n > s.Max {
		return fmt.Errorf("maximum is %d", s.Max)
	}
	s.Set(cfg, strconv.Itoa(n))
	return nil
}

// ApplyPreset replaces cfg with a named preset's defaults.
func ApplyPreset(cfg *GameConfig, preset string) {
	*cfg = PresetConfig(preset)
}

// CycleSetting advances one registry setting on cfg. Returns false for an
// unknown key.
func CycleSetting(cfg *GameConfig, key string) bool {
	s, ok := SettingByKey(key)
	if !ok {
		return false
	}
	s.Set(cfg, s.Next(*cfg))
	return true
}

// CanEditLobbyConfig reports whether a user may change lobby settings.
// Only the host may configure a game.
func CanEditLobbyConfig(hostID, playerID PlayerID) bool {
	return hostID == playerID
}

// FormatSettingsPanel is the read-only rules summary shown to players.
func FormatSettingsPanel(cfg GameConfig) string {
	label, pitch := PresetLabel(cfg.PresetName)
	msg := "⚙️ *Game Rules*\n" + divider + "\n"
	msg += fmt.Sprintf("Preset: *%s*\n_%s_\n\n", EscapeMD(label), EscapeMD(pitch))
	msg += fmt.Sprintf("🌙 Night %ds  ·  💬 Day %ds  ·  🗳️ Vote %ds\n",
		cfg.NightTimeoutSec, cfg.DiscussionTimeoutSec, cfg.VotingTimeoutSec)
	msg += divider + "\n"
	msg += "_Only the host configures these in the lobby before /begin._"
	return msg
}

// FormatLobbySettingsPanel is the editable settings message for the host.
func FormatLobbySettingsPanel(cfg GameConfig) string {
	label, pitch := PresetLabel(cfg.PresetName)
	msg := "⚙️ *Configure This Game*\n" + divider + "\n"
	msg += fmt.Sprintf("Preset: *%s*\n_%s_\n\n", EscapeMD(label), EscapeMD(pitch))
	msg += fmt.Sprintf("🌙 Night %ds  ·  💬 Day %ds  ·  🗳️ Vote %ds\n",
		cfg.NightTimeoutSec, cfg.DiscussionTimeoutSec, cfg.VotingTimeoutSec)
	msg += divider + "\n"
	msg += "_Tap to cycle · `/set night 75` for custom values_\n"
	msg += "_Locked when you run /begin._"
	return msg
}

// FormatSetHelp lists /set usage for the host.
func FormatSetHelp() string {
	msg := "⚙️ *Custom settings* (host only, lobby only)\n" + divider + "\n"
	msg += "Usage: `/set <key> <value>`\n\n"
	for _, s := range Settings() {
		switch s.Kind {
		case SettingToggle:
			msg += fmt.Sprintf("• `%s` — on/off\n", s.Key)
		default:
			msg += fmt.Sprintf("• `%s` — %d–%d\n", s.Key, s.Min, s.Max)
		}
	}
	msg += divider + "\n_Example: `/set night 75` or `/set lovers on`_"
	return msg
}
