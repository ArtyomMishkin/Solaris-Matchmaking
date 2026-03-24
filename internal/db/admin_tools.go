package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func PromotePlayerToAdmin(database *sql.DB, nickname string) error {
	var playerID int64
	err := database.QueryRow(`SELECT id FROM players WHERE nickname = $1`, nickname).Scan(&playerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("player with nickname %q not found", nickname)
		}
		return fmt.Errorf("load player by nickname: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := database.Exec(`
UPDATE player_credentials
SET role = 'admin', updated_at = $1
WHERE player_id = $2
`, now, playerID)
	if err != nil {
		return fmt.Errorf("promote player to admin: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated rows for promote: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("credentials for nickname %q not found", nickname)
	}

	return nil
}

func RemoveAdminFromPlayer(database *sql.DB, nickname string) error {
	var playerID int64
	err := database.QueryRow(`SELECT id FROM players WHERE nickname = $1`, nickname).Scan(&playerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("player with nickname %q not found", nickname)
		}
		return fmt.Errorf("load player by nickname: %w", err)
	}

	var adminCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM player_credentials WHERE role = 'admin'`).Scan(&adminCount); err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if adminCount <= 0 {
		return nil
	}

	// If we are removing the only admin, refuse to prevent accidental lock-out.
	var isAdmin bool
	if err := database.QueryRow(`SELECT EXISTS(SELECT 1 FROM player_credentials WHERE player_id = $1 AND role = 'admin')`, playerID).
		Scan(&isAdmin); err != nil {
		return fmt.Errorf("check if player is admin: %w", err)
	}
	if isAdmin && adminCount == 1 {
		return fmt.Errorf("cannot remove the last admin (%q)", nickname)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := database.Exec(`
UPDATE player_credentials
SET role = 'player', updated_at = $1
WHERE player_id = $2 AND role = 'admin'
`, now, playerID)
	if err != nil {
		return fmt.Errorf("remove admin from player: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated rows for remove: %w", err)
	}
	if affected == 0 && !isAdmin {
		return fmt.Errorf("player %q is not admin", nickname)
	}

	return nil
}

func ListAdmins(database *sql.DB) ([]string, error) {
	rows, err := database.Query(`
SELECT p.nickname
FROM players p
JOIN player_credentials pc ON pc.player_id = p.id
WHERE pc.role = 'admin'
ORDER BY p.nickname;
`)
	if err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	defer rows.Close()

	var admins []string
	for rows.Next() {
		var nickname string
		if err := rows.Scan(&nickname); err != nil {
			return nil, fmt.Errorf("scan admin nickname: %w", err)
		}
		admins = append(admins, nickname)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin nicknames: %w", err)
	}

	return admins, nil
}
