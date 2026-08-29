package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Server struct {
	pool        *pgxpool.Pool
	adminEmails map[string]bool
	// Анти-brute-force на активацию ключа и выдачу токена (ревью 2.1):
	// 256-битный ключ делает перебор непрактичным, но без блокировки
	// эндпоинты открыты для бесконечных попыток.
	activateLim *ipLimiter
	tokenLim    *ipLimiter
}

func parseAdminEmails() map[string]bool {
	m := make(map[string]bool)
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			m[e] = true
		}
	}
	return m
}

func (s *Server) isAdmin(email string) bool {
	return s.adminEmails[strings.ToLower(strings.TrimSpace(email))]
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping: %v", err)
	}

	s := &Server{
		pool:        pool,
		adminEmails: parseAdminEmails(),
		activateLim: newIPLimiter(time.Minute, 20),
		tokenLim:    newIPLimiter(time.Minute, 60),
	}
	if err := s.migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := s.seedApps(ctx); err != nil {
		log.Fatalf("seedApps: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /auth/request-key", s.handleRequestKey)
	mux.HandleFunc("POST /auth/activate-key", s.handleActivateKey)
	mux.HandleFunc("POST /auth/token", s.handleToken)
	mux.HandleFunc("GET /auth/devices", s.requireKey(s.handleListDevices))
	mux.HandleFunc("DELETE /auth/devices/{device_id}", s.requireKey(s.handleRevokeDevice))
	mux.HandleFunc("GET /auth/presence", s.requireKey(s.handlePresence))
	mux.HandleFunc("POST /auth/news", s.requireKey(s.handleCreateNews))
	mux.HandleFunc("GET /auth/news", s.requireKey(s.handleListNews))
	mux.HandleFunc("POST /auth/news/{id}/ack", s.requireKey(s.handleAckNews))
	mux.HandleFunc("GET /auth/news/{id}/reads", s.requireKey(s.handleNewsReads))
	mux.HandleFunc("POST /auth/feedback", s.requireKey(s.handleFeedback))
	mux.HandleFunc("GET /auth/apps/secret", s.requireKey(s.handleAppSecretStatus))
	mux.HandleFunc("POST /auth/apps/secret", s.requireKey(s.handleAppSecretAction))
	mux.HandleFunc("GET /auth/apps/env", s.requireKey(s.handleAppEnvStatus))
	mux.HandleFunc("POST /auth/apps/env", s.requireKey(s.handleAppEnvSet))
	mux.HandleFunc("GET /auth/registry", s.requireKey(s.handleRegistryList))
	mux.HandleFunc("POST /auth/registry", s.requireKey(s.handleRegistryAdd))
	mux.HandleFunc("DELETE /auth/registry/{id}", s.requireKey(s.handleRegistryDelete))
	mux.HandleFunc("POST /auth/registry/upload", s.requireKey(s.handleRegistryUpload))
	mux.HandleFunc("GET /registry.json", s.handleRegistryJSON)
	mux.HandleFunc("POST /apps/register", s.handleRegisterApp)
	// Внутренний эндпоинт служебных токенов (lab→ekn): Caddy НЕ проксирует
	// /internal/* — доступен только в docker-сети (Блок D, замена mintServiceJWT).
	mux.HandleFunc("POST /internal/service-token", s.handleServiceToken)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("auth-service listening on :%s", port)
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "error", "db": "unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "db": "ok"})
}

