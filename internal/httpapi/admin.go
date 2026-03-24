package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type setRankRequest struct {
	RankTitle      string `json:"rankTitle"`
	RankAttestedAt string `json:"rankAttestedAt"`
}

func (a *api) adminPlayersCollection(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (a *api) adminPlayersSubresource(w http.ResponseWriter, r *http.Request) {
	// Supported:
	// - PUT /admin/players/{id}/rank
	// - DELETE /admin/players/{id}
	playerTokenID, err := a.requireAdmin(r)
	if err != nil {
		if err.Error() == "forbidden" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	_ = playerTokenID // kept for potential auditing later

	path := strings.TrimPrefix(r.URL.Path, "/admin/players/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	playerID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || playerID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid player id"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// PUT /admin/players/{id}/rank
	if len(parts) == 2 && parts[1] == "rank" {
		if r.Method != http.MethodPut {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		var req setRankRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		req.RankTitle = strings.TrimSpace(req.RankTitle)
		req.RankAttestedAt = strings.TrimSpace(req.RankAttestedAt)
		if req.RankTitle == "" || req.RankAttestedAt == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rankTitle and rankAttestedAt are required"})
			return
		}

		_, err := a.db.Exec(`
UPDATE players
SET rank_title = $1, rank_attested_at = $2, updated_at = $3
WHERE id = $4
`, req.RankTitle, req.RankAttestedAt, now, playerID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to set rank"})
			return
		}

		p, err := a.getPlayerByID(playerID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "player not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load player"})
			return
		}

		writeJSON(w, http.StatusOK, p)
		return
	}

	// DELETE /admin/players/{id}
	if len(parts) == 1 {
		if r.Method != http.MethodDelete {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		tx, err := a.db.Begin()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to begin transaction"})
			return
		}
		defer tx.Rollback()

		_, err = tx.Exec(`DELETE FROM lobbies_history WHERE host_player_id = $1`, playerID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete lobby history"})
			return
		}
		_, err = tx.Exec(`DELETE FROM lobbies WHERE host_player_id = $1`, playerID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete lobbies"})
			return
		}
		_, err = tx.Exec(`DELETE FROM players WHERE id = $1`, playerID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete player"})
			return
		}

		if err := tx.Commit(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to commit transaction"})
			return
		}

		writeJSON(w, http.StatusNoContent, map[string]string{"status": "deleted"})
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (a *api) adminLobbiesSubresource(w http.ResponseWriter, r *http.Request) {
	_, err := a.requireAdmin(r)
	if err != nil {
		if err.Error() == "forbidden" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/admin/lobbies/")
	id, err := strconv.ParseInt(strings.Trim(idStr, "/"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid lobby id"})
		return
	}

	tx, err := a.db.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	// Delete from history by original_lobby_id and then remove the lobby itself.
	_, err = tx.Exec(`DELETE FROM lobbies_history WHERE original_lobby_id = $1`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete lobby history"})
		return
	}
	_, err = tx.Exec(`DELETE FROM lobbies WHERE id = $1`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete lobby"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to commit transaction"})
		return
	}

	writeJSON(w, http.StatusNoContent, map[string]string{"status": "deleted"})
}

func (a *api) adminLobbiesHistorySubresource(w http.ResponseWriter, r *http.Request) {
	_, err := a.requireAdmin(r)
	if err != nil {
		if err.Error() == "forbidden" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/admin/lobbies-history/")
	id, err := strconv.ParseInt(strings.Trim(idStr, "/"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid lobby history id"})
		return
	}

	_, err = a.db.Exec(`DELETE FROM lobbies_history WHERE id = $1`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete lobby history"})
		return
	}

	writeJSON(w, http.StatusNoContent, map[string]string{"status": "deleted"})
}

