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

type lobby struct {
	ID                 int64             `json:"id"`
	HostPlayerID       int64             `json:"hostPlayerId"`
	Faction            string            `json:"faction"`
	MatchSize          int               `json:"matchSize"`
	IsRanked           bool              `json:"isRanked"`
	MissionConditionID *int64            `json:"missionConditionId,omitempty"`
	MissionCondition   *missionCondition `json:"missionCondition,omitempty"`
	CustomMissionName  string            `json:"customMissionName,omitempty"`
	CustomWeatherName  string            `json:"customWeatherName,omitempty"`
	CustomAtmosphere   string            `json:"customAtmosphereName,omitempty"`
	Players            []lobbyPlayer     `json:"players,omitempty"`
	Status             string            `json:"status"`
	StartedAt          string            `json:"startedAt,omitempty"`
	FinishedAt         string            `json:"finishedAt,omitempty"`
	CreatedAt          string            `json:"createdAt"`
	UpdatedAt          string            `json:"updatedAt"`
}

type createLobbyRequest struct {
	HostPlayerID int64  `json:"hostPlayerId"`
	Faction      string `json:"faction"`
	MatchSize    int    `json:"matchSize"`
	IsRanked     bool   `json:"isRanked"`
}

type missionCondition struct {
	ID          int64  `json:"id"`
	ModeName    string `json:"modeName"`
	WeatherName string `json:"weatherName"`
	Description string `json:"description"`
}

type lobbyPlayer struct {
	PlayerID   int64  `json:"playerId"`
	Faction    string `json:"faction"`
	IsReady    bool   `json:"isReady"`
	IsFinished bool   `json:"isFinished"`
	JoinedAt   string `json:"joinedAt"`
}

type joinLobbyRequest struct {
	PlayerID int64  `json:"playerId"`
	Faction  string `json:"faction"`
}

type customConditionsRequest struct {
	MissionName    string `json:"missionName"`
	WeatherName    string `json:"weatherName"`
	AtmosphereName string `json:"atmosphereName"`
}

func (a *api) createLobby(w http.ResponseWriter, r *http.Request) {
	var req createLobbyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	req.Faction = strings.TrimSpace(req.Faction)
	if req.HostPlayerID <= 0 || req.Faction == "" || req.MatchSize <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "hostPlayerId, faction and matchSize are required",
		})
		return
	}

	var exists int
	err := a.db.QueryRow(`SELECT 1 FROM players WHERE id = $1`, req.HostPlayerID).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "host player does not exist"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to validate host player"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var conditionID *int64

	var id int64
	err = a.db.QueryRow(`
INSERT INTO lobbies (host_player_id, faction, match_size, is_ranked, mission_condition_id, status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, 'open', $6, $7)
RETURNING id
`, req.HostPlayerID, req.Faction, req.MatchSize, req.IsRanked, conditionID, now, now).Scan(&id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create lobby"})
		return
	}

	_, _ = a.db.Exec(`
INSERT INTO lobby_players (lobby_id, player_id, faction_name, is_ready, is_finished, joined_at)
VALUES ($1, $2, $3, FALSE, FALSE, $4)
ON CONFLICT (lobby_id, player_id) DO NOTHING
`, id, req.HostPlayerID, req.Faction, now)

	l, err := a.getLobbyByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load created lobby"})
		return
	}

	writeJSON(w, http.StatusCreated, l)
}

func (a *api) getLobby(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/lobbies/")
	idStr = strings.Trim(idStr, "/")
	if strings.HasSuffix(idStr, "random-condition") {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid lobby id"})
		return
	}

	l, err := a.getLobbyByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "lobby not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch lobby"})
		return
	}

	writeJSON(w, http.StatusOK, l)
}

