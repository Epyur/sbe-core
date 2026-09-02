package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// «ЦУП Веб» (2026-09-02): вход по email-ссылке для браузерного портала.
// Magic-link — не новая модель авторизации, а альтернативная доставка того же
// "ключа" (users/devices/keys), что и request-key/activate-key, только по
// одноразовой ссылке из письма вместо кода, вводимого вручную. Устройство,
// заведённое этим путём, помечается channel='web' (см. migrate.go) — читается
// downstream (photo-service/lab-service) из JWT для урезания прав веб-сессий.

// magicLinkLimitExceeded — тот же анти-спам, что requestLimitExceeded, но по
// таблице magic_links (переиспользует те же env RATE_LIMIT_PER_10MIN/MAX_PENDING_KEYS).
func (s *Server) magicLinkLimitExceeded(ctx context.Context, email string) (bool, error) {
	per10m := 3
	if v := os.Getenv("RATE_LIMIT_PER_10MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			per10m = n
		}
	}
	maxPending := 5
	if v := os.Getenv("MAX_PENDING_KEYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxPending = n
		}
	}

	var recent int
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*) FROM magic_links m JOIN devices d ON d.device_id = m.device_id
WHERE d.user_id = $1 AND m.created_at > now() - interval '10 minutes'`, email).Scan(&recent); err != nil {
		return false, err
	}
	if recent >= per10m {
		return true, nil
	}

	var pending int
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*) FROM magic_links m JOIN devices d ON d.device_id = m.device_id
WHERE d.user_id = $1 AND m.status = 'pending'`, email).Scan(&pending); err != nil {
		return false, err
	}
	return pending >= maxPending, nil
}

func (s *Server) handleRequestLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid email: expected domain " + allowedDomain()})
		return
	}

	ctx := r.Context()

	limited, err := s.magicLinkLimitExceeded(ctx, req.Email)
	if err != nil {
		internalError(w, err)
		return
	}
	if limited {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many link requests, try later"})
		return
	}

	deviceID, err := newDeviceID()
	if err != nil {
		internalError(w, err)
		return
	}

	if _, err := s.pool.Exec(ctx, `INSERT INTO users (email) VALUES ($1) ON CONFLICT DO NOTHING`, req.Email); err != nil {
		internalError(w, err)
		return
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO devices (device_id, user_id, channel) VALUES ($1, $2, 'web')
ON CONFLICT DO NOTHING`, deviceID, req.Email); err != nil {
		internalError(w, err)
		return
	}

	token, err := newKey()
	if err != nil {
		internalError(w, err)
		return
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO magic_links (token_hash, device_id, expires_at)
VALUES ($1, $2, now() + interval '5 minutes')`, sha256Hex(token), deviceID); err != nil {
		internalError(w, err)
		return
	}

	link := fmt.Sprintf("%s/app/#/verify?token=%s", publicBaseURL(), token)
	if err := sendMagicLinkEmail(req.Email, link); err != nil {
		log.Printf("sendMagicLinkEmail: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "email send failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "link sent to email"})
}

func (s *Server) handleConsumeLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	// Анти-brute-force (тот же паттерн, что activateLim/tokenLim) — токен
	// короткоживущий, но перебор по IP всё равно стоит ограничить.
	if !s.consumeLim.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many attempts, try later"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "token is required"})
		return
	}

	ctx := r.Context()
	tokenHash := sha256Hex(token)

	var deviceID, status string
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `
SELECT device_id, status, expires_at FROM magic_links WHERE token_hash = $1`,
		tokenHash).Scan(&deviceID, &status, &expiresAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "link expired or already used"})
		return
	}
	if status != "pending" || time.Now().After(expiresAt) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "link expired or already used"})
		return
	}
	// Одноразовость: помечаем consumed сразу — повторный consume тем же
	// токеном (даже до истечения TTL) больше не пройдёт статусную проверку.
	if _, err := s.pool.Exec(ctx, `
UPDATE magic_links SET status = 'consumed', consumed_at = now() WHERE token_hash = $1`, tokenHash); err != nil {
		internalError(w, err)
		return
	}

	var email string
	if err := s.pool.QueryRow(ctx, `SELECT user_id FROM devices WHERE device_id = $1`, deviceID).Scan(&email); err != nil {
		internalError(w, err)
		return
	}

	// Владение email уже подтверждено переходом по ссылке — ключ выдаётся
	// сразу активным, без отдельного шага активации (в отличие от request-key).
	if _, err := s.pool.Exec(ctx, `DELETE FROM keys WHERE device_id = $1`, deviceID); err != nil {
		internalError(w, err)
		return
	}
	key, err := newKey()
	if err != nil {
		internalError(w, err)
		return
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO keys (key_hash, device_id, status) VALUES ($1, $2, 'active')`, sha256Hex(key), deviceID); err != nil {
		internalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"email": email, "device_id": deviceID, "key": key})
}
