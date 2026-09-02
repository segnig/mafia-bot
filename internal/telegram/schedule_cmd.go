package telegram

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/segni/mafia-bot/internal/actor"
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
				b.tickSchedules()
			}
		}
	}()
}

func (b *Bot) tickSchedules() {
	now := time.Now().UTC()
	b.refreshAllScheduleCards(now)
	b.fireDueSchedules(now)
	b.sendScheduleCountdownReminders(now)
}

func (b *Bot) fireDueSchedules(now time.Time) {
	due, err := b.store.ListDueScheduledGames(now)
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

	cardID := sg.CardMessageID
	chatID := sg.ChatID
	_ = b.store.DeleteScheduledGame(chatID)

	if cardID != 0 {
		b.sender.EditKeyboardMessage(chatID, cardID,
			"🗓️ *Scheduled game starting now!*",
			tgbotapi.NewInlineKeyboardMarkup())
	}

	hostLabel := scheduledHostLabel(sg)
	gameID := gameIDForChat(sg.ChatID)
	if b.supervisor.GetGame(gameID) != nil {
		b.sender.SendText(sg.ChatID, fmt.Sprintf(
			"🗓️ Scheduled game for %s was skipped — a game is already in progress.",
			formatScheduleInstant(sg.ScheduledAt)))
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
	ga.Send(engine.GameCreatedEvent{})

	joined, needStart := b.autoJoinScheduleSignups(ga, sg)
	joined += b.autoJoinWaitlist(ga, sg.ChatID)

	var extra string
	if joined > 0 {
		extra = fmt.Sprintf("\n\n✅ *%d* signed-up player(s) joined automatically.", joined)
	}
	if len(needStart) > 0 {
		names := make([]string, 0, len(needStart))
		for _, s := range needStart {
			names = append(names, engine.EscapeMD(scheduleSignupLabel(s)))
		}
		extra += fmt.Sprintf("\n\n⚠️ These signed-up players still need to DM `/start`, then `/join`: %s",
			strings.Join(names, ", "))
	}

	b.sender.SendText(sg.ChatID, fmt.Sprintf(
		"🗓️ *Scheduled game time!*\n\n%s is hosting. Tap **Join Lobby** or `/join` — *anyone* in the group can join now.\n\n_%d signed up before start time._%s",
		engine.EscapeMD(hostLabel), sg.PlayerCount(), extra))
}

func scheduleSignupLabel(s store.ScheduleSignup) string {
	if s.Username != "" {
		return "@" + s.Username
	}
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return fmt.Sprintf("player %d", s.PlayerID)
}

// formatSchedulePlayersSummary is a one-line player count for reminders and signups.
func formatSchedulePlayersSummary(sg *store.ScheduledGame) string {
	n := sg.PlayerCount()
	if n == 1 {
		return "👥 *1 player* (host only) — `/join` to sign up"
	}
	return fmt.Sprintf("👥 *%d players* (host + %d signed up)", n, len(sg.Signups))
}

// formatSchedulePlayersDetail lists everyone signed up for /schedule status.
func formatSchedulePlayersDetail(sg *store.ScheduledGame) string {
	lines := []string{
		fmt.Sprintf("👥 *%d players*", sg.PlayerCount()),
		fmt.Sprintf("  • %s _(host)_", engine.EscapeMD(scheduledHostLabel(sg))),
	}
	for _, s := range sg.Signups {
		lines = append(lines, fmt.Sprintf("  • %s", engine.EscapeMD(scheduleSignupLabel(s))))
	}
	if len(sg.Signups) == 0 {
		lines = append(lines, "_No one else signed up yet — `/join` to be first._")
	}
	return strings.Join(lines, "\n")
}

// formatScheduleCardText is the scheduled-game card body shown with the Join button.
func formatScheduleCardText(sg *store.ScheduledGame, now time.Time) string {
	return fmt.Sprintf(
		"🗓️ *Scheduled game*\n\nHost: %s\nWhen: *%s*\n⏳ Countdown: *%s*\n\n%s\n\n_Tap **Join** below, or type `/join`._",
		engine.EscapeMD(scheduledHostLabel(sg)),
		formatScheduleInstant(sg.ScheduledAt),
		formatScheduleCountdown(sg.ScheduledAt, now),
		formatSchedulePlayersDetail(sg))
}

func (b *Bot) postScheduleCard(sg *store.ScheduledGame) {
	if sg == nil {
		return
	}
	text := formatScheduleCardText(sg, time.Now().UTC())
	keyboard := buildScheduleJoinKeyboard(sg.ChatID)
	b.sender.SendTrackedKeyboard(sg.ChatID, text, keyboard, func(messageID int) {
		sg.CardMessageID = messageID
		if err := b.store.SaveScheduledGame(sg); err != nil {
			log.Printf("schedule: save card message id for chat %d: %v", sg.ChatID, err)
		}
	})
}

func (b *Bot) refreshScheduleCard(chatID int64) {
	sg, err := b.store.GetScheduledGame(chatID)
	if err != nil || sg == nil || sg.CardMessageID == 0 {
		return
	}
	text := formatScheduleCardText(sg, time.Now().UTC())
	keyboard := buildScheduleJoinKeyboard(chatID)
	b.sender.EditKeyboardMessage(chatID, sg.CardMessageID, text, keyboard)
}

func (b *Bot) refreshAllScheduleCards(now time.Time) {
	upcoming, err := b.store.ListUpcomingScheduledGames(now)
	if err != nil {
		return
	}
	for _, sg := range upcoming {
		if sg == nil || sg.CardMessageID == 0 {
			continue
		}
		text := formatScheduleCardText(sg, now)
		keyboard := buildScheduleJoinKeyboard(sg.ChatID)
		b.sender.EditKeyboardMessage(sg.ChatID, sg.CardMessageID, text, keyboard)
	}
}

func (b *Bot) addScheduleSignup(chatID int64, playerID engine.PlayerID, username, displayName string) (toast string, ok bool) {
	sg, err := b.store.GetScheduledGame(chatID)
	if err != nil || sg == nil {
		return "No scheduled game here.", false
	}
	if playerID == sg.HostID {
		return "You're the host — lobby opens automatically.", true
	}
	if !sg.AddSignup(playerID, username, displayName) {
		return fmt.Sprintf("Already signed up — %d players total.", sg.PlayerCount()), true
	}
	if err := b.store.SaveScheduledGame(sg); err != nil {
		log.Printf("schedule: save signup failed for chat %d: %v", chatID, err)
		return "Couldn't save your signup.", false
	}
	b.refreshScheduleCard(chatID)
	return fmt.Sprintf("Signed up! %d players now.", sg.PlayerCount()), true
}

func (b *Bot) handleSchedJoinCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) < 2 {
		b.answerCallback(cq, "")
		return
	}
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.answerCallback(cq, "")
		return
	}

	toast, ok := b.addScheduleSignup(chatID, engine.PlayerID(cq.From.ID), cq.From.UserName, cq.From.FirstName)
	if ok && cq.From != nil {
		if confirmed, _ := b.store.IsDMConfirmed(engine.PlayerID(cq.From.ID)); !confirmed {
			toast += " — DM /start first"
		}
	}
	b.answerCallback(cq, toast)
}

