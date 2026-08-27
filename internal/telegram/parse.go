package telegram

import (
	"strings"

	"github.com/segni/mafia-bot/internal/engine"
)

// whisperBody is the secret after the leading @mention has been stripped.
// A whisper sent as a reply has no mention in the arguments, so the whole
// remainder is the message.
func whisperBody(args string) string {
	body := strings.TrimSpace(args)
	if strings.HasPrefix(body, "@") {
		parts := strings.SplitN(body, " ", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
		return ""
	}
	return body
}

// usernameFromMention pulls the username out of a Telegram mention entity.
// Offset and length are in UTF-16-ish rune counts the way Telegram reports
// them for BMP text, which is what our player names are.
func usernameFromMention(text string, offset, length int) string {
	runes := []rune(text)
	if offset < 0 || length < 2 || offset+length > len(runes) {
		return ""
	}
	token := string(runes[offset : offset+length])
	return strings.TrimPrefix(token, "@")
}

func lookupPlayerByUsername(players map[engine.PlayerID]*engine.Player, username string) engine.PlayerID {
	for _, p := range players {
		if strings.EqualFold(p.Username, username) {
			return p.ID
		}
	}
	return 0
}

// isGlobalLeaderboard is the /leaderboard global switch. Anything else is the
// per-chat board, including an empty argument and a misspelling.
func isGlobalLeaderboard(args string) bool {
	return strings.EqualFold(strings.TrimSpace(args), "global")
}
