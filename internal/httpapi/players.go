package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type player struct {
	ID                int64    `json:"id"`
	FullName          string   `json:"fullName"`
	Nickname          string   `json:"nickname"`
	City              string   `json:"city"`
	Contacts          string   `json:"contacts"`
	PreferredLocation string   `json:"preferredLocation"`
	RankTitle         string   `json:"rankTitle,omitempty"`
	RankAttestedAt    string   `json:"rankAttestedAt,omitempty"`
	Factions          []string `json:"factions"`
	Tournaments       []string `json:"tournaments"`
	HobbyEvenings     []string `json:"hobbyEvenings"`
	TotalExperience   int      `json:"totalExperience"`
	OtherEvents       []string `json:"otherEvents"`
	FactionExperience []factionExperience `json:"factionExperience"`
	CollectionLink    string   `json:"collectionLink,omitempty"`
	CreatedAt         string   `json:"createdAt"`
	UpdatedAt         string   `json:"updatedAt"`
}

type factionExperience struct {
	Faction    string `json:"faction"`
	Experience int    `json:"experience"`
}

type createPlayerRequest struct {
	FullName          string `json:"fullName"`
	Nickname          string `json:"nickname"`
	City              string `json:"city"`
	Contacts          string `json:"contacts"`
	PreferredLocation string `json:"preferredLocation"`
	Password          string `json:"password"`
}

func (a *api) createPlayer(w http.ResponseWriter, r *http.Request) {
	var req createPlayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	req.FullName = strings.TrimSpace(req.FullName)
	req.Nickname = strings.TrimSpace(req.Nickname)
	req.City = strings.TrimSpace(req.City)
	req.Contacts = strings.TrimSpace(req.Contacts)
	req.PreferredLocation = strings.TrimSpace(req.PreferredLocation)
	req.Password = strings.TrimSpace(req.Password)

	if req.FullName == "" || req.Nickname == "" || req.City == "" || req.Contacts == "" || req.PreferredLocation == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "fullName, nickname, city, contacts, preferredLocation and password are required",
		})
		return
	}
	if len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to process password"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	factionsJSON := "[]"
	tournamentsJSON := "[]"
	hobbyEveningsJSON := "[]"
	otherEventsJSON := "[]"

	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create player"})
		return
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRow(`
INSERT INTO players (
	full_name, nickname, city, contacts, preferred_location,
	rank_title, rank_attested_at, factions, tournaments, hobby_evenings,
	total_experience, other_events, collection_link, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, '', '', $6, $7, $8, 0, $9, '', $10, $11)
RETURNING id
`,
		req.FullName,
		req.Nickname,
		req.City,
		req.Contacts,
		req.PreferredLocation,
		factionsJSON,
		tournamentsJSON,
		hobbyEveningsJSON,
		otherEventsJSON,
		now,
		now,
	).Scan(&id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "nickname already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create player"})
		return
	}

	_, err = tx.Exec(`
INSERT INTO player_credentials (player_id, password_hash, role, created_at, updated_at)
VALUES ($1, $2, 'player', $3, $4)
`, id, string(passwordHash), now, now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save credentials"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to finalize player creation"})
		return
	}

	p, err := a.getPlayerByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load created player"})
		return
	}

	writeJSON(w, http.StatusCreated, p)
}

func (a *api) getPlayer(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/players/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid player id"})
		return
	}

	p, err := a.getPlayerByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "player not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch player"})
		return
	}

	writeJSON(w, http.StatusOK, p)
}

func (a *api) getPlayerFactionExperience(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/players/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "faction-experience" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	playerID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || playerID <= 0 {
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
	orderClause := "experience DESC, faction_name ASC"
	switch sortBy {
	case "", "exp_desc":
		orderClause = "experience DESC, faction_name ASC"
	case "exp_asc":
		orderClause = "experience ASC, faction_name ASC"
	case "faction_asc":
		orderClause = "faction_name ASC, experience DESC"
	case "faction_desc":
		orderClause = "faction_name DESC, experience DESC"
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sort parameter"})
		return
	}

	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 200 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 200"})
			return
		}
		limit = v
	}

	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be >= 0"})
			return
		}
		offset = v
	}

	rows, err := a.db.Query(`
SELECT faction_name, experience
FROM player_faction_experience
WHERE player_id = $1
ORDER BY `+orderClause+`
LIMIT $2 OFFSET $3
`, playerID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load faction experience"})
		return
	}
	defer rows.Close()

	var items []factionExperience
	for rows.Next() {
		var fe factionExperience
		if err := rows.Scan(&fe.Faction, &fe.Experience); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read faction experience"})
			return
		}
		items = append(items, fe)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to iterate faction experience"})
		return
	}

	var total int
	if err := a.db.QueryRow(`SELECT COUNT(1) FROM player_faction_experience WHERE player_id = $1`, playerID).Scan(&total); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to count faction experience"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"playerId": playerID,
		"sort":     sortByOrDefault(sortBy),
		"limit":    limit,
		"offset":   offset,
		"total":    total,
		"items":    items,
	})
}

func sortByOrDefault(sortBy string) string {
	if sortBy == "" {
		return "exp_desc"
	}
	return sortBy
}

func (a *api) getPlayerByID(id int64) (player, error) {
	var p player
	var factionsRaw, tournamentsRaw, hobbyEveningsRaw, otherEventsRaw string

	err := a.db.QueryRow(`
SELECT
	id, full_name, nickname, city, contacts, preferred_location,
	rank_title, rank_attested_at, factions, tournaments, hobby_evenings,
	total_experience, other_events, collection_link, created_at, updated_at
FROM players
WHERE id = $1
`, id).Scan(
		&p.ID,
		&p.FullName,
		&p.Nickname,
		&p.City,
		&p.Contacts,
		&p.PreferredLocation,
		&p.RankTitle,
		&p.RankAttestedAt,
		&factionsRaw,
		&tournamentsRaw,
		&hobbyEveningsRaw,
		&p.TotalExperience,
		&otherEventsRaw,
		&p.CollectionLink,
		&p.CreatedAt,
		&p.UpdatedAt,
	)
	if err != nil {
		return player{}, err
	}

	_ = json.Unmarshal([]byte(factionsRaw), &p.Factions)
	_ = json.Unmarshal([]byte(tournamentsRaw), &p.Tournaments)
	_ = json.Unmarshal([]byte(hobbyEveningsRaw), &p.HobbyEvenings)
	_ = json.Unmarshal([]byte(otherEventsRaw), &p.OtherEvents)

	rows, err := a.db.Query(`
SELECT faction_name, experience
FROM player_faction_experience
WHERE player_id = $1
ORDER BY faction_name
`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var fe factionExperience
			if scanErr := rows.Scan(&fe.Faction, &fe.Experience); scanErr == nil {
				p.FactionExperience = append(p.FactionExperience, fe)
			}
		}
	}

	return p, nil
}
