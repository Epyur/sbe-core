package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// appSecretEnv — имя env-переменной service_secret для приложения ("documents" → "DOCUMENTS_SERVICE_SECRET").
func appSecretEnv(appID string) string {
	return strings.ToUpper(appID) + "_SERVICE_SECRET"
}

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// handleAppSecretStatus — статус service_secret приложения (admin).
func (s *Server) handleAppSecretStatus(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: admin only"})
		return
	}
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if appID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "app_id is required"})
		return
	}

	var set bool
	var updatedAt *time.Time
	err := s.pool.QueryRow(r.Context(),
		`SELECT service_secret <> '', updated_at FROM apps WHERE app_id = $1`, appID).Scan(&set, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "app not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		}
		return
	}

	var pending bool
	var pendingSince *time.Time
	err = s.pool.QueryRow(r.Context(),
		`SELECT status = 'pending', created_at FROM secret_rotations WHERE app_id = $1`, appID).Scan(&pending, &pendingSince)
	if errors.Is(err, pgx.ErrNoRows) {
		pending = false
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"app_id":        appID,
		"set":           set,
		"updated_at":    fmtTimePtr(updatedAt),
		"pending":       pending,
		"pending_since": fmtTimePtr(pendingSince),
	})
}

// handleAppSecretAction — sync (выровнять apps по env) или rotate (создать очередь ротации).
func (s *Server) handleAppSecretAction(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: admin only"})
		return
	}
	var req struct {
		AppID  string `json:"app_id"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.AppID = strings.TrimSpace(req.AppID)
	req.Action = strings.TrimSpace(req.Action)
	if req.AppID == "" || req.Action == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "app_id and action are required"})
		return
	}

	ctx := r.Context()
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM apps WHERE app_id = $1`, req.AppID).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "app not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		}
		return
	}

	now := time.Now().UTC()
	switch req.Action {
	case "sync":
		envSecret := os.Getenv(appSecretEnv(req.AppID))
		if envSecret == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "env secret not set on server"})
			return
		}
		if _, err := s.pool.Exec(ctx,
			`UPDATE apps SET service_secret = $2, updated_at = $3 WHERE app_id = $1`,
			req.AppID, envSecret, now); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
			return
		}
		s.auditSecret(ctx, req.AppID, "sync", u.Email)
		writeJSON(w, http.StatusOK, map[string]any{"app_id": req.AppID, "applied": true})
	case "rotate":
		newSecret, err := randomHex(32)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "rng error"})
			return
		}
		if _, err := s.pool.Exec(ctx, `
INSERT INTO secret_rotations (app_id, new_secret, status, requested_by, created_at, applied_at)
VALUES ($1, $2, 'pending', $3, $4, NULL)
ON CONFLICT (app_id) DO UPDATE SET
	new_secret = EXCLUDED.new_secret,
	status = 'pending',
	requested_by = EXCLUDED.requested_by,
	created_at = EXCLUDED.created_at,
	applied_at = NULL`,
			req.AppID, newSecret, u.Email, now); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
			return
		}
		s.auditSecret(ctx, req.AppID, "rotate", u.Email)
		writeJSON(w, http.StatusOK, map[string]any{
			"app_id":     req.AppID,
			"new_secret": newSecret,
			"pending":    true,
		})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid action: sync or rotate"})
	}
}

func (s *Server) auditSecret(ctx context.Context, appID, action, email string) {
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO secret_audit (app_id, action, requested_by) VALUES ($1, $2, $3)`,
		appID, action, email)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
