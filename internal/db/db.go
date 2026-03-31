package db

import (
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func OpenAndMigrate(databaseURL string) (*sql.DB, error) {
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	if err := migrate(database); err != nil {
		_ = database.Close()
		return nil, err
	}

	return database, nil
}

func migrate(database *sql.DB) error {
	_, err := database.Exec(`
CREATE TABLE IF NOT EXISTS players (
    id BIGSERIAL PRIMARY KEY,
    full_name TEXT NOT NULL,
    nickname TEXT NOT NULL UNIQUE,
    city TEXT NOT NULL,
    contacts TEXT NOT NULL,
    preferred_location TEXT NOT NULL,

    rank_title TEXT,
    rank_attested_at TEXT,
    factions TEXT NOT NULL DEFAULT '[]',
    tournaments TEXT NOT NULL DEFAULT '[]',
    hobby_evenings TEXT NOT NULL DEFAULT '[]',
    total_experience INTEGER NOT NULL DEFAULT 0,
    rating INTEGER NOT NULL DEFAULT 1500,
    rating_rd DOUBLE PRECISION NOT NULL DEFAULT 350,
    other_events TEXT NOT NULL DEFAULT '[]',
    collection_link TEXT,

    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("migrate players table: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE players
ADD COLUMN IF NOT EXISTS rating INTEGER NOT NULL DEFAULT 1500;
`)
	if err != nil {
		return fmt.Errorf("add players.rating: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE players
ADD COLUMN IF NOT EXISTS rating_rd DOUBLE PRECISION NOT NULL DEFAULT 350;
`)
	if err != nil {
		return fmt.Errorf("add players.rating_rd: %w", err)
	}

	_, err = database.Exec(`
CREATE TABLE IF NOT EXISTS player_credentials (
    id BIGSERIAL PRIMARY KEY,
    player_id BIGINT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'player',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (player_id) REFERENCES players(id) ON DELETE CASCADE
);
`)
	if err != nil {
		return fmt.Errorf("migrate player_credentials table: %w", err)
	}

	_, err = database.Exec(`
CREATE TABLE IF NOT EXISTS lobbies (
    id BIGSERIAL PRIMARY KEY,
    host_player_id BIGINT NOT NULL,
    faction TEXT NOT NULL,
    match_size INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (host_player_id) REFERENCES players(id)
);
`)
	if err != nil {
		return fmt.Errorf("migrate lobbies table: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS is_ranked BOOLEAN NOT NULL DEFAULT FALSE;
`)
	if err != nil {
		return fmt.Errorf("add lobbies.is_ranked: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS mission_condition_id BIGINT;
`)
	if err != nil {
		return fmt.Errorf("add lobbies.mission_condition_id: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS custom_mission_name TEXT;
`)
	if err != nil {
		return fmt.Errorf("add lobbies.custom_mission_name: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS custom_weather_name TEXT;
`)
	if err != nil {
		return fmt.Errorf("add lobbies.custom_weather_name: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS custom_atmosphere_name TEXT;
`)
	if err != nil {
		return fmt.Errorf("add lobbies.custom_atmosphere_name: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS started_at TEXT;
`)
	if err != nil {
		return fmt.Errorf("add lobbies.started_at: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS finished_at TEXT;
`)
	if err != nil {
		return fmt.Errorf("add lobbies.finished_at: %w", err)
	}
	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS rating_applied BOOLEAN NOT NULL DEFAULT FALSE;
`)
	if err != nil {
		return fmt.Errorf("add lobbies.rating_applied: %w", err)
	}

	_, err = database.Exec(`
ALTER TABLE lobbies
ADD COLUMN IF NOT EXISTS meeting_place TEXT NOT NULL DEFAULT '';
`)
	if err != nil {
		return fmt.Errorf("add lobbies.meeting_place: %w", err)
	}

	_, err = database.Exec(`
CREATE TABLE IF NOT EXISTS lobbies_history (
    id BIGSERIAL PRIMARY KEY,
    original_lobby_id BIGINT NOT NULL,
    host_player_id BIGINT NOT NULL,
    faction TEXT NOT NULL,
    match_size INTEGER NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    finished_at TEXT
);
`)
	if err != nil {
		return fmt.Errorf("migrate lobbies_history table: %w", err)
	}

	_, err = database.Exec(`
CREATE TABLE IF NOT EXISTS mission_conditions (
    id BIGSERIAL PRIMARY KEY,
    mode_name TEXT NOT NULL,
    weather_name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);
`)
	if err != nil {
		return fmt.Errorf("migrate mission_conditions table: %w", err)
	}

	_, err = database.Exec(`
CREATE TABLE IF NOT EXISTS lobby_players (
    id BIGSERIAL PRIMARY KEY,
    lobby_id BIGINT NOT NULL REFERENCES lobbies(id) ON DELETE CASCADE,
    player_id BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    faction_name TEXT NOT NULL,
    is_ready BOOLEAN NOT NULL DEFAULT FALSE,
    is_finished BOOLEAN NOT NULL DEFAULT FALSE,
    joined_at TEXT NOT NULL,
    UNIQUE (lobby_id, player_id)
);
`)
	if err != nil {
		return fmt.Errorf("migrate lobby_players table: %w", err)
	}
	_, err = database.Exec(`
CREATE TABLE IF NOT EXISTS player_faction_experience (
    id BIGSERIAL PRIMARY KEY,
    player_id BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    faction_name TEXT NOT NULL,
    experience INTEGER NOT NULL DEFAULT 0,
    UNIQUE (player_id, faction_name)
);
`)
	if err != nil {
		return fmt.Errorf("migrate player_faction_experience table: %w", err)
	}
	_, err = database.Exec(`
CREATE TABLE IF NOT EXISTS rating_history (
    id BIGSERIAL PRIMARY KEY,
    lobby_id BIGINT NOT NULL REFERENCES lobbies(id) ON DELETE CASCADE,
    player_id BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    old_rating INTEGER NOT NULL,
    new_rating INTEGER NOT NULL,
    old_rd DOUBLE PRECISION NOT NULL,
    new_rd DOUBLE PRECISION NOT NULL,
    score DOUBLE PRECISION NOT NULL,
    created_at TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("migrate rating_history table: %w", err)
	}
	_, err = database.Exec(`
INSERT INTO mission_conditions (mode_name, weather_name, description, is_active)
SELECT 'Breakthrough', 'Clear', 'Standard visibility and movement.', TRUE
WHERE NOT EXISTS (
    SELECT 1 FROM mission_conditions WHERE mode_name = 'Breakthrough' AND weather_name = 'Clear'
);
`)
	if err != nil {
		return fmt.Errorf("seed mission condition breakthrough/clear: %w", err)
	}
	_, err = database.Exec(`
INSERT INTO mission_conditions (mode_name, weather_name, description, is_active)
SELECT 'Domination', 'Rain', 'Difficult spotting due to rain.', TRUE
WHERE NOT EXISTS (
    SELECT 1 FROM mission_conditions WHERE mode_name = 'Domination' AND weather_name = 'Rain'
);
`)
	if err != nil {
		return fmt.Errorf("seed mission condition domination/rain: %w", err)
	}
	_, err = database.Exec(`
INSERT INTO mission_conditions (mode_name, weather_name, description, is_active)
SELECT 'Escort', 'Fog', 'Reduced long-range accuracy.', TRUE
WHERE NOT EXISTS (
    SELECT 1 FROM mission_conditions WHERE mode_name = 'Escort' AND weather_name = 'Fog'
);
`)
	if err != nil {
		return fmt.Errorf("seed mission condition escort/fog: %w", err)
	}

	_, err = database.Exec(`
CREATE INDEX IF NOT EXISTS idx_lobbies_host_player_id ON lobbies(host_player_id);
`)
	if err != nil {
		return fmt.Errorf("create lobbies index: %w", err)
	}

	_, err = database.Exec(`
CREATE INDEX IF NOT EXISTS idx_lobbies_history_original_lobby_id ON lobbies_history(original_lobby_id);
`)
	if err != nil {
		return fmt.Errorf("create lobbies_history index: %w", err)
	}
	_, err = database.Exec(`
CREATE INDEX IF NOT EXISTS idx_mission_conditions_is_active ON mission_conditions(is_active);
`)
	if err != nil {
		return fmt.Errorf("create mission_conditions index: %w", err)
	}
	_, err = database.Exec(`
CREATE INDEX IF NOT EXISTS idx_lobby_players_lobby_id ON lobby_players(lobby_id);
`)
	if err != nil {
		return fmt.Errorf("create lobby_players index: %w", err)
	}
	_, err = database.Exec(`
CREATE INDEX IF NOT EXISTS idx_player_faction_experience_player_id ON player_faction_experience(player_id);
`)
	if err != nil {
		return fmt.Errorf("create player_faction_experience index: %w", err)
	}
	_, err = database.Exec(`
CREATE INDEX IF NOT EXISTS idx_rating_history_player_id ON rating_history(player_id);
`)
	if err != nil {
		return fmt.Errorf("create rating_history index: %w", err)
	}

	return nil
}
