package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type lobbyHistoryPlayerDTO struct {
	PlayerID   int64  `json:"playerId"`
	Faction    string `json:"faction"`
	IsReady    bool   `json:"isReady"`
	IsFinished bool   `json:"isFinished"`
	JoinedAt   string `json:"joinedAt"`
}

type lobbyHistoryItemDTO struct {
	ID                 int64  `json:"id"`
	OriginalLobbyID    int64  `json:"originalLobbyId"`
	HostPlayerID       int64  `json:"hostPlayerId"`
	HostFaction        string `json:"hostFaction"`
	PlayerFaction      string `json:"playerFaction,omitempty"`
	MatchSize          int    `json:"matchSize"`
	Status             string `json:"status"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
	IsRanked           bool   `json:"isRanked"`
	MeetingPlace       string `json:"meetingPlace"`
	StartedAt          string `json:"startedAt,omitempty"`
	FinishedAt         string `json:"finishedAt,omitempty"`
	RatingApplied      bool   `json:"ratingApplied"`
	MissionConditionID *int64 `json:"missionConditionId,omitempty"`
	CustomMissionName  string `json:"customMissionName,omitempty"`
	CustomWeatherName  string `json:"customWeatherName,omitempty"`
	CustomAtmosphere   string `json:"customAtmosphereName,omitempty"`
}

func (a *api) lobbyHistoryByID(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if strings.HasSuffix(trimmed, "/players") {
		a.getLobbyHistoryPlayers(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.getLobbyHistory(w, r)
}

func (a *api) getPlayerLobbiesHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	playerID, err := parsePlayerSubresourceID(r.URL.Path, "lobbies-history")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid player id"})
		return
	}

	var exists int
	if err := a.db.QueryRow(`SELECT 1 FROM players WHERE id = $1`, playerID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "player not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch player"})
		return
	}

	q := r.URL.Query()
	sortBy := strings.ToLower(strings.TrimSpace(q.Get("sort")))
	orderClause := "l.finished_at DESC, l.id DESC"
	switch sortBy {
	case "", "finished_desc":
		orderClause = "l.finished_at DESC, l.id DESC"
	case "finished_asc":
		orderClause = "l.finished_at ASC, l.id ASC"
	case "id_desc":
		orderClause = "l.id DESC"
	case "id_asc":
		orderClause = "l.id ASC"
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
SELECT COUNT(1)
FROM lobbies l
JOIN lobby_players lp ON lp.lobby_id = l.id
WHERE lp.player_id = $1
  AND l.status = 'finished'
`, playerID).Scan(&total); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to count lobby history"})
		return
	}

	rows, err := a.db.Query(`
SELECT
	l.id, l.id AS original_lobby_id, l.host_player_id, l.faction, lp.faction_name, l.match_size,
	l.status, l.created_at, l.updated_at, l.is_ranked, l.meeting_place,
	l.started_at, l.finished_at, l.rating_applied, l.mission_condition_id,
	l.custom_mission_name, l.custom_weather_name, l.custom_atmosphere_name
FROM lobbies l
JOIN lobby_players lp ON lp.lobby_id = l.id
WHERE lp.player_id = $1
  AND l.status = 'finished'
ORDER BY `+orderClause+`
LIMIT $2 OFFSET $3
`, playerID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby history"})
		return
	}
	defer rows.Close()

	items := make([]lobbyHistoryItemDTO, 0)
	for rows.Next() {
		it, scanErr := scanLobbyHistoryItem(rows)
		if scanErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read lobby history"})
			return
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to iterate lobby history"})
		return
	}

	if sortBy == "" {
		sortBy = "finished_desc"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"playerId": playerID,
		"sort":     sortBy,
		"limit":    limit,
		"offset":   offset,
		"total":    total,
		"items":    items,
	})
}

