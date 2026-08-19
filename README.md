# Mafia Bot

A Telegram Mafia/Werewolf game bot built in Go with a deterministic, testable game engine decoupled from the transport layer.

## Architecture

```
cmd/bot/main.go          - Entry point
internal/engine/         - Pure state machine (no Telegram deps), fully unit-testable
internal/actor/          - Per-game goroutine supervisor (actor model)
internal/telegram/       - Transport layer: handlers, keyboards, rate-limited sender
internal/store/          - Persistence interface (in-memory impl included)
```

## Running

```bash
export TELEGRAM_BOT_TOKEN="your-token-here"
export MONGODB_URI="mongodb+srv://user:password@cluster.mongodb.net"
export MONGODB_DB="mafia_bot"  # optional, defaults to "mafia_bot"
go run cmd/bot/main.go
```

### MongoDB Atlas Setup

1. Create a free cluster at [MongoDB Atlas](https://cloud.mongodb.com)
2. Create a database user with read/write access
3. Whitelist your server IP (or use `0.0.0.0/0` for development)
4. Copy the connection string and set it as `MONGODB_URI`

Collections created automatically: `games`, `waitlists`, `dm_confirmed`, `cooldowns`

## Deploy on Render (Free Tier)

Use a **Background Worker** (not Web Service) for 24/7 Telegram polling.

1. Push this repo to GitHub
2. Render → **New** → **Blueprint** → connect repo
3. Set `TELEGRAM_BOT_TOKEN` and `MONGODB_URI` in the dashboard
4. Deploy

Full guide: [docs/DEPLOY_RENDER.md](docs/DEPLOY_RENDER.md)

## Commands

| Command | Context | Description |
|---------|---------|-------------|
| `/startgame` | Group | Opens a lobby |
| `/join` | Group | Join the lobby |
| `/leave` | Group | Leave the lobby |
| `/begin` | Group (host) | Start the game |
| `/endgame` | Group (host) | Force-end the game |
| `/status` | Group | Show game status |
| `/myrole` | DM | Re-send your role |

## Testing

```bash
go test ./internal/engine/ -v
```

## Features

- Multiple concurrent games (one per group chat)
- Roles: Villager, Mafia, Detective, Doctor, Godfather, Vigilante, Jester
- Dynamic role generation with weighted random sampling (crypto/rand)
- Night actions via DM, public voting via inline keyboards
- Timer-driven phase transitions
- Actor-model concurrency (no shared mutable state)
- Crash recovery via persistent state snapshots
