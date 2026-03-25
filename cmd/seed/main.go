package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"

	"solaris-matchmaking/internal/db"
)

func main() {
	databaseURL := getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/solaris_matchmaking?sslmode=disable")

	database, err := db.OpenAndMigrate(databaseURL)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	defer database.Close()

	if err := seed(database); err != nil {
		log.Fatalf("seed failed: %v", err)
	}

	fmt.Println("seed completed successfully")
}

func seed(database *sql.DB) error {
	now := time.Now().UTC().Format(time.RFC3339)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("StrongPass123!"), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	p1, err := upsertPlayer(tx, now, "Иван Петров", "WolfGuard", "Moscow", "@wolfguard", "North Club", string(passwordHash))
	if err != nil {
		return err
	}
	p2, err := upsertPlayer(tx, now, "Алексей Соколов", "JadePilot", "Saint-Petersburg", "@jadepilot", "South Club", string(passwordHash))
	if err != nil {
		return err
	}
	p3, err := upsertPlayer(tx, now, "Михаил Орлов", "MercFox", "Kazan", "@mercfox", "Central Club", string(passwordHash))
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`UPDATE player_credentials SET role = 'admin', updated_at = $1 WHERE player_id = $2`, now, p1); err != nil {
		return fmt.Errorf("promote admin: %w", err)
	}

	if err := upsertFactionExp(tx, p1, "Clan Wolf", 12); err != nil {
		return err
	}
	if err := upsertFactionExp(tx, p1, "Mercenaries", 3); err != nil {
		return err
	}
	if err := upsertFactionExp(tx, p2, "Clan Jade Falcon", 7); err != nil {
		return err
	}
	if err := upsertFactionExp(tx, p3, "Mercenaries", 5); err != nil {
		return err
	}

	// Demo casual lobby with two players.
	var lobbyID int64
	err = tx.QueryRow(`
INSERT INTO lobbies (host_player_id, faction, match_size, is_ranked, status, custom_mission_name, custom_weather_name, custom_atmosphere_name, created_at, updated_at)
VALUES ($1, 'Clan Wolf', 350, FALSE, 'open', 'Capture Base', 'Snow', 'Thin', $2, $2)
RETURNING id
`, p1, now).Scan(&lobbyID)
	if err != nil {
		return fmt.Errorf("insert demo lobby: %w", err)
	}

	if _, err := tx.Exec(`
INSERT INTO lobby_players (lobby_id, player_id, faction_name, is_ready, is_finished, joined_at)
VALUES ($1, $2, 'Clan Wolf', FALSE, FALSE, $3),
       ($1, $4, 'Clan Jade Falcon', FALSE, FALSE, $3)
ON CONFLICT (lobby_id, player_id) DO NOTHING
`, lobbyID, p1, now, p2); err != nil {
		return fmt.Errorf("insert demo lobby players: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed transaction: %w", err)
	}
	return nil
}

func upsertPlayer(tx *sql.Tx, now, fullName, nickname, city, contacts, location, passwordHash string) (int64, error) {
	factionsRaw, _ := json.Marshal([]string{})
	tournamentsRaw, _ := json.Marshal([]string{})
	hobbyRaw, _ := json.Marshal([]string{})
	otherRaw, _ := json.Marshal([]string{})

	var playerID int64
	err := tx.QueryRow(`
INSERT INTO players (full_name, nickname, city, contacts, preferred_location, factions, tournaments, hobby_evenings, other_events, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
ON CONFLICT (nickname) DO UPDATE
SET full_name = EXCLUDED.full_name, city = EXCLUDED.city, contacts = EXCLUDED.contacts, preferred_location = EXCLUDED.preferred_location, updated_at = EXCLUDED.updated_at
RETURNING id
`, fullName, nickname, city, contacts, location, string(factionsRaw), string(tournamentsRaw), string(hobbyRaw), string(otherRaw), now).Scan(&playerID)
	if err != nil {
		return 0, fmt.Errorf("upsert player %s: %w", nickname, err)
	}

	_, err = tx.Exec(`
INSERT INTO player_credentials (player_id, password_hash, role, created_at, updated_at)
VALUES ($1, $2, 'player', $3, $3)
ON CONFLICT (player_id) DO UPDATE
SET password_hash = EXCLUDED.password_hash, updated_at = EXCLUDED.updated_at
`, playerID, passwordHash, now)
	if err != nil {
		return 0, fmt.Errorf("upsert credentials %s: %w", nickname, err)
	}
	return playerID, nil
}

func upsertFactionExp(tx *sql.Tx, playerID int64, faction string, exp int) error {
	_, err := tx.Exec(`
INSERT INTO player_faction_experience (player_id, faction_name, experience)
VALUES ($1, $2, $3)
ON CONFLICT (player_id, faction_name) DO UPDATE
SET experience = EXCLUDED.experience
`, playerID, faction, exp)
	if err != nil {
		return fmt.Errorf("upsert faction experience %s: %w", faction, err)
	}
	return nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