func (s *Server) handleRequestKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		DeviceID string `json:"device_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DeviceID = strings.TrimSpace(req.DeviceID)

	if !validEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid email: expected domain " + allowedDomain()})
		return
	}
	if !uuidRe.MatchString(req.DeviceID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid device_id"})
		return
	}

	ctx := r.Context()

	limited, err := s.requestLimitExceeded(ctx, req.Email)
	if err != nil {
		internalError(w, err)
		return
	}
	if limited {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many key requests, try later"})
		return
	}

	if _, err := s.pool.Exec(ctx, `INSERT INTO users (email) VALUES ($1) ON CONFLICT DO NOTHING`, req.Email); err != nil {
		internalError(w, err)
		return
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO devices (device_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, req.DeviceID, req.Email); err != nil {
		internalError(w, err)
		return
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM keys WHERE device_id = $1`, req.DeviceID); err != nil {
		internalError(w, err)
		return
	}

	key, err := newKey()
	if err != nil {
		internalError(w, err)
		return
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO keys (key_hash, device_id, status) VALUES ($1, $2, 'pending')`, sha256Hex(key), req.DeviceID); err != nil {
		internalError(w, err)
		return
	}
	if err := sendKeyEmail(req.Email, key); err != nil {
		log.Printf("sendKeyEmail: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "email send failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "key sent to email"})
}

func (s *Server) handleActivateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		DeviceID string `json:"device_id"`
		Key      string `json:"key"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	// Анти-brute-force (ревью 2.1): лимит попыток активации с одного IP.
	if !s.activateLim.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many attempts, try later"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	key := strings.TrimSpace(req.Key)
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "key is required"})
		return
	}

	var deviceID, userID, status string
	err := s.pool.QueryRow(r.Context(), `
SELECT k.device_id, d.user_id, k.status
FROM keys k JOIN devices d ON d.device_id = k.device_id
WHERE k.key_hash = $1`, sha256Hex(key)).Scan(&deviceID, &userID, &status)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "key not found"})
		return
	}
	if userID != req.Email || deviceID != req.DeviceID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "key does not match email/device"})
		return
	}
	if status == "active" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "already active"})
		return
	}
	if _, err := s.pool.Exec(r.Context(), `UPDATE keys SET status = 'active' WHERE key_hash = $1`, sha256Hex(key)); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		AppID string `json:"app_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	// Анти-brute-force (ревью 2.1): лимит запросов токенов с одного IP.
	if !s.tokenLim.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many requests, try later"})
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	req.AppID = strings.TrimSpace(req.AppID)
	if req.Key == "" || req.AppID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "key and app_id are required"})
		return
	}

	var deviceID, userID, status string
	err := s.pool.QueryRow(r.Context(), `
SELECT k.device_id, d.user_id, k.status
FROM keys k JOIN devices d ON d.device_id = k.device_id
WHERE k.key_hash = $1`, sha256Hex(req.Key)).Scan(&deviceID, &userID, &status)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid key"})
		return
	}
	if status != "active" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "key not activated"})
		return
	}
	// Per-service ключ (Блок D, ревью 1.2): токен для app подписывается его
	// service_secret, а не общим JWT_SECRET — утечка одного ключа не даёт
	// подделать токены для остальных сервисов.
	var appSecret string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT service_secret FROM apps WHERE app_id = $1`, req.AppID).Scan(&appSecret); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown app_id"})
		} else {
			internalError(w, err)
		}
		return
	}

	// Точка учёта присутствия (ЦУП, 2026-08-22): каждый плагин получает токен
	// здесь перед вызовом своего backend — не блокирует выдачу токена при ошибке.
	s.touchLastSeen(r.Context(), deviceID)

	jwtStr, exp, err := signJWT(userID, deviceID, req.AppID, appSecret, tokenTTL())
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jwt": jwtStr, "expires_at": exp.UTC().Format(time.RFC3339)})
}

type authKeyCtx struct{}

type authUser struct {
	Email    string
	DeviceID string
}

