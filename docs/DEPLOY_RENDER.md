# Deploy on Render (Free Tier)

This bot is configured for [Render](https://render.com) free tier using a **Background Worker**.

## Why Background Worker?

Telegram bots use **long polling** (always listening for updates). On Render free:

| Service type | Free tier behavior | Good for this bot? |
|---|---|---|
| **Background Worker** | Runs continuously (within 750 hrs/month) | ✅ **Recommended** |
| Web Service | Sleeps after ~15 min without HTTP traffic | ❌ Bot stops receiving updates |

## Prerequisites

1. **Telegram bot token** from [@BotFather](https://t.me/BotFather)
2. **MongoDB Atlas** free cluster ([cloud.mongodb.com](https://cloud.mongodb.com))
3. **GitHub repo** with this project pushed

## MongoDB Atlas (required for Render)

Render uses dynamic outbound IPs. In Atlas:

1. **Network Access** → Add IP Address → `0.0.0.0/0` (allow from anywhere)
2. **Database Access** → Create a user with read/write on your database
3. Copy connection string: `mongodb+srv://user:pass@cluster.mongodb.net`

## Deploy with Blueprint (easiest)

1. Push code to GitHub
2. Render Dashboard → **New** → **Blueprint**
3. Connect your repo — Render reads `render.yaml`
4. Set secret env vars when prompted:
   - `TELEGRAM_BOT_TOKEN`
   - `MONGODB_URI`
5. Click **Apply** → wait for deploy

## Deploy manually

1. Render Dashboard → **New** → **Background Worker**
2. Connect GitHub repo
3. Settings:

| Field | Value |
|---|---|
| **Runtime** | Go |
| **Build Command** | `go build -ldflags="-s -w" -o bot ./cmd/bot` |
| **Start Command** | `./bot` |
| **Plan** | Free |

4. Environment variables:

| Key | Value |
|---|---|
| `TELEGRAM_BOT_TOKEN` | Your BotFather token |
| `MONGODB_URI` | MongoDB Atlas connection string |
| `MONGODB_DB` | `mafia_bot` (optional) |
| `RENDER` | `true` (optional, enables production logging) |

5. **Create Background Worker**

## Deploy with Docker (alternative)

1. New → Background Worker
2. **Language** → Docker
3. Dockerfile path: `./Dockerfile`
4. Same environment variables as above

## Verify deployment

Check Render logs for:

```
Connected to MongoDB Atlas (db: mafia_bot)
Bot started as @YourBotName
Starting Mafia Bot (worker mode)...
```

Test in Telegram:
1. DM the bot → `/start`
2. In a group → `/startgame`

## Free tier limits

- **750 instance hours/month** — one worker 24/7 uses ~720 hrs (fits)
- **512 MB RAM** — sufficient for this bot
- **No persistent disk** — all state is in MongoDB Atlas (already implemented)
- **Cold starts** on redeploy — active games auto-resume from MongoDB

## Troubleshooting

| Problem | Fix |
|---|---|
| `Failed to connect to MongoDB` | Check Atlas IP whitelist (`0.0.0.0/0`), user/password in URI |
| Bot not responding | Confirm service type is **Worker**, not Web Service |
| `TELEGRAM_BOT_TOKEN required` | Add env var in Render dashboard → Environment |
| Deploy fails on build | Check Render logs; ensure `go.mod` is committed |
| Games lost on restart | Verify `MONGODB_URI` is set — without it bot won't persist |

## Updating

Push to your connected branch — Render auto-redeploys. Games in progress resume automatically after restart.