func (b *Bot) handleSchedInfoCallback(cq *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) < 2 {
		b.answerCallback(cq, "")
		return
	}
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.answerCallback(cq, "")
		return
	}
	sg, err := b.store.GetScheduledGame(chatID)
	if err != nil || sg == nil {
		b.answerCallback(cq, "No scheduled game.")
		return
	}
	b.refreshScheduleCard(chatID)
	b.answerCallback(cq, fmt.Sprintf("%d players · %s left",
		sg.PlayerCount(),
		formatScheduleCountdown(sg.ScheduledAt, time.Now().UTC())))
}

func (b *Bot) autoJoinScheduleSignups(ga *actor.GameActor, sg *store.ScheduledGame) (joined int, needStart []store.ScheduleSignup) {
	if sg == nil {
		return 0, nil
	}
	for _, signup := range sg.Signups {
		if signup.PlayerID == sg.HostID {
			continue
		}
		if confirmed, _ := b.store.IsDMConfirmed(signup.PlayerID); !confirmed {
			needStart = append(needStart, signup)
			continue
		}
		ga.Send(engine.JoinEvent{
			PlayerID:    signup.PlayerID,
			Username:    signup.Username,
			DisplayName: signup.DisplayName,
			Time:        time.Now(),
		})
		joined++
	}
	return joined, needStart
}

func (b *Bot) autoJoinWaitlist(ga *actor.GameActor, chatID int64) int {
	waitlist, err := b.store.GetWaitlist(chatID)
	if err != nil || len(waitlist) == 0 {
		return 0
	}
	joined := 0
	for _, pid := range waitlist {
		if confirmed, _ := b.store.IsDMConfirmed(pid); !confirmed {
			continue
		}
		ga.Send(engine.JoinEvent{
			PlayerID: pid,
			Time:     time.Now(),
		})
		joined++
	}
	_ = b.store.ClearWaitlist(chatID)
	return joined
}

