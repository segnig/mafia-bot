package engine

import (
	"strings"
	"testing"
)

func TestHelpIndexListsTopics(t *testing.T) {
	index := FormatHelpIndex()
	for _, topic := range []string{"general", "settings", "roles", "gameplay", "stats", "/guide"} {
		if !strings.Contains(index, topic) {
			t.Errorf("help index missing %q", topic)
		}
	}
}

func TestHelpGeneralCoversSetup(t *testing.T) {
	general := FormatHelpGeneral()
	for _, phrase := range []string{"/startgame", "/begin", "/settings", "/accuse", "DM the bot"} {
		if !strings.Contains(general, phrase) {
			t.Errorf("general help missing %q", phrase)
		}
	}
}

func TestHelpSettingsDocumentsSetCommand(t *testing.T) {
	settings := FormatHelpSettings()
	if !strings.Contains(settings, "/set night 75") {
		t.Error("settings help should show /set examples")
	}
	if !strings.Contains(settings, "Host Only") {
		t.Error("settings help should say host only")
	}
	for _, key := range []string{"night", "discussion", "lovers"} {
		if !strings.Contains(settings, key) {
			t.Errorf("settings help missing key %q", key)
		}
	}
}

func TestHelpCommandsListsRoutedCommands(t *testing.T) {
	cmds := FormatHelpCommands()
	required := []string{
		"/startgame", "/join", "/leave", "/begin", "/endgame",
		"/status", "/graveyard", "/roles", "/settings", "/set",
		"/accuse", "/defend", "/whisper", "/nominate", "/second", "/reveal",
		"/stats", "/leaderboard", "/achievements", "/lastgame",
		"/myrole", "/mafia", "/ghost", "/host", "/kick", "/guide",
	}
	for _, cmd := range required {
		if !strings.Contains(cmds, cmd) {
			t.Errorf("commands help missing %s", cmd)
		}
	}
}

func TestLookupRoleHelp(t *testing.T) {
	cases := []struct {
		query string
		want  Role
	}{
		{"detective", RoleDetective},
		{"Detective", RoleDetective},
		{"godfather", RoleGodfather},
		{"gf", RoleGodfather},
		{"serial killer", RoleSerialKiller},
		{"sk", RoleSerialKiller},
		{"jester", RoleJester},
	}
	for _, tc := range cases {
		role, ok := LookupRoleHelp(tc.query)
		if !ok || role != tc.want {
			t.Errorf("LookupRoleHelp(%q) = %q, %v; want %q, true", tc.query, role, ok, tc.want)
		}
	}
	if _, ok := LookupRoleHelp("not-a-role"); ok {
		t.Error("unknown role should not resolve")
	}
}

func TestResolveHelpTopicRoutes(t *testing.T) {
	if got := ResolveHelpTopic(""); !strings.Contains(got, "Pick a topic") {
		t.Error("empty topic should return index")
	}
	if got := ResolveHelpTopic("settings"); !strings.Contains(got, "Presets") {
		t.Error("settings topic should return settings help")
	}
	if got := ResolveHelpTopic("detective"); !strings.Contains(got, "Detective") {
		t.Error("role topic should return role help")
	}
	if got := ResolveHelpTopic("xyzzy"); !strings.Contains(got, "No help topic") {
		t.Error("unknown topic should return unknown help")
	}
}

func TestFormatGuideMessageLinksToGuide(t *testing.T) {
	msg := FormatGuideMessage()
	if !strings.Contains(msg, PlayerGuideURL) {
		t.Error("guide message should include guide URL")
	}
}

func TestHelpRoleIncludesTeamAndBlurb(t *testing.T) {
	msg := FormatHelpRole(RoleGodfather)
	if !strings.Contains(msg, "Godfather") || !strings.Contains(msg, "Mafia") {
		t.Errorf("role help should include title and team, got %q", msg)
	}
	if !strings.Contains(msg, "Investigations") {
		t.Error("godfather help should mention investigation disguise")
	}
}
