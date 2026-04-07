package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
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
		"meetingPlace": "Main Club",
		"matchSize":    350,
	}
	createLobbyBody[fmt.Sprintf("player%d", playerResp.ID)] = map[string]any{"faction": "Clan Wolf"}
	lobbyPayload, _ := json.Marshal(createLobbyBody)
	lobbyReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(lobbyPayload))
	lobbyReq.Header.Set("Content-Type", "application/json")
	lobbyRec := httptest.NewRecorder()
	handler.ServeHTTP(lobbyRec, lobbyReq)

	if lobbyRec.Code != http.StatusCreated {
		t.Fatalf("expected status %d for lobby creation, got %d, body=%s", http.StatusCreated, lobbyRec.Code, lobbyRec.Body.String())
	}

	var lobbyResp map[string]any
	if err := json.Unmarshal(lobbyRec.Body.Bytes(), &lobbyResp); err != nil {
		t.Fatalf("unmarshal lobby response: %v", err)
	}
	if int64(lobbyResp["hostPlayerId"].(float64)) != playerResp.ID {
		t.Fatalf("expected hostPlayerId %d, got %v", playerResp.ID, lobbyResp["hostPlayerId"])
	}
	slotKey := fmt.Sprintf("player%d", playerResp.ID)
	hostSlot, ok := lobbyResp[slotKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %s object in lobby response", slotKey)
	}
	if hostSlot["faction"] != "Clan Wolf" {
		t.Fatalf("expected %s.faction Clan Wolf, got %v", slotKey, hostSlot["faction"])
	}
	if int(lobbyResp["matchSize"].(float64)) != 350 {
		t.Fatalf("expected matchSize 350, got %v", lobbyResp["matchSize"])
	}
	if lobbyResp["status"] != "open" {
		t.Fatalf("expected status open, got %q", lobbyResp["status"])
	}
}