func (a *api) getLobbyByID(id int64) (lobby, error) {
	var l lobby
	var conditionID sql.NullInt64
	var customMission, customWeather, customAtmosphere sql.NullString
	var startedAt, finishedAt sql.NullString
	err := a.db.QueryRow(`
SELECT id, host_player_id, faction, match_size, is_ranked, mission_condition_id,
       custom_mission_name, custom_weather_name, custom_atmosphere_name,
       status, started_at, finished_at, created_at, updated_at
FROM lobbies
WHERE id = $1
`, id).Scan(
		&l.ID,
		&l.HostPlayerID,
		&l.Faction,
		&l.MatchSize,
		&l.IsRanked,
		&conditionID,
		&customMission,
		&customWeather,
		&customAtmosphere,
		&l.Status,
		&startedAt,
		&finishedAt,
		&l.CreatedAt,
		&l.UpdatedAt,
	)
	if err != nil {
		return lobby{}, err
	}
	if conditionID.Valid {
		cid := conditionID.Int64
		l.MissionConditionID = &cid
		condition, err := a.getMissionConditionByID(cid)
		if err == nil {
			l.MissionCondition = &condition
		}
	}
	if customMission.Valid {
		l.CustomMissionName = customMission.String
	}
	if customWeather.Valid {
		l.CustomWeatherName = customWeather.String
	}
	if customAtmosphere.Valid {
		l.CustomAtmosphere = customAtmosphere.String
	}
	if startedAt.Valid {
		l.StartedAt = startedAt.String
	}
	if finishedAt.Valid {
		l.FinishedAt = finishedAt.String
	}
	rows, err := a.db.Query(`
SELECT player_id, faction_name, is_ready, is_finished, joined_at
FROM lobby_players
WHERE lobby_id = $1
ORDER BY joined_at
`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var lp lobbyPlayer
			if scanErr := rows.Scan(&lp.PlayerID, &lp.Faction, &lp.IsReady, &lp.IsFinished, &lp.JoinedAt); scanErr == nil {
				l.Players = append(l.Players, lp)
			}
		}
	}
	return l, nil
}

func (a *api) pickRandomMissionCondition() (missionCondition, error) {
	var c missionCondition
	err := a.db.QueryRow(`
SELECT id, mode_name, weather_name, description
FROM mission_conditions
WHERE is_active = TRUE
ORDER BY random()
LIMIT 1
`).Scan(&c.ID, &c.ModeName, &c.WeatherName, &c.Description)
	if err != nil {
		return missionCondition{}, err
	}
	return c, nil
}

func (a *api) getMissionConditionByID(id int64) (missionCondition, error) {
	var c missionCondition
	err := a.db.QueryRow(`
SELECT id, mode_name, weather_name, description
FROM mission_conditions
WHERE id = $1
`, id).Scan(&c.ID, &c.ModeName, &c.WeatherName, &c.Description)
	if err != nil {
		return missionCondition{}, err
	}
	return c, nil
}

func (a *api) listMissionConditions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	rows, err := a.db.Query(`
SELECT id, mode_name, weather_name, description
FROM mission_conditions
WHERE is_active = TRUE
ORDER BY id
`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load mission conditions"})
		return
	}
	defer rows.Close()

	var out []missionCondition
	for rows.Next() {
		var c missionCondition
		if err := rows.Scan(&c.ID, &c.ModeName, &c.WeatherName, &c.Description); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read mission conditions"})
			return
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) randomizeLobbyMissionCondition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/lobbies/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "random-condition" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	lobbyID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || lobbyID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid lobby id"})
		return
	}

	var isRanked bool
	err = a.db.QueryRow(`SELECT is_ranked FROM lobbies WHERE id = $1`, lobbyID).Scan(&isRanked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "lobby not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby"})
		return
	}
	if isRanked {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mission conditions are available only for non-ranked lobbies"})
		return
	}

	condition, err := a.pickRandomMissionCondition()
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no active mission conditions configured"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to pick mission condition"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = a.db.Exec(`
UPDATE lobbies
SET mission_condition_id = $1, updated_at = $2
WHERE id = $3
`, condition.ID, now, lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update lobby condition"})
		return
	}

	updatedLobby, err := a.getLobbyByID(lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby"})
		return
	}
	writeJSON(w, http.StatusOK, updatedLobby)
}

