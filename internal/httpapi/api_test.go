package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"solaris-matchmaking/internal/db"
	"solaris-matchmaking/internal/httpapi"
)

func setupTestServer(t *testing.T) (*sql.DB, http.Handler) {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	database, err := db.OpenAndMigrate(databaseURL)
	if err != nil {
		t.Fatalf("open and migrate database: %v", err)
	}

	_, _ = database.Exec(`TRUNCATE TABLE
		lobby_players,
		player_faction_experience,
		lobbies_history,
		lobbies,
		player_credentials,
		players
	RESTART IDENTITY CASCADE`)

	t.Cleanup(func() {
		_ = database.Close()
	})

	return database, httpapi.NewRouter(database)
}

func TestHealthEndpoint(t *testing.T) {
	_, handler := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestRegisterPlayerAndCreateLobbyFlow(t *testing.T) {
	database, handler := setupTestServer(t)

	createPlayerBody := map[string]any{
		"fullName":          "Иван Петров",
		"nickname":          "WolfGuard",
		"city":              "Moscow",
		"contacts":          "@wolfguard",
		"preferredLocation": "North Club",
		"password":          "StrongPass123!",
	}
	playerPayload, _ := json.Marshal(createPlayerBody)
	playerReq := httptest.NewRequest(http.MethodPost, "/players", bytes.NewReader(playerPayload))
	playerReq.Header.Set("Content-Type", "application/json")
	playerRec := httptest.NewRecorder()
	handler.ServeHTTP(playerRec, playerReq)

	if playerRec.Code != http.StatusCreated {
		t.Fatalf("expected status %d for player creation, got %d, body=%s", http.StatusCreated, playerRec.Code, playerRec.Body.String())
	}

	var playerResp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(playerRec.Body.Bytes(), &playerResp); err != nil {
		t.Fatalf("unmarshal player response: %v", err)
	}
	if playerResp.ID <= 0 {
		t.Fatalf("expected positive player id, got %d", playerResp.ID)
	}

	var role string
	err := database.QueryRow(`SELECT role FROM player_credentials WHERE player_id = $1`, playerResp.ID).Scan(&role)
	if err != nil {
		t.Fatalf("load player credentials role: %v", err)
	}
	if role != "player" {
		t.Fatalf("expected role 'player', got %q", role)
	}

	createLobbyBody := map[string]any{
		"hostPlayerId": playerResp.ID,
		"faction":      "Clan Wolf",
		"matchSize":    350,
	}
	lobbyPayload, _ := json.Marshal(createLobbyBody)
	lobbyReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(lobbyPayload))
	lobbyReq.Header.Set("Content-Type", "application/json")
	lobbyRec := httptest.NewRecorder()
	handler.ServeHTTP(lobbyRec, lobbyReq)

	if lobbyRec.Code != http.StatusCreated {
		t.Fatalf("expected status %d for lobby creation, got %d, body=%s", http.StatusCreated, lobbyRec.Code, lobbyRec.Body.String())
	}

	var lobbyResp struct {
		ID           int64  `json:"id"`
		HostPlayerID int64  `json:"hostPlayerId"`
		Faction      string `json:"faction"`
		MatchSize    int    `json:"matchSize"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(lobbyRec.Body.Bytes(), &lobbyResp); err != nil {
		t.Fatalf("unmarshal lobby response: %v", err)
	}

	if lobbyResp.HostPlayerID != playerResp.ID {
		t.Fatalf("expected hostPlayerId %d, got %d", playerResp.ID, lobbyResp.HostPlayerID)
	}
	if lobbyResp.Faction != "Clan Wolf" {
		t.Fatalf("expected faction Clan Wolf, got %q", lobbyResp.Faction)
	}
	if lobbyResp.MatchSize != 350 {
		t.Fatalf("expected matchSize 350, got %d", lobbyResp.MatchSize)
	}
	if lobbyResp.Status != "open" {
		t.Fatalf("expected status open, got %q", lobbyResp.Status)
	}
}

func TestCreateLobbyFailsForUnknownHostPlayer(t *testing.T) {
	_, handler := setupTestServer(t)

	createLobbyBody := map[string]any{
		"hostPlayerId": 9999,
		"faction":      "Clan Jade Falcon",
		"matchSize":    300,
	}
	lobbyPayload, _ := json.Marshal(createLobbyBody)
	lobbyReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(lobbyPayload))
	lobbyReq.Header.Set("Content-Type", "application/json")
	lobbyRec := httptest.NewRecorder()
	handler.ServeHTTP(lobbyRec, lobbyReq)

	if lobbyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusBadRequest, lobbyRec.Code, lobbyRec.Body.String())
	}
}

func TestMissionConditionsOnlyForCasualLobbies(t *testing.T) {
	_, handler := setupTestServer(t)

	createPlayer := func(t *testing.T, nickname string) int64 {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"fullName":          "Player " + nickname,
			"nickname":          nickname,
			"city":              "Moscow",
			"contacts":          "@contact",
			"preferredLocation": "Main Club",
			"password":          "StrongPass123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/players", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create player expected %d, got %d, body=%s", http.StatusCreated, rec.Code, rec.Body.String())
		}
		var out struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.ID
	}

	playerID := createPlayer(t, "CondNick")

	// Ranked lobby should NOT get condition automatically.
	rankedPayload, _ := json.Marshal(map[string]any{
		"hostPlayerId": playerID,
		"faction":      "Clan Wolf",
		"matchSize":    350,
		"isRanked":     true,
	})
	rankedReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(rankedPayload))
	rankedReq.Header.Set("Content-Type", "application/json")
	rankedRec := httptest.NewRecorder()
	handler.ServeHTTP(rankedRec, rankedReq)
	if rankedRec.Code != http.StatusCreated {
		t.Fatalf("create ranked lobby expected %d, got %d, body=%s", http.StatusCreated, rankedRec.Code, rankedRec.Body.String())
	}
	var rankedResp struct {
		ID               int64 `json:"id"`
		IsRanked         bool  `json:"isRanked"`
		MissionCondition *struct {
			ID int64 `json:"id"`
		} `json:"missionCondition"`
	}
	_ = json.Unmarshal(rankedRec.Body.Bytes(), &rankedResp)
	if !rankedResp.IsRanked {
		t.Fatalf("expected ranked lobby")
	}
	if rankedResp.MissionCondition != nil {
		t.Fatalf("did not expect mission condition for ranked lobby")
	}

	// Casual lobby can randomize condition later.
	casualPayload, _ := json.Marshal(map[string]any{
		"hostPlayerId": playerID,
		"faction":      "Clan Jade Falcon",
		"matchSize":    300,
		"isRanked":     false,
	})
	casualReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(casualPayload))
	casualReq.Header.Set("Content-Type", "application/json")
	casualRec := httptest.NewRecorder()
	handler.ServeHTTP(casualRec, casualReq)
	if casualRec.Code != http.StatusCreated {
		t.Fatalf("create casual lobby expected %d, got %d, body=%s", http.StatusCreated, casualRec.Code, casualRec.Body.String())
	}
	var casualResp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(casualRec.Body.Bytes(), &casualResp)

	randomReq := httptest.NewRequest(http.MethodPost, "/lobbies/"+strconv.FormatInt(casualResp.ID, 10)+"/random-condition", nil)
	randomRec := httptest.NewRecorder()
	handler.ServeHTTP(randomRec, randomReq)
	if randomRec.Code != http.StatusOK {
		t.Fatalf("random condition expected %d, got %d, body=%s", http.StatusOK, randomRec.Code, randomRec.Body.String())
	}
	var randomResp struct {
		MissionCondition *struct {
			ID int64 `json:"id"`
		} `json:"missionCondition"`
	}
	_ = json.Unmarshal(randomRec.Body.Bytes(), &randomResp)
	if randomResp.MissionCondition == nil || randomResp.MissionCondition.ID <= 0 {
		t.Fatalf("expected mission condition after randomization")
	}
}

func TestAdminSetRankAndDeleteLobbyAndPlayer(t *testing.T) {
	database, handler := setupTestServer(t)

	createPlayer := func(t *testing.T, body map[string]any) int64 {
		t.Helper()

		payload, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/players", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status %d, got %d, body=%s", http.StatusCreated, rec.Code, rec.Body.String())
		}

		var out struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal player response: %v", err)
		}
		return out.ID
	}

	playerID := createPlayer(t, map[string]any{
		"fullName":          "Admin Player",
		"nickname":          "AdminNick",
		"city":              "Moscow",
		"contacts":          "@admin",
		"preferredLocation": "North Club",
		"password":          "StrongPass123!",
	})

	// Promote to admin for tests (bypass CLI/auth).
	_, err := database.Exec(`UPDATE player_credentials SET role = 'admin' WHERE player_id = $1`, playerID)
	if err != nil {
		t.Fatalf("promote test admin: %v", err)
	}

	// Login and get JWT.
	loginReq := map[string]any{"nickname": "AdminNick", "password": "StrongPass123!"}
	loginPayload, _ := json.Marshal(loginReq)
	loginHTTPReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginPayload))
	loginHTTPReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginHTTPReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login expected status %d, got %d, body=%s", http.StatusOK, loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	if loginResp.Token == "" {
		t.Fatalf("expected token")
	}
	authHeader := "Bearer " + loginResp.Token

	// Set rank.
	setRankReq := map[string]any{
		"rankTitle":      "Captain",
		"rankAttestedAt": "2026-03-24",
	}
	setRankPayload, _ := json.Marshal(setRankReq)
	setRankHTTPReq := httptest.NewRequest(http.MethodPut, "/admin/players/"+strconv.FormatInt(playerID, 10)+"/rank", bytes.NewReader(setRankPayload))
	setRankHTTPReq.Header.Set("Content-Type", "application/json")
	setRankHTTPReq.Header.Set("Authorization", authHeader)
	setRankRec := httptest.NewRecorder()
	handler.ServeHTTP(setRankRec, setRankHTTPReq)
	if setRankRec.Code != http.StatusOK {
		t.Fatalf("set rank expected status %d, got %d, body=%s", http.StatusOK, setRankRec.Code, setRankRec.Body.String())
	}

	// Create another player (target) to test player deletion.
	targetID := createPlayer(t, map[string]any{
		"fullName":          "Target Player",
		"nickname":          "TargetNick",
		"city":              "Omsk",
		"contacts":          "@target",
		"preferredLocation": "South Club",
		"password":          "StrongPass123!",
	})

	// Create a lobby for admin (lobby deletion test).
	lobbyPayload, _ := json.Marshal(map[string]any{
		"hostPlayerId": playerID,
		"faction":      "Clan Wolf",
		"matchSize":    350,
	})
	lobbyReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(lobbyPayload))
	lobbyReq.Header.Set("Content-Type", "application/json")
	lobbyRec := httptest.NewRecorder()
	handler.ServeHTTP(lobbyRec, lobbyReq)
	if lobbyRec.Code != http.StatusCreated {
		t.Fatalf("create lobby expected %d, got %d, body=%s", http.StatusCreated, lobbyRec.Code, lobbyRec.Body.String())
	}
	var lobbyResp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(lobbyRec.Body.Bytes(), &lobbyResp); err != nil {
		t.Fatalf("unmarshal lobby response: %v", err)
	}
	lobbyID := lobbyResp.ID

	// Insert corresponding history row.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = database.Exec(`
INSERT INTO lobbies_history (original_lobby_id, host_player_id, faction, match_size, status, created_at, updated_at, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $6)
`, lobbyID, playerID, "Clan Wolf", 350, "open", now, now)
	if err != nil {
		t.Fatalf("insert lobby history: %v", err)
	}

	// Delete lobby (should also delete from history by original_lobby_id).
	deleteLobbyReq := httptest.NewRequest(http.MethodDelete, "/admin/lobbies/"+strconv.FormatInt(lobbyID, 10), nil)
	deleteLobbyReq.Header.Set("Authorization", authHeader)
	deleteLobbyRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteLobbyRec, deleteLobbyReq)
	if deleteLobbyRec.Code != http.StatusNoContent {
		t.Fatalf("delete lobby expected status %d, got %d, body=%s", http.StatusNoContent, deleteLobbyRec.Code, deleteLobbyRec.Body.String())
	}

	var lobbyCount int
	_ = database.QueryRow(`SELECT COUNT(1) FROM lobbies WHERE id = $1`, lobbyID).Scan(&lobbyCount)
	if lobbyCount != 0 {
		t.Fatalf("expected lobby to be deleted, count=%d", lobbyCount)
	}
	var historyCount int
	_ = database.QueryRow(`SELECT COUNT(1) FROM lobbies_history WHERE original_lobby_id = $1`, lobbyID).Scan(&historyCount)
	if historyCount != 0 {
		t.Fatalf("expected lobby history to be deleted, count=%d", historyCount)
	}

	// Create lobby for target and corresponding history row, then delete target player.
	targetLobbyPayload, _ := json.Marshal(map[string]any{
		"hostPlayerId": targetID,
		"faction":      "Clan Wolf",
		"matchSize":    350,
	})
	targetLobbyReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(targetLobbyPayload))
	targetLobbyReq.Header.Set("Content-Type", "application/json")
	targetLobbyRec := httptest.NewRecorder()
	handler.ServeHTTP(targetLobbyRec, targetLobbyReq)
	if targetLobbyRec.Code != http.StatusCreated {
		t.Fatalf("create target lobby expected %d, got %d, body=%s", http.StatusCreated, targetLobbyRec.Code, targetLobbyRec.Body.String())
	}
	var targetLobbyResp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(targetLobbyRec.Body.Bytes(), &targetLobbyResp); err != nil {
		t.Fatalf("unmarshal target lobby response: %v", err)
	}
	targetLobbyID := targetLobbyResp.ID

	_, err = database.Exec(`
INSERT INTO lobbies_history (original_lobby_id, host_player_id, faction, match_size, status, created_at, updated_at, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $6)
`, targetLobbyID, targetID, "Clan Wolf", 350, "open", now, now)
	if err != nil {
		t.Fatalf("insert target lobby history: %v", err)
	}

	// Delete target player.
	deletePlayerReq := httptest.NewRequest(http.MethodDelete, "/admin/players/"+strconv.FormatInt(targetID, 10), nil)
	deletePlayerReq.Header.Set("Authorization", authHeader)
	deletePlayerRec := httptest.NewRecorder()
	handler.ServeHTTP(deletePlayerRec, deletePlayerReq)
	if deletePlayerRec.Code != http.StatusNoContent {
		t.Fatalf("delete player expected status %d, got %d, body=%s", http.StatusNoContent, deletePlayerRec.Code, deletePlayerRec.Body.String())
	}

	var playerCount int
	_ = database.QueryRow(`SELECT COUNT(1) FROM players WHERE id = $1`, targetID).Scan(&playerCount)
	if playerCount != 0 {
		t.Fatalf("expected player to be deleted, count=%d", playerCount)
	}

	var targetLobbyCount int
	_ = database.QueryRow(`SELECT COUNT(1) FROM lobbies WHERE host_player_id = $1`, targetID).Scan(&targetLobbyCount)
	if targetLobbyCount != 0 {
		t.Fatalf("expected target lobbies to be deleted, count=%d", targetLobbyCount)
	}
	var targetHistoryCount int
	_ = database.QueryRow(`SELECT COUNT(1) FROM lobbies_history WHERE host_player_id = $1`, targetID).Scan(&targetHistoryCount)
	if targetHistoryCount != 0 {
		t.Fatalf("expected target lobby history to be deleted, count=%d", targetHistoryCount)
	}
}

func TestNonAdminCannotCallAdminEndpoints(t *testing.T) {
	database, handler := setupTestServer(t)

	playerID := func(t *testing.T, nickname string) int64 {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"fullName":          "User",
			"nickname":          nickname,
			"city":              "City",
			"contacts":          "@contact",
			"preferredLocation": "Loc",
			"password":          "StrongPass123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/players", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status %d, got %d, body=%s", http.StatusCreated, rec.Code, rec.Body.String())
		}
		var out struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.ID
	}

	adminNickname := "AdminNick2"
	adminID := playerID(t, adminNickname)
	_, err := database.Exec(`UPDATE player_credentials SET role = 'admin' WHERE player_id = $1`, adminID)
	if err != nil {
		t.Fatalf("promote admin2: %v", err)
	}

	userNickname := "UserNick2"
	userID := playerID(t, userNickname)
	_ = userID

	// Login as non-admin user.
	loginReq := map[string]any{"nickname": userNickname, "password": "StrongPass123!"}
	loginPayload, _ := json.Marshal(loginReq)
	loginHTTPReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginPayload))
	loginHTTPReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginHTTPReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login expected status %d, got %d, body=%s", http.StatusOK, loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	authHeader := "Bearer " + loginResp.Token

	// Try admin rank endpoint on admin user.
	setRankReq := map[string]any{
		"rankTitle":      "Captain",
		"rankAttestedAt": "2026-03-24",
	}
	setRankPayload, _ := json.Marshal(setRankReq)
	setRankHTTPReq := httptest.NewRequest(http.MethodPut, "/admin/players/"+strconv.FormatInt(adminID, 10)+"/rank", bytes.NewReader(setRankPayload))
	setRankHTTPReq.Header.Set("Content-Type", "application/json")
	setRankHTTPReq.Header.Set("Authorization", authHeader)
	setRankRec := httptest.NewRecorder()
	handler.ServeHTTP(setRankRec, setRankHTTPReq)

	if setRankRec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d, body=%s", http.StatusForbidden, setRankRec.Code, setRankRec.Body.String())
	}
}

func TestCasualLobbyReadyAndFinishGivesExperience(t *testing.T) {
	database, handler := setupTestServer(t)

	createPlayer := func(t *testing.T, nickname string) int64 {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"fullName":          "Player " + nickname,
			"nickname":          nickname,
			"city":              "Moscow",
			"contacts":          "@"+nickname,
			"preferredLocation": "Main Club",
			"password":          "StrongPass123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/players", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create player expected %d, got %d, body=%s", http.StatusCreated, rec.Code, rec.Body.String())
		}
		var out struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.ID
	}

	p1 := createPlayer(t, "FlowP1")
	p2 := createPlayer(t, "FlowP2")

	loginToken := func(t *testing.T, nickname string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"nickname": nickname,
			"password": "StrongPass123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login expected %d, got %d, body=%s", http.StatusOK, rec.Code, rec.Body.String())
		}
		var out struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Token
	}
	p1Token := loginToken(t, "FlowP1")
	p2Token := loginToken(t, "FlowP2")

	// Create casual lobby by player1.
	createLobbyPayload, _ := json.Marshal(map[string]any{
		"hostPlayerId": p1,
		"faction":      "Clan Wolf",
		"matchSize":    350,
		"isRanked":     false,
	})
	createLobbyReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(createLobbyPayload))
	createLobbyReq.Header.Set("Content-Type", "application/json")
	createLobbyRec := httptest.NewRecorder()
	handler.ServeHTTP(createLobbyRec, createLobbyReq)
	if createLobbyRec.Code != http.StatusCreated {
		t.Fatalf("create lobby expected %d, got %d, body=%s", http.StatusCreated, createLobbyRec.Code, createLobbyRec.Body.String())
	}
	var lobbyResp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(createLobbyRec.Body.Bytes(), &lobbyResp)

	// Player2 joins with own faction.
	joinPayload, _ := json.Marshal(map[string]any{
		"playerId": p2,
		"faction":  "Clan Jade Falcon",
	})
	joinReq := httptest.NewRequest(http.MethodPost, "/lobbies/"+strconv.FormatInt(lobbyResp.ID, 10)+"/join", bytes.NewReader(joinPayload))
	joinReq.Header.Set("Content-Type", "application/json")
	joinReq.Header.Set("Authorization", "Bearer "+p2Token)
	joinRec := httptest.NewRecorder()
	handler.ServeHTTP(joinRec, joinReq)
	if joinRec.Code != http.StatusOK {
		t.Fatalf("join lobby expected %d, got %d, body=%s", http.StatusOK, joinRec.Code, joinRec.Body.String())
	}

	// Custom conditions (non-MMR only).
	condPayload, _ := json.Marshal(map[string]any{
		"missionName":    "Capture Base",
		"weatherName":    "Snow",
		"atmosphereName": "Thin",
	})
	condReq := httptest.NewRequest(http.MethodPut, "/lobbies/"+strconv.FormatInt(lobbyResp.ID, 10)+"/conditions", bytes.NewReader(condPayload))
	condReq.Header.Set("Content-Type", "application/json")
	condRec := httptest.NewRecorder()
	handler.ServeHTTP(condRec, condReq)
	if condRec.Code != http.StatusOK {
		t.Fatalf("set conditions expected %d, got %d, body=%s", http.StatusOK, condRec.Code, condRec.Body.String())
	}

	ready := func(playerID int64) {
		token := p1Token
		if playerID == p2 {
			token = p2Token
		}
		payload, _ := json.Marshal(map[string]any{"playerId": playerID})
		req := httptest.NewRequest(http.MethodPost, "/lobbies/"+strconv.FormatInt(lobbyResp.ID, 10)+"/ready", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ready expected %d, got %d, body=%s", http.StatusOK, rec.Code, rec.Body.String())
		}
	}
	ready(p1)
	ready(p2)

	finish := func(playerID int64) {
		token := p1Token
		if playerID == p2 {
			token = p2Token
		}
		payload, _ := json.Marshal(map[string]any{"playerId": playerID})
		req := httptest.NewRequest(http.MethodPost, "/lobbies/"+strconv.FormatInt(lobbyResp.ID, 10)+"/match-finished", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("match-finished expected %d, got %d, body=%s", http.StatusOK, rec.Code, rec.Body.String())
		}
	}
	finish(p1)
	finish(p2)

	var exp1, exp2 int
	var factions1Raw, factions2Raw string
	if err := database.QueryRow(`SELECT total_experience, factions FROM players WHERE id = $1`, p1).Scan(&exp1, &factions1Raw); err != nil {
		t.Fatalf("load p1 progression: %v", err)
	}
	if err := database.QueryRow(`SELECT total_experience, factions FROM players WHERE id = $1`, p2).Scan(&exp2, &factions2Raw); err != nil {
		t.Fatalf("load p2 progression: %v", err)
	}
	if exp1 != 1 || exp2 != 1 {
		t.Fatalf("expected both players to get 1 exp, got p1=%d p2=%d", exp1, exp2)
	}
	if !strings.Contains(factions1Raw, "Clan Wolf") {
		t.Fatalf("expected p1 factions to contain Clan Wolf, got %s", factions1Raw)
	}
	if !strings.Contains(factions2Raw, "Clan Jade Falcon") {
		t.Fatalf("expected p2 factions to contain Clan Jade Falcon, got %s", factions2Raw)
	}

	var p1FactionExp, p2FactionExp int
	if err := database.QueryRow(`SELECT experience FROM player_faction_experience WHERE player_id = $1 AND faction_name = $2`, p1, "Clan Wolf").Scan(&p1FactionExp); err != nil {
		t.Fatalf("load p1 faction experience: %v", err)
	}
	if err := database.QueryRow(`SELECT experience FROM player_faction_experience WHERE player_id = $1 AND faction_name = $2`, p2, "Clan Jade Falcon").Scan(&p2FactionExp); err != nil {
		t.Fatalf("load p2 faction experience: %v", err)
	}
	if p1FactionExp != 1 || p2FactionExp != 1 {
		t.Fatalf("expected faction exp p1=1 p2=1, got p1=%d p2=%d", p1FactionExp, p2FactionExp)
	}
}

func TestGetPlayerFactionExperienceWithPaginationAndSort(t *testing.T) {
	database, handler := setupTestServer(t)

	payload, _ := json.Marshal(map[string]any{
		"fullName":          "Player Stats",
		"nickname":          "StatsNick",
		"city":              "Moscow",
		"contacts":          "@stats",
		"preferredLocation": "Main Club",
		"password":          "StrongPass123!",
	})
	req := httptest.NewRequest(http.MethodPost, "/players", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create player expected %d, got %d, body=%s", http.StatusCreated, rec.Code, rec.Body.String())
	}
	var playerResp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &playerResp)

	_, err := database.Exec(`
INSERT INTO player_faction_experience (player_id, faction_name, experience)
VALUES
($1, 'Clan Wolf', 5),
($1, 'Clan Jade Falcon', 2),
($1, 'Mercenaries', 8)
`, playerResp.ID)
	if err != nil {
		t.Fatalf("insert faction experience: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet,
		"/players/"+strconv.FormatInt(playerResp.ID, 10)+"/faction-experience?sort=exp_desc&limit=2&offset=0", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get faction experience expected %d, got %d, body=%s", http.StatusOK, getRec.Code, getRec.Body.String())
	}

	var out struct {
		PlayerID int64 `json:"playerId"`
		Total    int   `json:"total"`
		Items    []struct {
			Faction    string `json:"faction"`
			Experience int    `json:"experience"`
		} `json:"items"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal faction experience: %v", err)
	}
	if out.PlayerID != playerResp.ID {
		t.Fatalf("expected playerId %d, got %d", playerResp.ID, out.PlayerID)
	}
	if out.Total != 3 {
		t.Fatalf("expected total 3, got %d", out.Total)
	}
	if len(out.Items) != 2 {
		t.Fatalf("expected 2 items by pagination, got %d", len(out.Items))
	}
	if out.Items[0].Faction != "Mercenaries" || out.Items[0].Experience != 8 {
		t.Fatalf("expected top faction Mercenaries(8), got %s(%d)", out.Items[0].Faction, out.Items[0].Experience)
	}
	if out.Items[1].Faction != "Clan Wolf" || out.Items[1].Experience != 5 {
		t.Fatalf("expected second faction Clan Wolf(5), got %s(%d)", out.Items[1].Faction, out.Items[1].Experience)
	}
}
