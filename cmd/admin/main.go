package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"solaris-matchmaking/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("usage: go run ./cmd/admin <make-admin|remove-admin|list-admins> [nickname]")
	}

	action := strings.TrimSpace(os.Args[1])
	if action == "" {
		log.Fatalf("action must not be empty")
	}

	databaseURL := getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/solaris_matchmaking?sslmode=disable")
	database, err := db.OpenAndMigrate(databaseURL)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	defer database.Close()

	switch action {
	case "make-admin":
		if len(os.Args) < 3 {
			log.Fatalf("usage: go run ./cmd/admin make-admin <nickname>")
		}
		nickname := strings.TrimSpace(os.Args[2])
		if nickname == "" {
			log.Fatalf("nickname must not be empty")
		}
		if err := db.PromotePlayerToAdmin(database, nickname); err != nil {
			log.Fatalf("make-admin failed: %v", err)
		}
		fmt.Printf("player %q is now admin\n", nickname)
	case "remove-admin":
		if len(os.Args) < 3 {
			log.Fatalf("usage: go run ./cmd/admin remove-admin <nickname>")
		}
		nickname := strings.TrimSpace(os.Args[2])
		if nickname == "" {
			log.Fatalf("nickname must not be empty")
		}
		if err := db.RemoveAdminFromPlayer(database, nickname); err != nil {
			log.Fatalf("remove-admin failed: %v", err)
		}
		fmt.Printf("player %q is no longer admin\n", nickname)
	case "list-admins":
		admins, err := db.ListAdmins(database)
		if err != nil {
			log.Fatalf("list-admins failed: %v", err)
		}
		if len(admins) == 0 {
			fmt.Println("no admins found")
			return
		}
		for _, n := range admins {
			fmt.Println(n)
		}
	default:
		log.Fatalf("usage: go run ./cmd/admin <make-admin|remove-admin|list-admins> [nickname]")
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
