package main

import (
	"context"
	"log"
	"time"

	"github.com/ArminDashti/chatbot-api/internal/auth"
	"github.com/ArminDashti/chatbot-api/internal/config"
	httpserver "github.com/ArminDashti/chatbot-api/internal/http"
	"github.com/ArminDashti/chatbot-api/internal/llm"
	"github.com/ArminDashti/chatbot-api/internal/store"
)

func main() {
	config.LoadDotEnv(".env")
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	sqlDB, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer sqlDB.Close()

	if err := store.Migrate(sqlDB, cfg.MigrationsDir); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	hash, err := auth.HashPassword(store.DefaultPassword)
	if err != nil {
		log.Fatalf("hash default password: %v", err)
	}
	if err := store.SeedDefaultUser(ctx, sqlDB, hash); err != nil {
		log.Fatalf("seed default user: %v", err)
	}

	chat := llm.New(cfg.ChatBaseURL, cfg.ChatAPIKey, cfg.ChatModel)
	srv := httpserver.New(cfg, sqlDB, chat)
	log.Printf("chatbot-api listening on %s", cfg.Addr)
	if err := srv.Router().Run(cfg.Addr); err != nil {
		log.Fatal(err)
	}
}