func (s *Server) requireKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		// Строго "Bearer " с пробелом (раньше TrimPrefix("Bearer") принимал
		// и "BearerX" без пробела — ревью 2.1).
		var key string
		if strings.HasPrefix(auth, "Bearer ") {
			key = strings.TrimSpace(auth[len("Bearer "):])
		}
		if key == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var deviceID, userID, status string
		err := s.pool.QueryRow(r.Context(), `
SELECT k.device_id, d.user_id, k.status
FROM keys k JOIN devices d ON d.device_id = k.device_id
WHERE k.key_hash = $1`, sha256Hex(key)).Scan(&deviceID, &userID, &status)
		if err != nil || status != "active" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		// Без этого /auth/presence и /auth/devices (авторизуются мастер-ключом
		// устройства напрямую, минуя /auth/token) не считались "активностью" —
		// пользователь мог открыть «Онлайн» и не увидеть в списке самого себя,
		// если давно не запрашивал новый JWT ни у одного плагина (обнаружено
		// пользователем, 2026-08-22, сразу после первого деплоя presence).
		s.touchLastSeen(r.Context(), deviceID)
		ctx := context.WithValue(r.Context(), authKeyCtx{}, authUser{Email: userID, DeviceID: deviceID})
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) touchLastSeen(ctx context.Context, deviceID string) {
	if _, err := s.pool.Exec(ctx, `UPDATE devices SET last_seen_at = now() WHERE device_id = $1`, deviceID); err != nil {
		log.Printf("update last_seen_at: %v", err)
	}
}