func (a *api) getLobbyHistory(w http.ResponseWriter, r *http.Request) {
	lobbyID, err := parseLobbyHistoryIDPath(r.URL.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid lobby history id"})
		return
	}

	row := a.db.QueryRow(`
SELECT
	id, id AS original_lobby_id, host_player_id, faction, faction AS player_faction, match_size,
	status, created_at, updated_at, is_ranked, meeting_place,
	started_at, finished_at, rating_applied, mission_condition_id,
	custom_mission_name, custom_weather_name, custom_atmosphere_name
FROM lobbies
WHERE id = $1
  AND status = 'finished'
`, lobbyID)
	item, err := scanLobbyHistoryItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "finished lobby not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby history"})
		return
	}

	players, err := a.loadLobbyPlayersForFinishedLobby(lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby history players"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lobby":   item,
		"players": players,
	})
}

func (a *api) getLobbyHistoryPlayers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lobbyID, err := parseLobbyHistoryPlayersIDPath(r.URL.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid lobby history id"})
		return
	}
	players, err := a.loadLobbyPlayersForFinishedLobby(lobbyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "finished lobby not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby history players"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lobbyHistoryId": lobbyID,
		"players":        players,
	})
}

func (a *api) loadLobbyPlayersForFinishedLobby(lobbyID int64) ([]lobbyHistoryPlayerDTO, error) {
	var exists int
	if err := a.db.QueryRow(`SELECT 1 FROM lobbies WHERE id = $1 AND status = 'finished'`, lobbyID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}

	rows, err := a.db.Query(`
SELECT player_id, faction_name, is_ready, is_finished, joined_at
FROM lobby_players
WHERE lobby_id = $1
ORDER BY joined_at ASC, player_id ASC
`, lobbyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]lobbyHistoryPlayerDTO, 0)
	for rows.Next() {
		var p lobbyHistoryPlayerDTO
		if scanErr := rows.Scan(&p.PlayerID, &p.Faction, &p.IsReady, &p.IsFinished, &p.JoinedAt); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanLobbyHistoryItem(s scanner) (lobbyHistoryItemDTO, error) {
	var item lobbyHistoryItemDTO
	var missionID sql.NullInt64
	var customMission, customWeather, customAtmos, startedAt, finishedAt sql.NullString
	err := s.Scan(
		&item.ID, &item.OriginalLobbyID, &item.HostPlayerID, &item.HostFaction, &item.PlayerFaction, &item.MatchSize,
		&item.Status, &item.CreatedAt, &item.UpdatedAt, &item.IsRanked, &item.MeetingPlace,
		&startedAt, &finishedAt, &item.RatingApplied, &missionID, &customMission, &customWeather, &customAtmos,
	)
	if err != nil {
		return lobbyHistoryItemDTO{}, err
	}
	if missionID.Valid {
		v := missionID.Int64
		item.MissionConditionID = &v
	}
	if customMission.Valid {
		item.CustomMissionName = customMission.String
	}
	if customWeather.Valid {
		item.CustomWeatherName = customWeather.String
	}
	if customAtmos.Valid {
		item.CustomAtmosphere = customAtmos.String
	}
	if startedAt.Valid {
		item.StartedAt = startedAt.String
	}
	if finishedAt.Valid {
		item.FinishedAt = finishedAt.String
	}
	return item, nil
}

func parsePlayerSubresourceID(path, subresource string) (int64, error) {
	p := strings.Trim(strings.TrimPrefix(path, "/players/"), "/")
	parts := strings.Split(p, "/")
	if len(parts) != 2 || parts[1] != subresource {
		return 0, errors.New("invalid path")
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func parseLobbyHistoryIDPath(path string) (int64, error) {
	p := strings.Trim(strings.TrimPrefix(path, "/lobbies-history/"), "/")
	if p == "" || strings.Contains(p, "/") {
		return 0, errors.New("invalid path")
	}
	id, err := strconv.ParseInt(p, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func parseLobbyHistoryPlayersIDPath(path string) (int64, error) {
	p := strings.Trim(strings.TrimPrefix(path, "/lobbies-history/"), "/")
	parts := strings.Split(p, "/")
	if len(parts) != 2 || parts[1] != "players" {
		return 0, errors.New("invalid path")
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
