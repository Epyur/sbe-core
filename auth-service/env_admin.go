package main

import (
	"net/http"
	"strings"
	"time"
)

// allowedAppEnvKeys — белый список env-переменных, которые можно менять через
// ЦУП (POST /auth/apps/env), по приложениям. Критично: без этого списка
// admin-эндпоинт стал бы способом переписать ЛЮБУЮ переменную сервера
// (JWT_SECRET, DATABASE_URL чужого сервиса и т.п.) — см. ревью безопасности
// 2026-08-25 (plugins/secrev.md, план 2026-08-25-sbe-secrets-cup-plan.md, A1:
// LAB_MAIL_PASSWORD — учётка почты email-приёма результатов lab-service).
var allowedAppEnvKeys = map[string]map[string]bool{
	"lab": {
		"LAB_MAIL_ENABLED":               true,
		"LAB_MAIL_IMAP_SERVER":           true,
		"LAB_MAIL_LOGIN":                 true,
		"LAB_MAIL_PASSWORD":              true,
		"LAB_MAIL_POLL_INTERVAL_SECONDS": true,
		"LAB_MAIL_METHOD_MAP":            true,
		// LAB_MAIL_DEFAULT_PROJECT_CODE (2026-08-29) — проект для заявок из
		// письма без указанного ЕКН (см. lab-service/email_ingest.go
		// applyApplicationEmail); не секрет, но тот же admin-only канал, что и
		// остальные LAB_MAIL_* — согласованный UI/аудит в одном месте.
		"LAB_MAIL_DEFAULT_PROJECT_CODE": true,
	},
}

func isValidEnvValue(v string) bool {
	return len(v) <= 4096 && !strings.ContainsAny(v, "\x00\r\n")
}

// handleAppEnvStatus — статус admin-управляемых env-переменных приложения: по
// каждому разрешённому ключу — применялось ли когда-нибудь значение (`set`) и
// есть ли pending-заявка. Значения НИКОГДА не возвращаются (тот же принцип
// маскировки, что у service_secret, — GET отдаёт только факт/дату).
func (s *Server) handleAppEnvStatus(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: admin only"})
		return
	}
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	allowed, ok := allowedAppEnvKeys[appID]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown app_id"})
		return
	}

	rows, err := s.pool.Query(r.Context(),
		`SELECT env_key, status, applied_at, created_at FROM app_env_pending WHERE app_id = $1`, appID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		return
	}
	defer rows.Close()

	type rowState struct {
		status    string
		appliedAt *time.Time
		createdAt *time.Time
	}
	state := map[string]rowState{}
	for rows.Next() {
		var rs rowState
		var key string
		if err := rows.Scan(&key, &rs.status, &rs.appliedAt, &rs.createdAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
			return
		}
		state[key] = rs
	}

	keys := make([]map[string]any, 0, len(allowed))
	for key := range allowed {
		rs, has := state[key]
		pending := has && rs.status == "pending"
		var pendingSince *time.Time
		if pending {
			pendingSince = rs.createdAt
		}
		keys = append(keys, map[string]any{
			"key":           key,
			"set":           has && rs.status == "applied",
			"updated_at":    fmtTimePtr(rs.appliedAt),
			"pending":       pending,
			"pending_since": fmtTimePtr(pendingSince),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"app_id": appID, "keys": keys})
}

// handleAppEnvSet — ставит в очередь новые значения env-переменных приложения
// (admin). Каждый ключ сверяется с allowedAppEnvKeys — при первом же
// неразрешённом ключе отклоняется ВЕСЬ запрос (не применяем частично, чтобы
// не оставлять клиента в непонятном промежуточном состоянии). Значения в БД
// живут только до применения хост-скриптом (secret-applier.sh) — value
// обнуляется сразу после apply, в отличие от secret_rotations.new_secret,
// который остаётся в таблице навсегда (там секрет ещё нужен для sync).
func (s *Server) handleAppEnvSet(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: admin only"})
		return
	}
	var req struct {
		AppID  string            `json:"app_id"`
		Values map[string]string `json:"values"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.AppID = strings.TrimSpace(req.AppID)
	allowed, ok := allowedAppEnvKeys[req.AppID]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown app_id"})
		return
	}
	if len(req.Values) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "values is required"})
		return
	}
	for key, value := range req.Values {
		if !allowed[key] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "key not allowed: " + key})
			return
		}
		if !isValidEnvValue(value) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid value for key: " + key})
			return
		}
	}

	ctx := r.Context()
	now := time.Now().UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		return
	}
	defer tx.Rollback(ctx)
	appliedKeys := make([]string, 0, len(req.Values))
	for key, value := range req.Values {
		if _, err := tx.Exec(ctx, `
INSERT INTO app_env_pending (app_id, env_key, value, status, requested_by, created_at, applied_at)
VALUES ($1, $2, $3, 'pending', $4, $5, NULL)
ON CONFLICT (app_id, env_key) DO UPDATE SET
	value = EXCLUDED.value,
	status = 'pending',
	requested_by = EXCLUDED.requested_by,
	created_at = EXCLUDED.created_at,
	applied_at = NULL`,
			req.AppID, key, value, u.Email, now); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
			return
		}
		appliedKeys = append(appliedKeys, key)
	}
	if err := tx.Commit(ctx); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		return
	}
	s.auditSecret(ctx, req.AppID, "env-set:"+strings.Join(appliedKeys, ","), u.Email)
	writeJSON(w, http.StatusOK, map[string]any{"app_id": req.AppID, "pending": true})
}
