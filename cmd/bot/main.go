package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/segni/mafia-bot/internal/store"
	"github.com/segni/mafia-bot/internal/telegram"
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable is required")
	}

	st := store.NewMemoryStore()

	bot, err := telegram.NewBot(token, st)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down...")
		bot.Stop()
		os.Exit(0)
	}()

	log.Println("Starting Mafia Bot...")
	bot.Start()
}
