package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"solaris-matchmaking/internal/db"
	"solaris-matchmaking/internal/httpapi"
	"solaris-matchmaking/internal/logx"
)

func main() {
	logx.SetupFromEnv()

	databaseURL := getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/solaris_matchmaking?sslmode=disable")

	database, err := db.OpenAndMigrate(databaseURL)
	if err != nil {
		slog.Error("database init failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	handler := logx.HTTPAccess(httpapi.NewRouter(database))

	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("server started", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
