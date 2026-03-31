package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// lobby is the internal shape loaded from the DB (host faction duplicated on lobbies.faction).
type lobby struct {
	ID                 int64
	HostPlayerID       int64
	Faction            string
	MeetingPlace      string
	MatchSize          int
	IsRanked           bool
	MissionConditionID *int64
	MissionCondition   *missionCondition
	CustomMissionName  string
	CustomWeatherName  string
	CustomAtmosphere   string
	Players            []lobbyPlayer
	Status             string
	StartedAt          string
	FinishedAt         string
	CreatedAt          string
	UpdatedAt          string
}

type lobbyMemberDTO struct {
	PlayerID   int64  `json:"playerId"`
	Faction    string `json:"faction"`
	IsReady    bool   `json:"isReady"`
	IsFinished bool   `json:"isFinished"`
	JoinedAt   string `json:"joinedAt"`
}

func playerSlotKey(playerID int64) string {
	return fmt.Sprintf("player%d", playerID)
}

func factionFromPlayerSlot(raw map[string]json.RawMessage, playerID int64) (string, error) {
	key := playerSlotKey(playerID)
	slotRaw, ok := raw[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	var slot struct {
		Faction string `json:"faction"`
	}
	if err := json.Unmarshal(slotRaw, &slot); err != nil {
		return "", fmt.Errorf("invalid %s object", key)
	}
	f := strings.TrimSpace(slot.Faction)
	if f == "" {
		return "", fmt.Errorf("%s.faction is required", key)
	}
	return f, nil
}

func validateCreateLobbyKeys(raw map[string]json.RawMessage, allowedSlot string) error {
	for k := range raw {
		switch k {
		case "hostPlayerId", "matchSize", "isRanked", "meetingPlace":
			continue
		default:
			if strings.HasPrefix(k, "player") {
				if k != allowedSlot {
					return fmt.Errorf("unexpected field %q", k)
				}
				continue
			}
			return fmt.Errorf("unknown field %q", k)
		}
	}
	return nil
}

func validateJoinLobbyKeys(raw map[string]json.RawMessage, allowedSlot string) error {
	for k := range raw {
		if k == "playerId" {
			continue
		}
		if strings.HasPrefix(k, "player") {
			if k != allowedSlot {
				return fmt.Errorf("unexpected field %q", k)
			}
			continue
		}
		return fmt.Errorf("unknown field %q", k)
	}
	return nil
}

// lobbyToJSON builds the wire object: each participant is under player{theirId}.
func lobbyToJSON(l lobby) map[string]any {
	out := map[string]any{
		"id":           l.ID,
		"hostPlayerId": l.HostPlayerID,
		"matchSize":    l.MatchSize,
		"isRanked":     l.IsRanked,
		"meetingPlace": l.MeetingPlace,
		"status":       l.Status,
		"createdAt":    l.CreatedAt,
		"updatedAt":    l.UpdatedAt,
	}
	if l.MissionConditionID != nil {
		out["missionConditionId"] = *l.MissionConditionID
	}
	if l.MissionCondition != nil {
		out["missionCondition"] = l.MissionCondition
	}
	if l.CustomMissionName != "" {
		out["customMissionName"] = l.CustomMissionName
	}
	if l.CustomWeatherName != "" {
		out["customWeatherName"] = l.CustomWeatherName
	}
	if l.CustomAtmosphere != "" {
		out["customAtmosphereName"] = l.CustomAtmosphere
	}
	if l.StartedAt != "" {
		out["startedAt"] = l.StartedAt
	}
	if l.FinishedAt != "" {
		out["finishedAt"] = l.FinishedAt
	}

	hostSeen := false
	for i := range l.Players {
		p := l.Players[i]
		out[playerSlotKey(p.PlayerID)] = lobbyMemberDTO{
			PlayerID: p.PlayerID, Faction: p.Faction, IsReady: p.IsReady,
			IsFinished: p.IsFinished, JoinedAt: p.JoinedAt,
		}
		if p.PlayerID == l.HostPlayerID {
			hostSeen = true
		}
	}
	if !hostSeen {
		out[playerSlotKey(l.HostPlayerID)] = lobbyMemberDTO{
			PlayerID: l.HostPlayerID, Faction: l.Faction, IsReady: false, IsFinished: false, JoinedAt: "",
		}
	}
	return out
}

type missionCondition struct {
	ID          int64  `json:"id"`
	ModeName    string `json:"modeName"`
	WeatherName string `json:"weatherName"`
	Description string `json:"description"`
}

type lobbyPlayer struct {
	PlayerID   int64
	Faction    string
	IsReady    bool
	IsFinished bool
	JoinedAt   string
}

type customConditionsRequest struct {
	MissionName    string `json:"missionName"`
	WeatherName    string `json:"weatherName"`
	AtmosphereName string `json:"atmosphereName"`
}

type rankedResultRequest struct {
	WinnerPlayerID int64 `json:"winnerPlayerId"`
	IsDraw         bool  `json:"isDraw"`
}

func (a *api) createLobby(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	var hostPlayerID int64
	if err := json.Unmarshal(raw["hostPlayerId"], &hostPlayerID); err != nil || hostPlayerID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hostPlayerId is required"})
		return
	}
	var matchSize int
	if err := json.Unmarshal(raw["matchSize"], &matchSize); err != nil || matchSize <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "matchSize is required"})
		return
	}
	isRanked := false
	if br, ok := raw["isRanked"]; ok {
		_ = json.Unmarshal(br, &isRanked)
	}

	slotKey := playerSlotKey(hostPlayerID)
	if err := validateCreateLobbyKeys(raw, slotKey); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	hostFaction, ferr := factionFromPlayerSlot(raw, hostPlayerID)
	if ferr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ferr.Error()})
		return
	}

	var meetingPlace string
	if err := json.Unmarshal(raw["meetingPlace"], &meetingPlace); err != nil || strings.TrimSpace(meetingPlace) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "meetingPlace is required"})
		return
	}
	meetingPlace = strings.TrimSpace(meetingPlace)

	var exists int
	err = a.db.QueryRow(`SELECT 1 FROM players WHERE id = $1`, hostPlayerID).Scan(&exists)
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
INSERT INTO lobbies (host_player_id, faction, match_size, is_ranked, meeting_place, mission_condition_id, status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, 'open', $7, $8)
RETURNING id
`, hostPlayerID, hostFaction, matchSize, isRanked, meetingPlace, conditionID, now, now).Scan(&id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create lobby"})
		return
	}

	_, _ = a.db.Exec(`
