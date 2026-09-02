package telegram

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/engine"
	"github.com/segni/mafia-bot/internal/store"
)

const schedulePollInterval = 30 * time.Second

func (b *Bot) startScheduleWatcher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(schedulePollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.fireDueSchedules()
			}
		}
	}()
}

func (b *Bot) fireDueSchedules() {
	due, err := b.store.ListDueScheduledGames(time.Now().UTC())
	if err != nil {
		log.Printf("schedule: list due failed: %v", err)
		return
	}
	for _, sg := range due {
		b.fireScheduledGame(sg)
	}
}

func (b *Bot) fireScheduledGame(sg *store.ScheduledGame) {
	if sg == nil {
		return
	}
	_ = b.store.DeleteScheduledGame(sg.ChatID)

	hostLabel := scheduledHostLabel(sg)
	gameID := gameIDForChat(sg.ChatID)
	if b.supervisor.GetGame(gameID) != nil {
		b.sender.SendText(sg.ChatID, fmt.Sprintf(
			"🗓️ Scheduled game for %s was skipped — a game is already in progress.",
			sg.ScheduledAt.UTC().Format("15:04 UTC")))
		return
	}

	state := engine.NewGameState(gameID, sg.ChatID, sg.HostID, b.newGameConfig(sg.ChatID))
	state.Players[sg.HostID] = &engine.Player{
		ID:          sg.HostID,
		Username:    sg.HostUsername,
		DisplayName: sg.HostName,
		Alive:       true,
		JoinedAt:    time.Now(),
	}

	ga := b.startGame(state)

	if waitlist, err := b.store.GetWaitlist(sg.ChatID); err == nil && len(waitlist) > 0 {
		b.sender.SendText(sg.ChatID, "📢 Scheduled game time! Waitlisted players: tap Join below!")
		_ = b.store.ClearWaitlist(sg.ChatID)
	}

	b.sender.SendText(sg.ChatID, fmt.Sprintf(
		"🗓️ *Scheduled game time!*\n\n%s is hosting. Tap **Join Lobby** or `/join`.",
		engine.EscapeMD(hostLabel)))
	ga.Send(engine.GameCreatedEvent{})
}

func scheduledHostLabel(sg *store.ScheduledGame) string {
	if sg.HostUsername != "" {
		return "@" + sg.HostUsername
	}
	if sg.HostName != "" {
		return sg.HostName
	}
	return fmt.Sprintf("player %d", sg.HostID)
}

func (b *Bot) cmdSchedule(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() {
		b.sender.SendDM(msg.Chat.ID, "Use `/schedule` in your group chat to plan the next game.")
		return
	}

	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		b.showScheduleStatus(msg)
		return
	}
	if strings.EqualFold(args, "cancel") {
		b.cancelSchedule(msg)
		return
	}

	if b.supervisor.GetGame(gameIDForChat(msg.Chat.ID)) != nil {
		b.sender.SendText(msg.Chat.ID, "A game is already running. Finish or `/endgame` it before scheduling the next one.")
		return
	}

	when, err := parseScheduleTime(args, time.Now().UTC())
	if err != nil {
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
			"Couldn't parse that time: %s\n\n*Usage:*\n`/schedule in 2h`\n`/schedule in 45m`\n`/schedule at 20:00` _(UTC)_\n`/schedule cancel`",
			engine.EscapeMD(err.Error())))
		return
	}

	hostID := engine.PlayerID(msg.From.ID)
	sg := &store.ScheduledGame{
		ChatID:       msg.Chat.ID,
		HostUsername: msg.From.UserName,
		HostName:     msg.From.FirstName,
		HostID:       hostID,
		ScheduledAt:  when,
		CreatedAt:    time.Now().UTC(),
	}
	if err := b.store.SaveScheduledGame(sg); err != nil {
		log.Printf("schedule: save failed for chat %d: %v", msg.Chat.ID, err)
		b.sender.SendText(msg.Chat.ID, "Couldn't save the schedule right now. Try again in a moment.")
		return
	}

	reply := fmt.Sprintf(
		"🗓️ Game scheduled for *%s* (%s).\n\nYou'll host when the lobby opens. Configure with `/settings` after it starts.\n\n`/schedule` to check · `/schedule cancel` to remove",
		when.UTC().Format("Mon Jan 2, 15:04 UTC"),
		formatScheduleWhen(when, time.Now().UTC()))

	if confirmed, _ := b.store.IsDMConfirmed(hostID); !confirmed {
		reply += "\n\n⚠️ DM me `/start` before then so you can receive your role."
	}
	b.sender.SendText(msg.Chat.ID, reply)
}

func (b *Bot) showScheduleStatus(msg *tgbotapi.Message) {
	sg, err := b.store.GetScheduledGame(msg.Chat.ID)
	if err != nil {
		log.Printf("schedule: load failed for chat %d: %v", msg.Chat.ID, err)
		b.sender.SendText(msg.Chat.ID, "Couldn't read the schedule right now.")
		return
	}
	if sg == nil {
		b.sender.SendText(msg.Chat.ID, "No game is scheduled in this group.\n\n`/schedule in 2h` · `/schedule at 20:00` _(UTC)_")
		return
	}

	now := time.Now().UTC()
	b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
		"🗓️ *Scheduled game*\n\nHost: %s\nWhen: *%s* (%s)\n\n`/schedule cancel` to remove",
		engine.EscapeMD(scheduledHostLabel(sg)),
		sg.ScheduledAt.UTC().Format("Mon Jan 2, 15:04 UTC"),
		formatScheduleWhen(sg.ScheduledAt, now)))
}

func (b *Bot) cancelSchedule(msg *tgbotapi.Message) {
	sg, err := b.store.GetScheduledGame(msg.Chat.ID)
	if err != nil {
		log.Printf("schedule: load failed for chat %d: %v", msg.Chat.ID, err)
		b.sender.SendText(msg.Chat.ID, "Couldn't read the schedule right now.")
		return
	}
	if sg == nil {
		b.sender.SendText(msg.Chat.ID, "Nothing to cancel — no game is scheduled.")
		return
	}

	hostID := engine.PlayerID(msg.From.ID)
	isHost := sg.HostID == hostID
	isAdmin := b.isGroupAdmin(msg.Chat.ID, msg.From.ID)
	if !isHost && !isAdmin {
		b.sender.SendText(msg.Chat.ID, "Only the scheduled host or a group admin can cancel.")
		return
	}

	if err := b.store.DeleteScheduledGame(msg.Chat.ID); err != nil {
		log.Printf("schedule: delete failed for chat %d: %v", msg.Chat.ID, err)
		b.sender.SendText(msg.Chat.ID, "Couldn't cancel the schedule right now.")
		return
	}
	b.sender.SendText(msg.Chat.ID, "🗓️ Scheduled game cancelled.")
}
