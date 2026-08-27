package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/segni/mafia-bot/internal/store"
	"github.com/segni/mafia-bot/internal/telegram"
)

func main() {
	// .env is for local dev only; Render injects env vars via dashboard
	if err := godotenv.Load(); err != nil && os.Getenv("RENDER") == "" {
		log.Println("No .env file found, using environment variables")
	}

	if os.Getenv("RENDER") != "" {
		log.SetFlags(log.LstdFlags | log.LUTC)
		log.Println("Running on Render")
	}

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable is required")
	}

	mongoURI := os.Getenv("MONGODB_URI")
	if mongoURI == "" {
		log.Fatal("MONGODB_URI environment variable is required (e.g. mongodb+srv://user:pass@cluster.mongodb.net)")
	}

	dbName := os.Getenv("MONGODB_DB")
	if dbName == "" {
		dbName = "mafia_bot"
	}

	mongoStore, err := connectMongoWithRetry(mongoURI, dbName, 5)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB Atlas: %v", err)
	}
	defer mongoStore.Close()
	log.Printf("Connected to MongoDB Atlas (db: %s)", dbName)

	bot, err := telegram.NewBot(token, mongoStore)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down gracefully...")
		bot.Stop()
		mongoStore.Close()
		os.Exit(0)
	}()

	cfg := listenConfigFromEnv()
	if cfg.WebhookURL != "" {
		log.Println("Starting Mafia Bot (webhook mode)...")
	} else {
		log.Println("Starting Mafia Bot (polling mode — set WEBHOOK_URL for production)")
	}
	if err := bot.Start(cfg); err != nil {
		log.Fatalf("bot stopped: %v", err)
	}
}

func listenConfigFromEnv() telegram.ListenConfig {
	cfg := telegram.ListenConfig{
		Secret:     os.Getenv("WEBHOOK_SECRET"),
		WebhookURL: strings.TrimRight(os.Getenv("WEBHOOK_URL"), "/"),
	}
	if cfg.WebhookURL == "" {
		// Render web services expose this automatically.
		cfg.WebhookURL = strings.TrimRight(os.Getenv("RENDER_EXTERNAL_URL"), "/")
	}
	if port := os.Getenv("PORT"); port != "" {
		cfg.Addr = ":" + port
	} else if cfg.WebhookURL != "" {
		cfg.Addr = ":8080"
	}
	return cfg
}

func connectMongoWithRetry(uri, dbName string, attempts int) (*store.MongoStore, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		s, err := store.NewMongoStore(uri, dbName)
		if err == nil {
			return s, nil
		}
		lastErr = err
		wait := time.Duration(i+1) * 3 * time.Second
		log.Printf("MongoDB connect attempt %d/%d failed: %v (retry in %s)", i+1, attempts, err, wait)
		time.Sleep(wait)
	}
	return nil, fmt.Errorf("after %d attempts: %w", attempts, lastErr)
}
