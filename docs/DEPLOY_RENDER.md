# Deploy on Render (Webhook)

This bot is configured for [Render](https://render.com) as a **Web Service**. Telegram pushes updates to `POST /telegram/webhook` over HTTPS.

## Why a Web Service?

| Service type | What Telegram needs | Good for this bot? |
|---|---|---|
| **Web Service** | A public HTTPS URL for `setWebhook` | ✅ **Required for webhook mode** |
| Background Worker | No inbound HTTP | ❌ Telegram cannot reach it |

On the **free** web plan the instance sleeps after ~15 minutes idle. The next Telegram update wakes it (cold start). Games resume from MongoDB; phase timers only run while the process is up. For reliable night/day clocks, use a paid instance that does not sleep.

## Prerequisites

1. **Telegram bot token** from [@BotFather](https://t.me/BotFather)
2. **MongoDB Atlas** free cluster ([cloud.mongodb.com](https://cloud.mongodb.com))
3. **GitHub repo** with this project pushed

## MongoDB Atlas (required)

Render uses dynamic outbound IPs. In Atlas this is the step that actually
unblocks the bot — without it the TLS handshake fails with
`remote error: tls: internal error` (Atlas drops the connection instead of
saying "IP not allowed").

1. **Network Access** → Add IP Address → **Allow Access from Anywhere** → `0.0.0.0/0`
2. **Database Access** → Create a user with read/write on your database
3. Copy the **Drivers** connection string:
   `mongodb+srv://USER:PASSWORD@cluster.mongodb.net/`
   URL-encode special characters in the password (`@` → `%40`, `#` → `%23`).
4. In Render, set `MONGODB_URI` to that string. Do not wrap it in quotes.

## Deploy with Blueprint (easiest)

1. Push code to GitHub
2. Render Dashboard → **New** → **Blueprint**
3. Connect your repo — Render reads `render.yaml`
4. Set secret env vars when prompted:
   - `TELEGRAM_BOT_TOKEN`
   - `MONGODB_URI`
   - `WEBHOOK_SECRET` (any long random string; Telegram's `secret_token`)
5. Click **Apply** → wait for deploy

Render sets `PORT` and `RENDER_EXTERNAL_URL`. The bot registers
`{RENDER_EXTERNAL_URL}/telegram/webhook` with Telegram on boot.

## Deploy manually

1. Render Dashboard → **New** → **Web Service**
2. Connect GitHub repo
3. Settings:

| Field | Value |
|---|---|
| **Runtime** | Go |
| **Build Command** | `go build -ldflags="-s -w" -o bot ./cmd/bot` |
| **Start Command** | `./bot` |
| **Health Check Path** | `/health` |
| **Plan** | Free, or paid if you need timers that never pause |

4. Environment variables:

| Key | Value |
|---|---|
| `TELEGRAM_BOT_TOKEN` | Your BotFather token |
| `MONGODB_URI` | MongoDB Atlas connection string |
| `MONGODB_DB` | `mafia_bot` (optional) |
| `WEBHOOK_SECRET` | Long random string (recommended) |
| `WEBHOOK_URL` | Optional override; defaults to `RENDER_EXTERNAL_URL` |
| `GODEBUG` | `tlsrsakex=1` (needed for Atlas TLS on Go 1.22; set in Docker image and `render.yaml`) |
| `RENDER` | `true` (optional, enables production logging) |

5. **Create Web Service**

## Deploy with Docker (alternative)

1. New → Web Service
2. **Language** → Docker
3. Dockerfile path: `./Dockerfile`
4. Same environment variables as above

## Verify deployment

Check Render logs for:

```
Connected to MongoDB Atlas (db: mafia_bot)
Bot started as @YourBotName
Telegram webhook registered at https://….onrender.com/telegram/webhook
webhook listening on :10000 for https://….onrender.com/telegram/webhook
```

Open `https://your-service.onrender.com/health` — it should return `ok`.

Test in Telegram:
1. DM the bot → `/start`
2. In a group → `/startgame`

## Local development

Without `WEBHOOK_URL`, `go run cmd/bot/main.go` **deletes any webhook** and long-polls, so you do not need HTTPS on your laptop. Do not run polling against a bot that is also serving production webhooks — Telegram allows only one delivery mode per bot.

## Troubleshooting

| Problem | Fix |
|---|---|
| `tls: internal error` / `ReplicaSetNoPrimary` | Atlas Network Access must allow `0.0.0.0/0`. Then redeploy. Also set `GODEBUG=tlsrsakex=1` (already in `render.yaml`). URL-encode the password in `MONGODB_URI`. |
| Bot not responding | Confirm the service is a **Web Service** (not a Worker) and `/health` is 200 |
| `setWebhook` error | `WEBHOOK_URL` / `RENDER_EXTERNAL_URL` must be `https://…` with no trailing path |
| 401s in logs | `WEBHOOK_SECRET` changed after Telegram registered the old secret — restart so `setWebhook` runs again |
| `TELEGRAM_BOT_TOKEN required` | Add env var in Render dashboard → Environment |
| Games lost on restart | Verify `MONGODB_URI` is set — without it bot won't persist |
| Night timer felt frozen | Free web instance was asleep; upgrade the plan or ping `/health` |

## Updating

Push to your connected branch — Render auto-redeploys. The new process calls `setWebhook` again and resumes active games from MongoDB.
