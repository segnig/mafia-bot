# Mafia Bot

A Telegram Mafia/Werewolf game bot built in Go with a deterministic, testable game engine decoupled from the transport layer.

## Architecture

```
cmd/bot/main.go          - Entry point
internal/engine/         - Pure state machine (no Telegram deps), fully unit-testable
internal/stats/          - Durable player records, derived purely from a finished game
internal/actor/          - Per-game goroutine supervisor (actor model)
internal/telegram/       - Transport layer: handlers, keyboards, rate-limited sender
internal/store/          - Persistence interface (MongoDB Atlas + in-memory impl)
```

`engine/` imports nothing from Telegram and `stats/` imports neither Telegram nor
a database, so the entire rules engine and the whole progression system can be
tested without a bot token or a running cluster.

## Running

```bash
export TELEGRAM_BOT_TOKEN="your-token-here"
export MONGODB_URI="mongodb+srv://user:password@cluster.mongodb.net"
export MONGODB_DB="mafia_bot"  # optional, defaults to "mafia_bot"
go run cmd/bot/main.go
```

With only those variables the bot **long-polls** (fine for local `go run`).
In production set a public HTTPS origin so Telegram pushes updates:

```bash
export WEBHOOK_URL="https://your-service.onrender.com"
export WEBHOOK_SECRET="a-long-random-string"
export PORT=8080
```

Telegram POSTs to `{WEBHOOK_URL}/telegram/webhook` with
`X-Telegram-Bot-Api-Secret-Token`. On Render a **Web Service** provides
`PORT` and `RENDER_EXTERNAL_URL`, so `WEBHOOK_URL` can be omitted there.

Values are also read from a `.env` file in the working directory, so local runs
need no exports.

### MongoDB Atlas Setup

1. Create a free cluster at [MongoDB Atlas](https://cloud.mongodb.com)
2. Create a database user with read/write access
3. Whitelist your server IP (or use `0.0.0.0/0` for development)
4. Copy the connection string and set it as `MONGODB_URI`

Collections and indexes are created automatically: `games`, `waitlists`,
`dm_confirmed`, `cooldowns`, `player_stats`, `game_history`, `chat_settings`.

`MONGODB_URI` is required to start the bot. The in-memory store implements the
same interface and is used by the test suite, but it is not wired into `main`,
since a bot that silently forgets every record on restart is worse than one that
refuses to boot.

## Deploy on Render

Use a **Web Service** so Telegram can POST webhook updates to a public HTTPS URL.

1. Push this repo to GitHub
2. Render → **New** → **Blueprint** → connect repo
3. Set `TELEGRAM_BOT_TOKEN`, `MONGODB_URI`, and `WEBHOOK_SECRET` in the dashboard
4. Deploy

Full guide: [docs/DEPLOY_RENDER.md](docs/DEPLOY_RENDER.md)

## Commands

| Command | Context | Description |
|---------|---------|-------------|
| `/startgame` | Group | Open a lobby |
| `/join` / `/leave` | Group | Join or leave the lobby |
| `/begin` | Group (host) | Start the game |
| `/endgame` | Group (host/admin) | Force-end the game |
| `/status` | Group | Phase, timer, alive and dead counts |
| `/graveyard` | Group | The dead, in the order they died |
| `/roles` | Group or DM | Full role reference |
| `/settings` | Group (host/admin, lobby only) | Configure rules before /begin |
| `/accuse`, `/defend`, `/whisper` | Group (alive) | Interactive discussion |
| `/nominate`, `/second` | Group (alive) | Trial mode |
| `/reveal` | Group (Mayor) | Trade anonymity for vote weight |
| `/stats [@player]` | Anywhere | Lifetime record |
| `/leaderboard [global]` | Group or DM | Best players here, or overall |
| `/achievements` | Anywhere | Unlocked and remaining achievements |
| `/lastgame` | Group | Recap of the group's last game |
| `/myrole` | DM | Re-send your role |
| `/mafia <msg>` | DM (mafia, night) | Private mafia team channel |
| `/ghost <msg>` | DM (dead) | Private channel for eliminated players |

## Testing

```bash
go test ./...            # everything
go test -race ./...      # concurrency
go vet ./... && gofmt -l .
```

## Features

**Game**
- Multiple concurrent games, one per group chat, fully isolated
- 14 roles across four factions: Town, Mafia, Killer, Neutral
- Lovers modifier — a paired player dies of grief when their partner does
- Ordered night resolution: roleblocks → visits → framing → protection → kills → information → grief
- Mayor day reveal with weighted voting, measured against total weight so it raises the bar for everyone
- Dynamic role generation with weighted random sampling over `crypto/rand`

**Live UI**
- One vote board rewritten in place: tally, bars, voters, turnout, countdown
- One-tap day mood bar, graveyard panel, and status panel
- Rematch, leaderboard, and recap buttons on the final card
- Inline `/settings` panel built from a settings registry, saved per chat

**Progression**
- Lifetime records: win rate, survival rate, streaks, per-role breakdown
- 23 achievements, including secret ones
- Per-chat and global leaderboards, scored so a single lucky game can't top them
- Full post-game recap with a night-by-night timeline and awards
- A host-cancelled game counts as neither a win nor a loss

**Reliability**
- Actor-model concurrency, no shared mutable state
- Crash recovery from persistent state snapshots
- Role DMs are confirmed by Telegram before Night 1; a blocked player is redealt, a flaky send only marks them silent
- Global and per-chat rate limiting, with retry and backoff
- Markdown escaping on every piece of user-supplied text