func (b *Bot) signupForScheduledGame(msg *tgbotapi.Message) bool {
	sg, err := b.store.GetScheduledGame(msg.Chat.ID)
	if err != nil || sg == nil {
		return false
	}

	toast, ok := b.addScheduleSignup(msg.Chat.ID, engine.PlayerID(msg.From.ID), msg.From.UserName, msg.From.FirstName)
	reply := toast
	if ok && msg.From != nil {
		if confirmed, _ := b.store.IsDMConfirmed(engine.PlayerID(msg.From.ID)); !confirmed {
			reply += "\n\n⚠️ DM me `/start` before then so you can receive your role."
		}
	}
	b.sender.SendText(msg.Chat.ID, reply)
	return true
}

func (b *Bot) sendScheduleCountdownReminders(now time.Time) {
	upcoming, err := b.store.ListUpcomingScheduledGames(now)
	if err != nil {
		log.Printf("schedule: list upcoming failed: %v", err)
		return
	}
	for _, sg := range upcoming {
		b.maybeSendScheduleReminder(sg, now)
	}
}

func (b *Bot) maybeSendScheduleReminder(sg *store.ScheduledGame, now time.Time) {
	if sg == nil {
		return
	}
	remaining := sg.ScheduledAt.Sub(now)
	host := engine.EscapeMD(scheduledHostLabel(sg))

	type threshold struct {
		within   time.Duration
		sent     *bool
		label    string
	}
	checks := []threshold{
		{5 * time.Minute, &sg.Reminder5m, "5 minutes"},
		{15 * time.Minute, &sg.Reminder15m, "15 minutes"},
		{time.Hour, &sg.Reminder1h, "1 hour"},
	}

	var fired *threshold
	for i := range checks {
		t := &checks[i]
		if remaining <= t.within && !*t.sent {
			fired = t
			break
		}
	}
	if fired == nil {
		return
	}

	*fired.sent = true
	if err := b.store.SaveScheduledGame(sg); err != nil {
		log.Printf("schedule: save reminder flag failed for chat %d: %v", sg.ChatID, err)
		return
	}

	b.sender.SendText(sg.ChatID, fmt.Sprintf(
		"⏳ *Countdown:* scheduled game in *%s*\n\n%s will host at *%s*.\n%s\n\nAnyone can `/join` to sign up.",
		fired.label,
		host,
		formatScheduleInstant(sg.ScheduledAt),
		formatSchedulePlayersSummary(sg)))
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
			"Couldn't parse that time: %s\n\n*Usage:*\n`/schedule in 2h`\n`/schedule in 45m`\n`/schedule at 20:00` _(%s)_\n`/schedule cancel`",
			engine.EscapeMD(err.Error()), scheduleZoneLabel))
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

	b.postScheduleCard(sg)

	if confirmed, _ := b.store.IsDMConfirmed(hostID); !confirmed {
		b.sender.SendText(msg.Chat.ID, "⚠️ DM me `/start` before then so you can receive your role.")
	}
}

func (b *Bot) showScheduleStatus(msg *tgbotapi.Message) {
	sg, err := b.store.GetScheduledGame(msg.Chat.ID)
	if err != nil {
		log.Printf("schedule: load failed for chat %d: %v", msg.Chat.ID, err)
		b.sender.SendText(msg.Chat.ID, "Couldn't read the schedule right now.")
		return
	}
	if sg == nil {
		b.sender.SendText(msg.Chat.ID, fmt.Sprintf("No game is scheduled in this group.\n\n`/schedule in 2h` · `/schedule at 20:00` _(%s)_", scheduleZoneLabel))
		return
	}

	b.refreshScheduleCard(msg.Chat.ID)

	now := time.Now().UTC()
	b.sender.SendText(msg.Chat.ID, fmt.Sprintf(
		"🗓️ *Scheduled game*\n\nHost: %s\nWhen: *%s*\n⏳ Countdown: *%s*\n\n%s\n\n_Schedule card updated above — tap **Join** there._",
		engine.EscapeMD(scheduledHostLabel(sg)),
		formatScheduleInstant(sg.ScheduledAt),
		formatScheduleCountdown(sg.ScheduledAt, now),
		formatSchedulePlayersDetail(sg)))
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

	if sg.CardMessageID != 0 {
		b.sender.EditKeyboardMessage(msg.Chat.ID, sg.CardMessageID,
			"🗓️ Scheduled game cancelled.",
			tgbotapi.NewInlineKeyboardMarkup())
	}

	if err := b.store.DeleteScheduledGame(msg.Chat.ID); err != nil {
		log.Printf("schedule: delete failed for chat %d: %v", msg.Chat.ID, err)
		b.sender.SendText(msg.Chat.ID, "Couldn't cancel the schedule right now.")
		return
	}
	b.sender.SendText(msg.Chat.ID, "🗓️ Scheduled game cancelled.")
}
