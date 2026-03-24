package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/golang-jwt/jwt/v5"
)

type loginRequest struct {
	Nickname string `json:"nickname"`
	Password string `json:"password"`
}

type loginClaims struct {
	PlayerID int64  `json:"playerId"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	req.Nickname = strings.TrimSpace(req.Nickname)
	req.Password = strings.TrimSpace(req.Password)
	if req.Nickname == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nickname and password are required"})
		return
	}

	var passwordHash string
	var role string
	var playerID int64
	err := a.db.QueryRow(`
SELECT pc.password_hash, pc.role, p.id
FROM player_credentials pc
JOIN players p ON p.id = pc.player_id
WHERE p.nickname = $1
`, req.Nickname).Scan(&passwordHash, &role, &playerID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	exp := time.Now().UTC().Add(24 * time.Hour)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, loginClaims{
		PlayerID: playerID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		},
	})

	tokenString, err := token.SignedString(a.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to sign token"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":    tokenString,
		"playerId": playerID,
		"role":     role,
	})
}

func (a *api) requireAdmin(r *http.Request) (int64, error) {
	claims, err := a.parseAuthClaims(r)
	if err != nil {
		return 0, err
	}
	if strings.ToLower(claims.Role) != "admin" {
		return 0, errors.New("forbidden")
	}
	return claims.PlayerID, nil
}

func (a *api) requirePlayer(r *http.Request) (int64, error) {
	claims, err := a.parseAuthClaims(r)
	if err != nil {
		return 0, err
	}
	return claims.PlayerID, nil
}

func (a *api) parseAuthClaims(r *http.Request) (loginClaims, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return loginClaims{}, errors.New("missing Authorization header")
	}

	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return loginClaims{}, errors.New("invalid Authorization header format")
	}
	tokenString := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	if tokenString == "" {
		return loginClaims{}, errors.New("missing bearer token")
	}

	var claims loginClaims
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return a.jwtSecret, nil
	})
	if err != nil {
		return loginClaims{}, errors.New("invalid token")
	}
	if token == nil || !token.Valid {
		return loginClaims{}, errors.New("invalid token")
	}

	return claims, nil
}