func userFrom(r *http.Request) authUser {
	u, _ := r.Context().Value(authKeyCtx{}).(authUser)
	return u
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	rows, err := s.pool.Query(r.Context(), `
SELECT d.device_id, d.label, d.created_at, COALESCE(k.status, '')
FROM devices d
LEFT JOIN keys k ON k.device_id = d.device_id
WHERE d.user_id = $1
ORDER BY d.created_at`, u.Email)
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()

	type device struct {
		DeviceID  string `json:"device_id"`
		Label     string `json:"label"`
		CreatedAt string `json:"created_at"`
		KeyStatus string `json:"key_status"`
	}
	devices := make([]device, 0)
	for rows.Next() {
		var d device
		var created time.Time
		if err := rows.Scan(&d.DeviceID, &d.Label, &created, &d.KeyStatus); err != nil {
			log.Printf("list devices scan: %v", err)
			continue
		}
		d.CreatedAt = created.UTC().Format(time.RFC3339)
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	deviceID := r.PathValue("device_id")
	// Без этой проверки невалидный device_id (напр. клиентский баг, слал буквально
	// "undefined") долетал до Postgres как параметр колонки UUID и падал с
	// "invalid input syntax for type uuid" — internalError() превращал это в
	// невразумительный 500 вместо понятного 400 (обнаружено пользователем,
	// 2026-08-22, на клиенте отдельно исправлено несовпадение camelCase/snake_case
	// в sbe-apstore/src/services/auth-service.ts listDevices()).
	if !uuidRe.MatchString(deviceID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid device_id"})
		return
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM devices WHERE device_id = $1 AND user_id = $2`, deviceID, u.Email)
	if err != nil {
		internalError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "device not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) queryEmails(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	emails := make([]string, 0)
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}
	return emails, rows.Err()
}

func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ctx := r.Context()

	online, err := s.queryEmails(ctx, `
SELECT DISTINCT d.user_id FROM devices d
WHERE d.last_seen_at >= now() - interval '30 minutes'`)
	if err != nil {
		internalError(w, err)
		return
	}

	resp := map[string]any{
		"online":   online,
		"is_admin": s.isAdmin(u.Email),
	}

	if s.isAdmin(u.Email) {
		rows, err := s.pool.Query(ctx, `
SELECT us.email, MAX(d.last_seen_at)
FROM users us
LEFT JOIN devices d ON d.user_id = us.email
GROUP BY us.email
ORDER BY MAX(d.last_seen_at) DESC NULLS LAST`)
		if err != nil {
			internalError(w, err)
			return
		}
		type userLastSeen struct {
			Email      string  `json:"email"`
			LastSeenAt *string `json:"last_seen_at"`
		}
		allUsers := make([]userLastSeen, 0)
		for rows.Next() {
			var email string
			var lastSeen *time.Time
			if err := rows.Scan(&email, &lastSeen); err != nil {
				log.Printf("presence all_users scan: %v", err)
				continue
			}
			var formatted *string
			if lastSeen != nil {
				str := lastSeen.UTC().Format(time.RFC3339)
				formatted = &str
			}
			allUsers = append(allUsers, userLastSeen{Email: email, LastSeenAt: formatted})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			internalError(w, err)
			return
		}
		resp["all_users"] = allUsers
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateNews(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req struct {
		Title      string   `json:"title"`
		Body       string   `json:"body"`
		Visibility string   `json:"visibility"`
		Recipients []string `json:"recipients"`
		Mandatory  bool     `json:"mandatory"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Visibility = strings.TrimSpace(req.Visibility)
	if req.Title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "title is required"})
		return
	}
	if req.Visibility != "all" && req.Visibility != "restricted" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "visibility must be 'all' or 'restricted'"})
		return
	}
	recipients := make([]string, 0, len(req.Recipients))
	for _, e := range req.Recipients {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			recipients = append(recipients, e)
		}
	}
	if req.Visibility == "restricted" && len(recipients) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "recipients is required for restricted visibility"})
		return
	}
	// Общедоступные необязательные сообщения (announceUpdate любого SBE-плагина)
	// разрешены любому авторизованному пользователю; ограниченный список
	// получателей или "обязательно к прочтению" — только администратору
	// (ADMIN_EMAILS), т.к. это принудительно всплывает модалкой у получателей.
	if (req.Visibility == "restricted" || req.Mandatory) && !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: restricted or mandatory news requires admin"})
		return
	}

	ctx := r.Context()
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO news_messages (author_email, title, body, visibility, mandatory)
VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		u.Email, req.Title, req.Body, req.Visibility, req.Mandatory).Scan(&id)
	if err != nil {
		internalError(w, err)
		return
	}
	if req.Visibility == "restricted" {
		for _, email := range recipients {
			if _, err := s.pool.Exec(ctx, `
INSERT INTO news_recipients (message_id, email) VALUES ($1, $2)
ON CONFLICT DO NOTHING`, id, email); err != nil {
				internalError(w, err)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleListNews(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	rows, err := s.pool.Query(r.Context(), `
SELECT m.id, m.author_email, m.title, m.body, m.visibility, m.mandatory, m.created_at,
       EXISTS(SELECT 1 FROM news_reads nr WHERE nr.message_id = m.id AND nr.email = $1) AS read
FROM news_messages m
WHERE m.visibility = 'all'
   OR EXISTS(SELECT 1 FROM news_recipients nrc WHERE nrc.message_id = m.id AND nrc.email = $1)
ORDER BY m.created_at DESC`, u.Email)
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()

	type newsItem struct {
		ID          int64  `json:"id"`
		AuthorEmail string `json:"author_email"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		Visibility  string `json:"visibility"`
		Mandatory   bool   `json:"mandatory"`
		CreatedAt   string `json:"created_at"`
		Read        bool   `json:"read"`
	}
	items := make([]newsItem, 0)
	for rows.Next() {
		var it newsItem
		var created time.Time
		if err := rows.Scan(&it.ID, &it.AuthorEmail, &it.Title, &it.Body, &it.Visibility, &it.Mandatory, &created, &it.Read); err != nil {
			log.Printf("list news scan: %v", err)
			continue
		}
		it.CreatedAt = created.UTC().Format(time.RFC3339)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"news": items})
}

func (s *Server) handleAckNews(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if _, err := s.pool.Exec(r.Context(), `
INSERT INTO news_reads (message_id, email) VALUES ($1, $2)
ON CONFLICT (message_id, email) DO NOTHING`, id, u.Email); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleNewsReads(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	ctx := r.Context()

	var visibility string
	if err := s.pool.QueryRow(ctx, `SELECT visibility FROM news_messages WHERE id = $1`, id).Scan(&visibility); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "news not found"})
		return
	}

	var recipients []string
	if visibility == "restricted" {
		recipients, err = s.queryEmails(ctx, `SELECT email FROM news_recipients WHERE message_id = $1`, id)
	} else {
		recipients, err = s.queryEmails(ctx, `SELECT email FROM users`)
	}
	if err != nil {
		internalError(w, err)
		return
	}

	rows, err := s.pool.Query(ctx, `SELECT email, read_at FROM news_reads WHERE message_id = $1`, id)
	if err != nil {
		internalError(w, err)
		return
	}
	readAt := make(map[string]string)
	for rows.Next() {
		var email string
		var t time.Time
		if err := rows.Scan(&email, &t); err != nil {
			log.Printf("news reads scan: %v", err)
			continue
		}
		readAt[email] = t.UTC().Format(time.RFC3339)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internalError(w, err)
		return
	}

	type readStatus struct {
		Email  string  `json:"email"`
		Read   bool    `json:"read"`
		ReadAt *string `json:"read_at,omitempty"`
	}
	result := make([]readStatus, 0, len(recipients))
	for _, email := range recipients {
		if ts, ok := readAt[email]; ok {
			ts := ts
			result = append(result, readStatus{Email: email, Read: true, ReadAt: &ts})
		} else {
			result = append(result, readStatus{Email: email, Read: false})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"reads": result})
}

func (s *Server) handleRegisterApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID         string `json:"app_id"`
		Name          string `json:"name"`
		OwnerEmail    string `json:"owner_email"`
		ServiceSecret string `json:"service_secret"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.AppID = strings.TrimSpace(req.AppID)
	if req.AppID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "app_id is required"})
		return
	}
	if !s.authorizedRegister(r.Context(), req.AppID, req.ServiceSecret) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	if _, err := s.pool.Exec(r.Context(), `
INSERT INTO apps (app_id, name, owner_email, service_secret)
VALUES ($1, $2, $3, $4)
ON CONFLICT (app_id) DO UPDATE SET
	name = EXCLUDED.name,
	owner_email = CASE WHEN EXCLUDED.owner_email <> '' THEN EXCLUDED.owner_email ELSE apps.owner_email END,
	service_secret = CASE WHEN EXCLUDED.service_secret <> '' THEN EXCLUDED.service_secret ELSE apps.service_secret END`,
		req.AppID, req.Name, req.OwnerEmail, req.ServiceSecret); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func allowedDomain() string {
	if d := os.Getenv("ALLOWED_EMAIL_DOMAIN"); d != "" {
		return d
	}
	return "tn.ru"
}

func (s *Server) requestLimitExceeded(ctx context.Context, email string) (bool, error) {
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
SELECT COUNT(*) FROM keys k JOIN devices d ON d.device_id = k.device_id
WHERE d.user_id = $1 AND k.created_at > now() - interval '10 minutes'`, email).Scan(&recent); err != nil {
		return false, err
	}
	if recent >= per10m {
		return true, nil
	}

	var pending int
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*) FROM keys k JOIN devices d ON d.device_id = k.device_id
WHERE d.user_id = $1 AND k.status = 'pending'`, email).Scan(&pending); err != nil {
		return false, err
	}
	return pending >= maxPending, nil
}

func (s *Server) authorizedRegister(ctx context.Context, appID, provided string) bool {
	if provided == "" {
		return false
	}
	if master := os.Getenv("APPS_REGISTER_SECRET"); master != "" && constTimeEqual(provided, master) {
		return true
	}
	var existing string
	err := s.pool.QueryRow(ctx, `SELECT service_secret FROM apps WHERE app_id = $1`, appID).Scan(&existing)
	if err == nil && constTimeEqual(provided, existing) {
		return true
	}
	return false
}

func validEmail(email string) bool {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	return strings.ToLower(email[at+1:]) == allowedDomain()
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

func internalError(w http.ResponseWriter, err error) {
	log.Printf("internal: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
}
