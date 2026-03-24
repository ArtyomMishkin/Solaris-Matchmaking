package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"solaris-matchmaking/internal/db"
	"solaris-matchmaking/internal/httpapi"
)

func main() {
	databaseURL := getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/solaris_matchmaking?sslmode=disable")

	database, err := db.OpenAndMigrate(databaseURL)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	defer database.Close()

	handler := httpapi.NewRouter(database)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("server started on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
