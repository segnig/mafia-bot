package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/segni/mafia-bot/internal/store"
	"github.com/segni/mafia-bot/internal/telegram"
)

func main() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
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

	mongoStore, err := store.NewMongoStore(mongoURI, dbName)
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
		log.Println("Shutting down...")
		bot.Stop()
		mongoStore.Close()
		os.Exit(0)
	}()

	log.Println("Starting Mafia Bot...")
	bot.Start()
}