INSERT INTO lobby_players (lobby_id, player_id, faction_name, is_ready, is_finished, joined_at)
VALUES ($1, $2, $3, FALSE, FALSE, $4)
ON CONFLICT (lobby_id, player_id) DO NOTHING
`, id, hostPlayerID, hostFaction, now)

	l, err := a.getLobbyByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load created lobby"})
		return
	}

	writeJSON(w, http.StatusCreated, lobbyToJSON(l))
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

	writeJSON(w, http.StatusOK, lobbyToJSON(l))
}

func (a *api) getLobbyByID(id int64) (lobby, error) {
	var l lobby
	var conditionID sql.NullInt64
	var customMission, customWeather, customAtmosphere sql.NullString
	var startedAt, finishedAt sql.NullString
	err := a.db.QueryRow(`
SELECT id, host_player_id, faction, meeting_place, match_size, is_ranked, mission_condition_id,
       custom_mission_name, custom_weather_name, custom_atmosphere_name,
       status, started_at, finished_at, created_at, updated_at
FROM lobbies
WHERE id = $1
`, id).Scan(
		&l.ID,
		&l.HostPlayerID,
		&l.Faction,
		&l.MeetingPlace,
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
	writeJSON(w, http.StatusOK, lobbyToJSON(updatedLobby))
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

	body, rerr := io.ReadAll(r.Body)
	if rerr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	var playerID int64
	if err := json.Unmarshal(raw["playerId"], &playerID); err != nil || playerID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "playerId is required"})
		return
	}
	slotKey := playerSlotKey(playerID)
	if err := validateJoinLobbyKeys(raw, slotKey); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	faction, ferr := factionFromPlayerSlot(raw, playerID)
	if ferr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ferr.Error()})
		return
	}
	authPlayerID, err := a.requirePlayer(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if authPlayerID != playerID {
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
`, lobbyID, playerID, faction, now)
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
	writeJSON(w, http.StatusOK, lobbyToJSON(updatedLobby))
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
	writeJSON(w, http.StatusOK, lobbyToJSON(updatedLobby))
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
	writeJSON(w, http.StatusOK, lobbyToJSON(updatedLobby))
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
		type lobbyMember struct {
			playerID    int64
			factionName string
		}
		var members []lobbyMember
		for rows.Next() {
			var m lobbyMember
			if scanErr := rows.Scan(&m.playerID, &m.factionName); scanErr != nil {
				_ = rows.Close()
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read lobby players"})
				return
			}
			members = append(members, m)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read lobby players"})
			return
		}
		_ = rows.Close()

		for _, m := range members {
			var factionsRaw string
			var totalExperience int
			if err := tx.QueryRow(`SELECT factions, total_experience FROM players WHERE id = $1`, m.playerID).Scan(&factionsRaw, &totalExperience); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load player progress"})
				return
			}
			var factions []string
			_ = json.Unmarshal([]byte(factionsRaw), &factions)
			if !containsString(factions, m.factionName) {
				factions = append(factions, m.factionName)
			}
			updatedFactions, _ := json.Marshal(factions)
			_, err = tx.Exec(`
UPDATE players
SET total_experience = $1, factions = $2, updated_at = $3
WHERE id = $4
`, totalExperience+1, string(updatedFactions), now, m.playerID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update player progression"})
				return
			}
			_, err = tx.Exec(`
INSERT INTO player_faction_experience (player_id, faction_name, experience)
VALUES ($1, $2, 1)
ON CONFLICT (player_id, faction_name)
DO UPDATE SET experience = player_faction_experience.experience + 1
`, m.playerID, m.factionName)
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
	writeJSON(w, http.StatusOK, lobbyToJSON(updatedLobby))
}