func (a *api) joinLobby(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lobbyID, ok := parseLobbyActionID(r.URL.Path, "join")
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	var req joinLobbyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	req.Faction = strings.TrimSpace(req.Faction)
	if req.PlayerID <= 0 || req.Faction == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "playerId and faction are required"})
		return
	}
	authPlayerID, err := a.requirePlayer(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if authPlayerID != req.PlayerID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM lobbies WHERE id = $1`, lobbyID).Scan(&exists); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "lobby not found"})
		return
	}

	var playersCount int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM lobby_players WHERE lobby_id = $1`, lobbyID).Scan(&playersCount); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to count players"})
		return
	}
	if playersCount >= 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "lobby already has 2 players"})
		return
	}

	_, err = tx.Exec(`
INSERT INTO lobby_players (lobby_id, player_id, faction_name, is_ready, is_finished, joined_at)
VALUES ($1, $2, $3, FALSE, FALSE, $4)
ON CONFLICT (lobby_id, player_id) DO UPDATE SET faction_name = EXCLUDED.faction_name
`, lobbyID, req.PlayerID, req.Faction, now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to join lobby"})
		return
	}
	_, _ = tx.Exec(`UPDATE lobbies SET updated_at = $1 WHERE id = $2`, now, lobbyID)

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to commit transaction"})
		return
	}
	updatedLobby, err := a.getLobbyByID(lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby"})
		return
	}
	writeJSON(w, http.StatusOK, updatedLobby)
}

func (a *api) setCustomConditions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lobbyID, ok := parseLobbyActionID(r.URL.Path, "conditions")
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	var req customConditionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	req.MissionName = strings.TrimSpace(req.MissionName)
	req.WeatherName = strings.TrimSpace(req.WeatherName)
	req.AtmosphereName = strings.TrimSpace(req.AtmosphereName)
	if req.MissionName == "" || req.WeatherName == "" || req.AtmosphereName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missionName, weatherName and atmosphereName are required"})
		return
	}

	var isRanked bool
	err := a.db.QueryRow(`SELECT is_ranked FROM lobbies WHERE id = $1`, lobbyID).Scan(&isRanked)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "lobby not found"})
		return
	}
	if isRanked {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "custom conditions are available only for non-ranked lobbies"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = a.db.Exec(`
UPDATE lobbies
SET custom_mission_name = $1, custom_weather_name = $2, custom_atmosphere_name = $3, mission_condition_id = NULL, updated_at = $4
WHERE id = $5
`, req.MissionName, req.WeatherName, req.AtmosphereName, now, lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to set custom conditions"})
		return
	}
	updatedLobby, err := a.getLobbyByID(lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby"})
		return
	}
	writeJSON(w, http.StatusOK, updatedLobby)
}

func (a *api) markLobbyReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lobbyID, ok := parseLobbyActionID(r.URL.Path, "ready")
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	var req struct {
		PlayerID int64 `json:"playerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PlayerID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "playerId is required"})
		return
	}
	authPlayerID, err := a.requirePlayer(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if authPlayerID != req.PlayerID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE lobby_players SET is_ready = TRUE WHERE lobby_id = $1 AND player_id = $2`, lobbyID, req.PlayerID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update ready state"})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "player is not in lobby"})
		return
	}

	var playersCount, readyCount int
	_ = tx.QueryRow(`SELECT COUNT(1) FROM lobby_players WHERE lobby_id = $1`, lobbyID).Scan(&playersCount)
	_ = tx.QueryRow(`SELECT COUNT(1) FROM lobby_players WHERE lobby_id = $1 AND is_ready = TRUE`, lobbyID).Scan(&readyCount)

	status := "open"
	startedAt := sql.NullString{}
	if err := tx.QueryRow(`SELECT status, started_at FROM lobbies WHERE id = $1`, lobbyID).Scan(&status, &startedAt); err == nil {
		if playersCount == 2 && readyCount == 2 && status != "started" {
			_, _ = tx.Exec(`UPDATE lobbies SET status = 'started', started_at = $1, updated_at = $1 WHERE id = $2`, now, lobbyID)
		} else {
			_, _ = tx.Exec(`UPDATE lobbies SET updated_at = $1 WHERE id = $2`, now, lobbyID)
		}
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to commit transaction"})
		return
	}
	updatedLobby, err := a.getLobbyByID(lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby"})
		return
	}
	writeJSON(w, http.StatusOK, updatedLobby)
}

