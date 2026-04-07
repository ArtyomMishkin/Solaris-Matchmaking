package httpapi

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

type api struct {
	db        *sql.DB
	jwtSecret []byte
}

func NewRouter(db *sql.DB) http.Handler {
	secret := os.Getenv("JWT_SECRET")
	var jwtSecret []byte
	if secret != "" {
		jwtSecret = []byte(secret)
	} else {
		jwtSecret = make([]byte, 32)
		if _, err := rand.Read(jwtSecret); err != nil {
			// Extremely unlikely fallback to keep server running in dev.
			jwtSecret = []byte("dev-jwt-secret-change-me")
		}
	}
	a := &api{db: db, jwtSecret: jwtSecret}
	mux := http.NewServeMux()

	mux.HandleFunc("/health", a.health)
	mux.HandleFunc("/auth/login", a.login)
	mux.HandleFunc("/players", a.players)
	mux.HandleFunc("/players/", a.playerByID)
	mux.HandleFunc("/lobbies", a.lobbies)
	mux.HandleFunc("/lobbies/", a.lobbyByID)
	mux.HandleFunc("/mission-conditions", a.listMissionConditions)

	mux.HandleFunc("/admin/players/", a.adminPlayersSubresource)
	mux.HandleFunc("/admin/lobbies/", a.adminLobbiesSubresource)
	mux.HandleFunc("/admin/lobbies-history", a.adminLobbiesHistoryCollection)
	mux.HandleFunc("/admin/lobbies-history/", a.adminLobbiesHistorySubresource)
	mux.HandleFunc("/admin/players", a.adminPlayersCollection)

	return mux
}

func (a *api) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *api) players(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.createPlayer(w, r)
}

func (a *api) playerByID(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.Trim(r.URL.Path, "/"), "faction-experience") {
		a.getPlayerFactionExperience(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.getPlayer(w, r)
}

func (a *api) lobbies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.createLobby(w, r)
}

func (a *api) lobbyByID(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(r.URL.Path, "/")
	switch {
	case strings.HasSuffix(trimmed, "random-condition"):
		a.randomizeLobbyMissionCondition(w, r)
		return
	case strings.HasSuffix(trimmed, "join"):
		a.joinLobby(w, r)
		return
	case strings.HasSuffix(trimmed, "conditions"):
		a.setCustomConditions(w, r)
		return
	case strings.HasSuffix(trimmed, "ready"):
		a.markLobbyReady(w, r)
		return
	case strings.HasSuffix(trimmed, "match-finished"):
		a.markMatchFinished(w, r)
		return
	case strings.HasSuffix(trimmed, "ranked-result"):
		a.submitRankedResult(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.getLobby(w, r)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
