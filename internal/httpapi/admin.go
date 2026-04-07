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

type adminPlayerSummary struct {
	ID        int64  `json:"id"`
	Nickname  string `json:"nickname"`
	FullName  string `json:"fullName"`
	City      string `json:"city"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

type adminLobbyHistorySummary struct {
	ID                 int64  `json:"id"`
	OriginalLobbyID    int64  `json:"originalLobbyId"`
	HostPlayerID       int64  `json:"hostPlayerId"`
	Faction            string `json:"faction"`
	MatchSize          int    `json:"matchSize"`
	IsRanked           bool   `json:"isRanked"`
	MeetingPlace       string `json:"meetingPlace"`
	MissionConditionID *int64 `json:"missionConditionId,omitempty"`
	CustomMissionName  string `json:"customMissionName,omitempty"`
	CustomWeatherName  string `json:"customWeatherName,omitempty"`
	CustomAtmosphere   string `json:"customAtmosphereName,omitempty"`
	Status             string `json:"status"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
	FinishedAt         string `json:"finishedAt,omitempty"`
}

func (a *api) adminPlayersCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	_, err := a.requireAdmin(r)
	if err != nil {
		if err.Error() == "forbidden" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	q := r.URL.Query()
	sortBy := strings.ToLower(strings.TrimSpace(q.Get("sort")))
	orderClause := "p.id ASC"
	switch sortBy {
	case "", "id_asc":
		orderClause = "p.id ASC"
	case "id_desc":
		orderClause = "p.id DESC"
	case "nickname_asc":
		orderClause = "p.nickname ASC"
	case "nickname_desc":
		orderClause = "p.nickname DESC"
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sort parameter"})
		return
	}

	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, perr := strconv.Atoi(raw)
		if perr != nil || v <= 0 || v > 200 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 200"})
			return
		}
		limit = v
	}

	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		v, perr := strconv.Atoi(raw)
		if perr != nil || v < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be >= 0"})
			return
		}
		offset = v
	}

	var total int
	if err := a.db.QueryRow(`
SELECT COUNT(1) FROM players p
LEFT JOIN player_credentials pc ON pc.player_id = p.id
`).Scan(&total); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to count players"})
		return
	}

	rows, err := a.db.Query(`
SELECT p.id, p.nickname, p.full_name, p.city, COALESCE(pc.role, 'player') AS role, p.created_at
FROM players p
LEFT JOIN player_credentials pc ON pc.player_id = p.id
ORDER BY `+orderClause+`
LIMIT $1 OFFSET $2
`, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load players"})
		return
	}
	defer rows.Close()

	var items []adminPlayerSummary
	for rows.Next() {
		var it adminPlayerSummary
		if scanErr := rows.Scan(&it.ID, &it.Nickname, &it.FullName, &it.City, &it.Role, &it.CreatedAt); scanErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read players"})
			return
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to iterate players"})
		return
	}

	sortOut := sortBy
	if sortOut == "" {
		sortOut = "id_asc"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sort":   sortOut,
		"limit":  limit,
		"offset": offset,
		"total":  total,
		"items":  items,
	})
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

func (a *api) adminLobbiesHistoryCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	_, err := a.requireAdmin(r)
	if err != nil {
		if err.Error() == "forbidden" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	q := r.URL.Query()
	sortBy := strings.ToLower(strings.TrimSpace(q.Get("sort")))
	orderClause := "id DESC"
	switch sortBy {
	case "", "id_desc":
		orderClause = "id DESC"
	case "id_asc":
		orderClause = "id ASC"
	case "finished_desc":
		orderClause = "finished_at DESC, id DESC"
	case "finished_asc":
		orderClause = "finished_at ASC, id ASC"
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sort parameter"})
		return
	}

	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, perr := strconv.Atoi(raw)
		if perr != nil || v <= 0 || v > 200 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 200"})
			return
		}
		limit = v
	}
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		v, perr := strconv.Atoi(raw)
		if perr != nil || v < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be >= 0"})
			return
		}
		offset = v
	}

	var total int
	if err := a.db.QueryRow(`SELECT COUNT(1) FROM lobbies_history`).Scan(&total); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to count lobby history"})
		return
	}

	rows, err := a.db.Query(`
SELECT id, original_lobby_id, host_player_id, faction, match_size, is_ranked, meeting_place,
       mission_condition_id, custom_mission_name, custom_weather_name, custom_atmosphere_name,
       status, created_at, updated_at, finished_at
FROM lobbies_history
ORDER BY `+orderClause+`
LIMIT $1 OFFSET $2
`, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby history"})
		return
	}
	defer rows.Close()

	var items []adminLobbyHistorySummary
	for rows.Next() {
		var it adminLobbyHistorySummary
		var missionID sql.NullInt64
		var customMission, customWeather, customAtmos, finishedAt sql.NullString
		if scanErr := rows.Scan(
			&it.ID, &it.OriginalLobbyID, &it.HostPlayerID, &it.Faction, &it.MatchSize,
			&it.IsRanked, &it.MeetingPlace, &missionID, &customMission, &customWeather, &customAtmos,
			&it.Status, &it.CreatedAt, &it.UpdatedAt, &finishedAt,
		); scanErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read lobby history"})
			return
		}
		if missionID.Valid {
			v := missionID.Int64
			it.MissionConditionID = &v
		}
		if customMission.Valid {
			it.CustomMissionName = customMission.String
		}
		if customWeather.Valid {
			it.CustomWeatherName = customWeather.String
		}
		if customAtmos.Valid {
			it.CustomAtmosphere = customAtmos.String
		}
		if finishedAt.Valid {
			it.FinishedAt = finishedAt.String
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to iterate lobby history"})
		return
	}

	sortOut := sortBy
	if sortOut == "" {
		sortOut = "id_desc"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sort":   sortOut,
		"limit":  limit,
		"offset": offset,
		"total":  total,
		"items":  items,
	})
}

