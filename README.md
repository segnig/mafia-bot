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
go run cmd/bot/main.go
```

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
