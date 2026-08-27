package telegram

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	webhookPath   = "/telegram/webhook"
	secretHeader  = "X-Telegram-Bot-Api-Secret-Token"
	maxUpdateBody = 1 << 20 // Telegram updates are small; this is a flood ceiling
)

// ListenConfig chooses how Telegram delivers updates.
//
// A public WebhookURL (https://…) registers a webhook and serves it on Addr.
// An empty WebhookURL deletes any webhook and long-polls instead, which is
// what local development uses when there is no HTTPS endpoint.
type ListenConfig struct {
	Addr       string // e.g. ":8080"
	WebhookURL string // public origin, e.g. https://mafia-bot.onrender.com
	Secret     string // Telegram secret_token; derived from the bot token if empty
}

func webhookEndpoint(publicBase string) string {
	return strings.TrimRight(publicBase, "/") + webhookPath
}

// deriveWebhookSecret is a stable secret when none is configured, so Telegram
// still sends X-Telegram-Bot-Api-Secret-Token and random POSTs to the path
// are rejected. It is not a substitute for setting WEBHOOK_SECRET in production.
func deriveWebhookSecret(token string) string {
	sum := sha256.Sum256([]byte("mafia-bot-webhook:" + token))
	return hex.EncodeToString(sum[:16])
}

func secretsEqual(got, want string) bool {
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func decodeUpdate(r io.Reader) (tgbotapi.Update, error) {
	var update tgbotapi.Update
	dec := json.NewDecoder(io.LimitReader(r, maxUpdateBody))
	if err := dec.Decode(&update); err != nil {
		return tgbotapi.Update{}, err
	}
	return update, nil
}

func (b *Bot) serveWebhook(cfg ListenConfig) error {
	secret := cfg.Secret
	if secret == "" {
		secret = deriveWebhookSecret(b.api.Token)
	}

	endpoint := webhookEndpoint(cfg.WebhookURL)
	if err := b.registerWebhook(endpoint, secret); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Mafia Bot is running"))
	})
	mux.Handle(webhookPath, b.webhookHandler(secret))

	addr := cfg.Addr
	if addr == "" {
		addr = ":8080"
	}
	b.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("webhook listening on %s for %s", addr, endpoint)
	err := b.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (b *Bot) registerWebhook(endpoint, secret string) error {
	params := tgbotapi.Params{
		"url":             endpoint,
		"secret_token":    secret,
		"max_connections": "40",
	}
	if _, err := b.api.MakeRequest("setWebhook", params); err != nil {
		return fmt.Errorf("setWebhook: %w", err)
	}
	log.Printf("Telegram webhook registered at %s", endpoint)
	return nil
}

func (b *Bot) webhookHandler(secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !secretsEqual(r.Header.Get(secretHeader), secret) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		update, err := decodeUpdate(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Telegram retries if this takes too long. The actor inbox is the
		// real work queue, so answer first and dispatch off the request.
		w.WriteHeader(http.StatusOK)
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("webhook dispatch panic: %v", rec)
				}
			}()
			b.dispatchUpdate(update)
		}()
	})
}