func (a *api) submitRankedResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lobbyID, ok := parseLobbyActionID(r.URL.Path, "ranked-result")
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	authPlayerID, err := a.requirePlayer(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req rankedResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if !req.IsDraw && req.WinnerPlayerID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "winnerPlayerId is required for non-draw result"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	var isRanked bool
	var status string
	var ratingApplied bool
	if err := tx.QueryRow(`SELECT is_ranked, status, rating_applied FROM lobbies WHERE id = $1`, lobbyID).Scan(&isRanked, &status, &ratingApplied); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "lobby not found"})
		return
	}
	if !isRanked {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "glicko is only for ranked lobbies"})
		return
	}
	if ratingApplied {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rating already applied for this lobby"})
		return
	}
	if status != "started" && status != "finished" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "match has not started yet"})
		return
	}

	rows, err := tx.Query(`SELECT player_id FROM lobby_players WHERE lobby_id = $1 ORDER BY joined_at`, lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby players"})
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) != 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ranked result requires exactly 2 players"})
		return
	}
	if authPlayerID != ids[0] && authPlayerID != ids[1] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	p1, p2 := ids[0], ids[1]
	if !req.IsDraw && req.WinnerPlayerID != p1 && req.WinnerPlayerID != p2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "winnerPlayerId must be one of lobby players"})
		return
	}

	var r1, r2 int
	var rd1, rd2 float64
	if err := tx.QueryRow(`SELECT rating, rating_rd FROM players WHERE id = $1`, p1).Scan(&r1, &rd1); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load player ratings"})
		return
	}
	if err := tx.QueryRow(`SELECT rating, rating_rd FROM players WHERE id = $1`, p2).Scan(&r2, &rd2); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load player ratings"})
		return
	}

	s1, s2 := 0.5, 0.5
	if !req.IsDraw {
		if req.WinnerPlayerID == p1 {
			s1, s2 = 1, 0
		} else {
			s1, s2 = 0, 1
		}
	}

	newR1, newRD1 := glickoUpdate(float64(r1), rd1, float64(r2), rd2, s1)
	newR2, newRD2 := glickoUpdate(float64(r2), rd2, float64(r1), rd1, s2)

	if _, err := tx.Exec(`UPDATE players SET rating = $1, rating_rd = $2, updated_at = $3 WHERE id = $4`,
		int(math.Round(newR1)), newRD1, now, p1); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update player rating"})
		return
	}
	if _, err := tx.Exec(`UPDATE players SET rating = $1, rating_rd = $2, updated_at = $3 WHERE id = $4`,
		int(math.Round(newR2)), newRD2, now, p2); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update player rating"})
		return
	}

	_, _ = tx.Exec(`
INSERT INTO rating_history (lobby_id, player_id, old_rating, new_rating, old_rd, new_rd, score, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8), ($1,$9,$10,$11,$12,$13,$14,$8)
`, lobbyID, p1, r1, int(math.Round(newR1)), rd1, newRD1, s1, now, p2, r2, int(math.Round(newR2)), rd2, newRD2, s2)

	_, _ = tx.Exec(`UPDATE lobbies SET rating_applied = TRUE, status = 'finished', finished_at = $1, updated_at = $1 WHERE id = $2`, now, lobbyID)

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to commit rating update"})
		return
	}

	updatedLobby, err := a.getLobbyByID(lobbyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load lobby"})
		return
	}
	writeJSON(w, http.StatusOK, lobbyToJSON(updatedLobby))
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

func glickoUpdate(r, rd, oppR, oppRD, score float64) (float64, float64) {
	const q = 0.005756462732485115 // ln(10)/400
	rd = clampRD(rd)
	oppRD = clampRD(oppRD)

	g := 1 / math.Sqrt(1+((3*q*q*oppRD*oppRD)/(math.Pi*math.Pi)))
	e := 1 / (1 + math.Pow(10, (-g*(r-oppR))/400))
	d2 := 1 / (q*q*g*g*e*(1-e))
	pre := 1/(rd*rd) + 1/d2
	newRD := math.Sqrt(1 / pre)
	newR := r + (q/pre)*g*(score-e)

	return newR, clampRD(newRD)
}

func clampRD(rd float64) float64 {
	if rd < 30 {
		return 30
	}
	if rd > 350 {
		return 350
	}
	return rd
}