func (a *api) markMatchFinished(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lobbyID, ok := parseLobbyActionID(r.URL.Path, "match-finished")
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var req struct {
		PlayerID int64 `json:"playerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PlayerID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "playerId is required"})
		return
	}
	authPlayerID, err := a.requirePlayer(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if authPlayerID != req.PlayerID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	var lobbyStatus string
	err = tx.QueryRow(`SELECT status FROM lobbies WHERE id = $1`, lobbyID).Scan(&lobbyStatus)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "lobby not found"})
		return
	}
	if lobbyStatus != "started" && lobbyStatus != "finished" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "match has not started yet"})
		return
	}

	res, err := tx.Exec(`UPDATE lobby_players SET is_finished = TRUE WHERE lobby_id = $1 AND player_id = $2`, lobbyID, req.PlayerID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to set match-finished flag"})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "player is not in lobby"})
		return
	}

	var totalPlayers, finishedPlayers int
	_ = tx.QueryRow(`SELECT COUNT(1) FROM lobby_players WHERE lobby_id = $1`, lobbyID).Scan(&totalPlayers)
	_ = tx.QueryRow(`SELECT COUNT(1) FROM lobby_players WHERE lobby_id = $1 AND is_finished = TRUE`, lobbyID).Scan(&finishedPlayers)

	if totalPlayers == 2 && finishedPlayers == 2 && lobbyStatus != "finished" {
		rows, qErr := tx.Query(`SELECT player_id, faction_name FROM lobby_players WHERE lobby_id = $1`, lobbyID)
		if qErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby players"})
			return
		}
		defer rows.Close()
		for rows.Next() {
			var playerID int64
			var factionName string
			if scanErr := rows.Scan(&playerID, &factionName); scanErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read lobby players"})
				return
			}

			var factionsRaw string
			var totalExperience int
			if err := tx.QueryRow(`SELECT factions, total_experience FROM players WHERE id = $1`, playerID).Scan(&factionsRaw, &totalExperience); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load player progress"})
				return
			}
			var factions []string
			_ = json.Unmarshal([]byte(factionsRaw), &factions)
			if !containsString(factions, factionName) {
				factions = append(factions, factionName)
			}
			updatedFactions, _ := json.Marshal(factions)
			_, err = tx.Exec(`
UPDATE players
SET total_experience = $1, factions = $2, updated_at = $3
WHERE id = $4
`, totalExperience+1, string(updatedFactions), now, playerID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update player progression"})
				return
			}
			_, err = tx.Exec(`
INSERT INTO player_faction_experience (player_id, faction_name, experience)
VALUES ($1, $2, 1)
ON CONFLICT (player_id, faction_name)
DO UPDATE SET experience = player_faction_experience.experience + 1
`, playerID, factionName)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update faction experience"})
				return
			}
		}

		_, _ = tx.Exec(`UPDATE lobbies SET status = 'finished', finished_at = $1, updated_at = $1 WHERE id = $2`, now, lobbyID)
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to commit transaction"})
		return
	}
	updatedLobby, err := a.getLobbyByID(lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby"})
		return
	}
	writeJSON(w, http.StatusOK, updatedLobby)
}

func parseLobbyActionID(path, action string) (int64, bool) {
	p := strings.Trim(strings.TrimPrefix(path, "/lobbies/"), "/")
	parts := strings.Split(p, "/")
	if len(parts) != 2 || parts[1] != action {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func containsString(items []string, target string) bool {
	for _, v := range items {
		if strings.EqualFold(v, target) {
			return true
		}
	}
	return false
}
