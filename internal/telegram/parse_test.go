package telegram

import (
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/actor"
	"github.com/segni/mafia-bot/internal/engine"
)

func TestWhisperBodyStripsTheLeadingMention(t *testing.T) {
	cases := []struct {
		args, want string
	}{
		{"@bob trust me vote ann", "trust me vote ann"},
		{"@bob   extra   spaces", "extra   spaces"},
		{"@bob", ""},
		{"trust me, I replied", "trust me, I replied"},
		{"  hello  ", "hello"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := whisperBody(tc.args); got != tc.want {
			t.Errorf("whisperBody(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestUsernameFromMention(t *testing.T) {
	text := "/whisper @Bob_99 hello"
	if got := usernameFromMention(text, 9, 7); got != "Bob_99" {
		t.Errorf("got %q, want Bob_99", got)
	}
	if got := usernameFromMention(text, 0, 100); got != "" {
		t.Errorf("an out-of-range entity should be ignored, got %q", got)
	}
	if got := usernameFromMention(text, 9, 1); got != "" {
		t.Errorf("a one-rune entity cannot be a mention, got %q", got)
	}
}

func TestLookupPlayerByUsernameIsCaseInsensitive(t *testing.T) {
	players := map[engine.PlayerID]*engine.Player{
		2: {ID: 2, Username: "Bob"},
		3: {ID: 3, Username: "ann"},
	}
	if got := lookupPlayerByUsername(players, "bob"); got != 2 {
		t.Errorf("looked up %d, want 2", got)
	}
	if got := lookupPlayerByUsername(players, "ANN"); got != 3 {
		t.Errorf("looked up %d, want 3", got)
	}
	if got := lookupPlayerByUsername(players, "nobody"); got != 0 {
		t.Errorf("unknown username returned %d", got)
	}
}

func TestGlobalLeaderboardSwitch(t *testing.T) {
	if !isGlobalLeaderboard("global") || !isGlobalLeaderboard(" GLOBAL ") {
		t.Error("the documented switch should select the worldwide board")
	}
	for _, args := range []string{"", "here", "globally", "globa"} {
		if isGlobalLeaderboard(args) {
			t.Errorf("%q should stay on the per-chat board", args)
		}
	}
}

// extractTargetPlayer has to work for every command that takes an @player:
// accuse, whisper, nominate, second, kick, host.
func TestExtractTargetPlayerFromReplyMentionAndTextMention(t *testing.T) {
	outbox := make(chan actor.OutgoingMessage, 16)
	go func() {
		for range outbox {
		}
	}()
	sup := actor.NewSupervisor(outbox)
	defer sup.Shutdown(2 * time.Second)

	gs := engine.NewGameState("g1", -100, 1, engine.DefaultConfig())
	gs.Players[1] = &engine.Player{ID: 1, Username: "ann", Alive: true}
	gs.Players[2] = &engine.Player{ID: 2, Username: "bob", Alive: true}
	ga := sup.StartGame(gs)
	b := &Bot{supervisor: sup}

	t.Run("reply", func(t *testing.T) {
		msg := &tgbotapi.Message{
			ReplyToMessage: &tgbotapi.Message{
				From: &tgbotapi.User{ID: 2},
			},
		}
		if got := b.extractTargetPlayer(msg, ga); got != 2 {
			t.Errorf("reply resolved to %d, want 2", got)
		}
	})

	t.Run("text_mention", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Entities: []tgbotapi.MessageEntity{{
				Type: "text_mention",
				User: &tgbotapi.User{ID: 2},
			}},
		}
		if got := b.extractTargetPlayer(msg, ga); got != 2 {
			t.Errorf("text_mention resolved to %d, want 2", got)
		}
	})

	t.Run("username mention", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Text: "/accuse @bob",
			Entities: []tgbotapi.MessageEntity{{
				Type:   "mention",
				Offset: 8,
				Length: 4,
			}},
		}
		if got := b.extractTargetPlayer(msg, ga); got != 2 {
			t.Errorf("@bob resolved to %d, want 2", got)
		}
	})

	t.Run("unknown username", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Text: "/accuse @eve",
			Entities: []tgbotapi.MessageEntity{{
				Type:   "mention",
				Offset: 8,
				Length: 4,
			}},
		}
		if got := b.extractTargetPlayer(msg, ga); got != 0 {
			t.Errorf("an unknown @mention should not resolve, got %d", got)
		}
	})

	t.Run("no target", func(t *testing.T) {
		msg := &tgbotapi.Message{Text: "/accuse"}
		if got := b.extractTargetPlayer(msg, ga); got != 0 {
			t.Errorf("a command with no target resolved to %d", got)
		}
	})
}

// Every routed command must appear in /help commands so players can discover it.
func TestHelpListsEveryRoutedCommand(t *testing.T) {
	cmds := engine.FormatHelpCommands()
	required := []string{
		"/startgame", "/join", "/leave", "/begin", "/endgame",
		"/status", "/graveyard", "/roles", "/settings", "/set", "/schedule",
		"/accuse", "/defend", "/whisper", "/nominate", "/second", "/reveal",
		"/stats", "/leaderboard", "/achievements", "/lastgame",
		"/myrole", "/mafia", "/ghost", "/host", "/kick", "/help", "/guide",
	}
	for _, cmd := range required {
		if !strings.Contains(cmds, cmd) {
			t.Errorf("help commands does not mention %s", cmd)
		}
	}
}