func TestCreateLobbyFailsForUnknownHostPlayer(t *testing.T) {
	_, handler := setupTestServer(t)

	createLobbyBody := map[string]any{
		"hostPlayerId": int64(9999),
		"meetingPlace": "Main Club",
		"matchSize":    300,
		"player9999":   map[string]any{"faction": "Clan Jade Falcon"},
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
	rankedBody := map[string]any{
		"hostPlayerId": playerID,
		"meetingPlace": "Main Club",
		"matchSize":    350,
		"isRanked":     true,
	}
	rankedBody[fmt.Sprintf("player%d", playerID)] = map[string]any{"faction": "Clan Wolf"}
	rankedPayload, _ := json.Marshal(rankedBody)
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
	casualBody := map[string]any{
		"hostPlayerId": playerID,
		"meetingPlace": "Main Club",
		"matchSize":    300,
		"isRanked":     false,
	}
	casualBody[fmt.Sprintf("player%d", playerID)] = map[string]any{"faction": "Clan Jade Falcon"}
	casualPayload, _ := json.Marshal(casualBody)
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

	listReq := httptest.NewRequest(http.MethodGet, "/admin/players?limit=10&offset=0&sort=nickname_asc", nil)
	listReq.Header.Set("Authorization", authHeader)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("admin list players expected %d, got %d, body=%s", http.StatusOK, listRec.Code, listRec.Body.String())
	}
	var listOut struct {
		Total int `json:"total"`
		Items []struct {
			Nickname string `json:"nickname"`
			Role     string `json:"role"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listOut); err != nil {
		t.Fatalf("unmarshal admin list: %v", err)
	}
	if listOut.Total < 1 || len(listOut.Items) < 1 {
		t.Fatalf("expected non-empty admin player list, total=%d items=%d", listOut.Total, len(listOut.Items))
	}

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
	admLobby := map[string]any{"hostPlayerId": playerID, "meetingPlace": "Main Club", "matchSize": 350}
	admLobby[fmt.Sprintf("player%d", playerID)] = map[string]any{"faction": "Clan Wolf"}
	lobbyPayload, _ := json.Marshal(admLobby)
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
	tgtLobby := map[string]any{"hostPlayerId": targetID, "meetingPlace": "Main Club", "matchSize": 350}
	tgtLobby[fmt.Sprintf("player%d", targetID)] = map[string]any{"faction": "Clan Wolf"}
	targetLobbyPayload, _ := json.Marshal(tgtLobby)
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

	// Ensure admin can read history collection and that extended fields are present.
	historyListReq := httptest.NewRequest(http.MethodGet, "/admin/lobbies-history?limit=10&offset=0&sort=id_desc", nil)
	historyListReq.Header.Set("Authorization", authHeader)
	historyListRec := httptest.NewRecorder()
	handler.ServeHTTP(historyListRec, historyListReq)
	if historyListRec.Code != http.StatusOK {
		t.Fatalf("admin list lobbies-history expected %d, got %d, body=%s", http.StatusOK, historyListRec.Code, historyListRec.Body.String())
	}
	var histOut struct {
		Items []struct {
			MeetingPlace string `json:"meetingPlace"`
			IsRanked     bool   `json:"isRanked"`
		} `json:"items"`
	}
	if err := json.Unmarshal(historyListRec.Body.Bytes(), &histOut); err != nil {
		t.Fatalf("unmarshal history list response: %v", err)
	}
	if len(histOut.Items) > 0 && histOut.Items[0].MeetingPlace == "" {
		t.Fatalf("expected meetingPlace in history list item")
	}
}

func TestAdminPlayersCollectionShowsFirstThreePlayers(t *testing.T) {
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
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal player response: %v", err)
		}
		return out.ID
	}

	p1 := createPlayer(t, "A1")
	p2 := createPlayer(t, "A2")
	p3 := createPlayer(t, "A3")

	// Keep admin auth working, but emulate incomplete credentials for one user.
	if _, err := database.Exec(`UPDATE player_credentials SET role = 'admin' WHERE player_id = $1`, p1); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	if _, err := database.Exec(`DELETE FROM player_credentials WHERE player_id = $1`, p3); err != nil {
		t.Fatalf("delete credentials for p3: %v", err)
	}

	loginPayload, _ := json.Marshal(map[string]any{"nickname": "A1", "password": "StrongPass123!"})
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginPayload))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login expected %d, got %d, body=%s", http.StatusOK, loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(loginRec.Body.Bytes(), &loginResp)

	req := httptest.NewRequest(http.MethodGet, "/admin/players?sort=id_asc&limit=3&offset=0", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list players expected %d, got %d, body=%s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var out struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal admin players list: %v", err)
	}
	if len(out.Items) != 3 {
		t.Fatalf("expected first 3 players in page, got %d", len(out.Items))
	}
	if out.Items[0].ID != p1 || out.Items[1].ID != p2 || out.Items[2].ID != p3 {
		t.Fatalf("expected ids [%d,%d,%d], got [%d,%d,%d]", p1, p2, p3, out.Items[0].ID, out.Items[1].ID, out.Items[2].ID)
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

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/admin/players", nil)
	listReq.Header.Set("Authorization", authHeader)
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusForbidden {
		t.Fatalf("admin list players expected %d for non-admin, got %d, body=%s", http.StatusForbidden, listRec.Code, listRec.Body.String())
	}

	historyListRec := httptest.NewRecorder()
	historyListReq := httptest.NewRequest(http.MethodGet, "/admin/lobbies-history", nil)
	historyListReq.Header.Set("Authorization", authHeader)
	handler.ServeHTTP(historyListRec, historyListReq)
	if historyListRec.Code != http.StatusForbidden {
		t.Fatalf("admin list lobbies-history expected %d for non-admin, got %d, body=%s", http.StatusForbidden, historyListRec.Code, historyListRec.Body.String())
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
	flowLobby := map[string]any{"hostPlayerId": p1, "meetingPlace": "Main Club", "matchSize": 350, "isRanked": false}
	flowLobby[fmt.Sprintf("player%d", p1)] = map[string]any{"faction": "Clan Wolf"}
	createLobbyPayload, _ := json.Marshal(flowLobby)
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
	joinBody := map[string]any{"playerId": p2}
	joinBody[fmt.Sprintf("player%d", p2)] = map[string]any{"faction": "Clan Jade Falcon"}
	joinPayload, _ := json.Marshal(joinBody)
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

	var historyCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM lobbies_history WHERE original_lobby_id = $1`, lobbyResp.ID).Scan(&historyCount); err != nil {
		t.Fatalf("count lobby history rows: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("expected finished casual lobby to be archived once, got %d rows", historyCount)
	}

	var origRanked bool
	var origMeeting string
	var origMissionID sql.NullInt64
	var origCustomMission, origCustomWeather, origCustomAtmos sql.NullString
	if err := database.QueryRow(`
SELECT is_ranked, meeting_place, mission_condition_id, custom_mission_name, custom_weather_name, custom_atmosphere_name
FROM lobbies
WHERE id = $1
`, lobbyResp.ID).Scan(&origRanked, &origMeeting, &origMissionID, &origCustomMission, &origCustomWeather, &origCustomAtmos); err != nil {
		t.Fatalf("load original casual lobby fields: %v", err)
	}
	var histRanked bool
	var histMeeting string
	var histMissionID sql.NullInt64
	var histCustomMission, histCustomWeather, histCustomAtmos sql.NullString
	if err := database.QueryRow(`
SELECT is_ranked, meeting_place, mission_condition_id, custom_mission_name, custom_weather_name, custom_atmosphere_name
FROM lobbies_history
WHERE original_lobby_id = $1
`, lobbyResp.ID).Scan(&histRanked, &histMeeting, &histMissionID, &histCustomMission, &histCustomWeather, &histCustomAtmos); err != nil {
		t.Fatalf("load archived casual lobby fields: %v", err)
	}
	if origRanked != histRanked || origMeeting != histMeeting ||
		origMissionID.Int64 != histMissionID.Int64 || origMissionID.Valid != histMissionID.Valid ||
		origCustomMission.String != histCustomMission.String || origCustomMission.Valid != histCustomMission.Valid ||
		origCustomWeather.String != histCustomWeather.String || origCustomWeather.Valid != histCustomWeather.Valid ||
		origCustomAtmos.String != histCustomAtmos.String || origCustomAtmos.Valid != histCustomAtmos.Valid {
		t.Fatalf("archived casual lobby fields mismatch original")
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

func TestRankedResultAppliesGlickoOnce(t *testing.T) {
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
		var out struct{ ID int64 `json:"id"` }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.ID
	}
	loginToken := func(t *testing.T, nickname string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"nickname": nickname, "password": "StrongPass123!"})
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login expected %d, got %d", http.StatusOK, rec.Code)
		}
		var out struct{ Token string `json:"token"` }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Token
	}

	p1 := createPlayer(t, "Rated1")
	p2 := createPlayer(t, "Rated2")
	p1Token := loginToken(t, "Rated1")
	p2Token := loginToken(t, "Rated2")

	rankedLobby := map[string]any{"hostPlayerId": p1, "meetingPlace": "Main Club", "matchSize": 350, "isRanked": true}
	rankedLobby[fmt.Sprintf("player%d", p1)] = map[string]any{"faction": "Clan Wolf"}
	createLobbyPayload, _ := json.Marshal(rankedLobby)
	createReq := httptest.NewRequest(http.MethodPost, "/lobbies", bytes.NewReader(createLobbyPayload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create ranked lobby expected %d, got %d, body=%s", http.StatusCreated, createRec.Code, createRec.Body.String())
	}
	var lobby struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal(createRec.Body.Bytes(), &lobby)

	rankedJoin := map[string]any{"playerId": p2}
	rankedJoin[fmt.Sprintf("player%d", p2)] = map[string]any{"faction": "Clan Jade Falcon"}
	joinPayload, _ := json.Marshal(rankedJoin)
	joinReq := httptest.NewRequest(http.MethodPost, "/lobbies/"+strconv.FormatInt(lobby.ID, 10)+"/join", bytes.NewReader(joinPayload))
	joinReq.Header.Set("Content-Type", "application/json")
	joinReq.Header.Set("Authorization", "Bearer "+p2Token)
	joinRec := httptest.NewRecorder()
	handler.ServeHTTP(joinRec, joinReq)
	if joinRec.Code != http.StatusOK {
		t.Fatalf("join expected %d, got %d", http.StatusOK, joinRec.Code)
	}

	readyReq := func(playerID int64, token string) {
		payload, _ := json.Marshal(map[string]any{"playerId": playerID})
		req := httptest.NewRequest(http.MethodPost, "/lobbies/"+strconv.FormatInt(lobby.ID, 10)+"/ready", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ready expected %d, got %d", http.StatusOK, rec.Code)
		}
	}
	readyReq(p1, p1Token)
	readyReq(p2, p2Token)

	var oldR1, oldR2 int
	_ = database.QueryRow(`SELECT rating FROM players WHERE id = $1`, p1).Scan(&oldR1)
	_ = database.QueryRow(`SELECT rating FROM players WHERE id = $1`, p2).Scan(&oldR2)

	resultPayload, _ := json.Marshal(map[string]any{"winnerPlayerId": p1, "isDraw": false})
	resultReq := httptest.NewRequest(http.MethodPost, "/lobbies/"+strconv.FormatInt(lobby.ID, 10)+"/ranked-result", bytes.NewReader(resultPayload))
	resultReq.Header.Set("Content-Type", "application/json")
	resultReq.Header.Set("Authorization", "Bearer "+p1Token)
	resultRec := httptest.NewRecorder()
	handler.ServeHTTP(resultRec, resultReq)
	if resultRec.Code != http.StatusOK {
		t.Fatalf("ranked-result expected %d, got %d, body=%s", http.StatusOK, resultRec.Code, resultRec.Body.String())
	}

	var newR1, newR2 int
	_ = database.QueryRow(`SELECT rating FROM players WHERE id = $1`, p1).Scan(&newR1)
	_ = database.QueryRow(`SELECT rating FROM players WHERE id = $1`, p2).Scan(&newR2)
	if newR1 <= oldR1 {
		t.Fatalf("expected winner rating increase: old=%d new=%d", oldR1, newR1)
	}
	if newR2 >= oldR2 {
		t.Fatalf("expected loser rating decrease: old=%d new=%d", oldR2, newR2)
	}

	var historyCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM lobbies_history WHERE original_lobby_id = $1`, lobby.ID).Scan(&historyCount); err != nil {
		t.Fatalf("count ranked lobby history rows: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("expected finished ranked lobby to be archived once, got %d rows", historyCount)
	}

	var origRanked bool
	var origMeeting string
	var origMissionID sql.NullInt64
	var origCustomMission, origCustomWeather, origCustomAtmos sql.NullString
	if err := database.QueryRow(`
SELECT is_ranked, meeting_place, mission_condition_id, custom_mission_name, custom_weather_name, custom_atmosphere_name
FROM lobbies
WHERE id = $1
`, lobby.ID).Scan(&origRanked, &origMeeting, &origMissionID, &origCustomMission, &origCustomWeather, &origCustomAtmos); err != nil {
		t.Fatalf("load original ranked lobby fields: %v", err)
	}
	var histRanked bool
	var histMeeting string
	var histMissionID sql.NullInt64
	var histCustomMission, histCustomWeather, histCustomAtmos sql.NullString
	if err := database.QueryRow(`
SELECT is_ranked, meeting_place, mission_condition_id, custom_mission_name, custom_weather_name, custom_atmosphere_name
FROM lobbies_history
WHERE original_lobby_id = $1
`, lobby.ID).Scan(&histRanked, &histMeeting, &histMissionID, &histCustomMission, &histCustomWeather, &histCustomAtmos); err != nil {
		t.Fatalf("load archived ranked lobby fields: %v", err)
	}
	if origRanked != histRanked || origMeeting != histMeeting ||
		origMissionID.Int64 != histMissionID.Int64 || origMissionID.Valid != histMissionID.Valid ||
		origCustomMission.String != histCustomMission.String || origCustomMission.Valid != histCustomMission.Valid ||
		origCustomWeather.String != histCustomWeather.String || origCustomWeather.Valid != histCustomWeather.Valid ||
		origCustomAtmos.String != histCustomAtmos.String || origCustomAtmos.Valid != histCustomAtmos.Valid {
		t.Fatalf("archived ranked lobby fields mismatch original")
	}

	// second attempt must fail (rating_applied protection)
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, resultReq)
	if secondRec.Code != http.StatusBadRequest {
		t.Fatalf("expected second ranked-result to fail with %d, got %d", http.StatusBadRequest, secondRec.Code)
	}
}
