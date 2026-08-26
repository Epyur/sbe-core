package main

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// handleServiceToken — внутренний эндпоинт служебных вызовов между сервисами
// (сейчас: lab-service → ekn-service; замена mintServiceJWT). Доступен только
// в docker-сети — Caddy не проксирует /internal/* (Блок D, ревью 1.2).
//
// Вызывающий аутентифицируется своим {APP}_SERVICE_SECRET (заголовок
// X-Service-Secret), который сверяется constant-time с apps.service_secret.
// Токен выпускается для ЦЕЛЕВОГО приложения и подписывается КЛЮЧОМ ЦЕЛИ —
// ни один сервис не может минтить токены за чужой (в отличие от прежнего
// mintServiceJWT на общем JWT_SECRET). Роль на стороне цели резолвится по
// email владельца приложения-цели (как раньше через mintServiceJWT).
func (s *Server) handleServiceToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetAppID string `json:"target_app_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.TargetAppID = strings.TrimSpace(req.TargetAppID)
	if req.TargetAppID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "target_app_id is required"})
		return
	}
	provided := strings.TrimSpace(r.Header.Get("X-Service-Secret"))
	if provided == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	callerApp, ok, err := s.findAppBySecret(r.Context(), provided)
	if err != nil {
		internalError(w, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}

	var ownerEmail, targetSecret string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT owner_email, service_secret FROM apps WHERE app_id = $1`, req.TargetAppID).
		Scan(&ownerEmail, &targetSecret); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown target_app_id"})
		return
	}
	if ownerEmail == "" || targetSecret == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "target app is not configured"})
		return
	}

	jwtStr, exp, err := signJWT(ownerEmail, "service:"+callerApp, req.TargetAppID, targetSecret, 5*time.Minute)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jwt": jwtStr, "expires_at": exp.UTC().Format(time.RFC3339)})
}

// findAppBySecret находит приложение, чей service_secret совпадает с provided
// (constant-time сравнение). Возвращает (appID, найдено, err).
func (s *Server) findAppBySecret(ctx context.Context, provided string) (string, bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT app_id, service_secret FROM apps WHERE service_secret <> ''`)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var appID, secret string
		if err := rows.Scan(&appID, &secret); err != nil {
			continue
		}
		if constTimeEqual(provided, secret) {
			return appID, true, nil
		}
	}
	return "", false, rows.Err()
}
