package telegram

import (
	"log"

	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/stats"
	"github.com/segni/mafia-bot/internal/store"
)

// recordResults turns a finished game into durable records: the game goes into
// the chat's history, every participant's lifetime record is updated, and
// anyone who unlocked something is told about it.
//
// A failure here must never take the bot down or block the group's next game,
// so each step is logged and skipped rather than aborting the rest.
func recordResults(st store.Store, sender *Sender, summary engine.GameSummary) {
	// A game that never dealt roles has nothing worth recording.
	if len(summary.Players) == 0 || summary.Days == 0 {
		return
	}

	record := stats.RecordFromSummary(summary)
	if err := st.SaveGameRecord(record); err != nil {
		log.Printf("results: failed to archive game %s: %v", summary.GameID, err)
	}

	for _, result := range summary.Players {
		// A player who never received a role was not really in this game.
		if result.Role == engine.RoleUnassigned {
			continue
		}

		playerStats, err := st.LoadPlayerStats(result.ID)
		if err != nil {
			log.Printf("results: failed to load stats for %d: %v", result.ID, err)
			continue
		}
		if playerStats == nil {
			playerStats = stats.NewPlayerStats(result.ID)
		}

		earned := playerStats.Apply(summary, result)
		if err := st.SavePlayerStats(playerStats); err != nil {
			log.Printf("results: failed to save stats for %d: %v", result.ID, err)
			continue
		}

		if len(earned) > 0 && sender != nil {
			sender.SendDM(int64(result.ID), stats.FormatUnlockDM(earned))
		}
	}
}
