package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// registryEntry — запись реестра (RegistryPluginEntry на клиенте).
type registryEntry struct {
	ID          string   `json:"id"`
	Dir         string   `json:"dir"`
	Name        string   `json:"name"`
	Repo        string   `json:"repo"`
	Branch      string   `json:"branch,omitempty"`
	Required    bool     `json:"required,omitempty"`
	HasView     bool     `json:"hasView,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	OwnerEmail  string   `json:"ownerEmail,omitempty"`
	Description string   `json:"description,omitempty"`
}

type registryAddition struct {
	RegistryID int64          `json:"registryId"`
	Plugin     registryEntry  `json:"plugin"`
	CreatedAt  time.Time      `json:"-"`
}

const registryBasePath = "/srv/www/registry.json"

// handleRegistryList — список добавленных администратором записей реестра (admin).
func (s *Server) handleRegistryList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: admin only"})
		return
	}
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, entry, created_at FROM registry_additions ORDER BY created_at`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		return
	}
	defer rows.Close()

	out := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var raw []byte
		var createdAt time.Time
		if err := rows.Scan(&id, &raw, &createdAt); err != nil {
			continue
		}
		var entry json.RawMessage
		if len(raw) == 0 {
			entry = json.RawMessage("{}")
		} else {
			entry = json.RawMessage(raw)
		}
		out = append(out, map[string]any{
			"registryId": id,
			"createdAt":  createdAt.UTC().Format(time.RFC3339),
			"plugin":     entry,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": out})
}

// handleRegistryAdd — добавляет новый плагин в реестр (admin).
func (s *Server) handleRegistryAdd(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: admin only"})
		return
	}
	var req struct {
		Plugin registryEntry `json:"plugin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	p := req.Plugin
	p.ID = strings.TrimSpace(p.ID)
	p.Dir = strings.TrimSpace(p.Dir)
	p.Repo = strings.TrimSpace(p.Repo)
	if p.ID == "" || p.Repo == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id and repo are required"})
		return
	}
	// Защита от path traversal через dir/id из реестра (ревью B4):
	// только безопасные имена каталогов.
	var idDirRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	if !idDirRe.MatchString(strings.ToLower(p.Dir)) || strings.Contains(p.Dir, "..") ||
		strings.ContainsAny(p.Repo, "\\\x00") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id or dir"})
		return
	}
	if p.Dir == "" {
		p.Dir = p.ID
	}
	raw, err := json.Marshal(p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "encode error"})
		return
	}
	var id int64
	err = s.pool.QueryRow(r.Context(), `
INSERT INTO registry_additions (entry) VALUES ($1) RETURNING id`, raw).Scan(&id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// handleRegistryDelete — удаляет добавленную запись реестра (admin).
func (s *Server) handleRegistryDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: admin only"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM registry_additions WHERE id = $1`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleRegistryJSON — публичный GET /registry.json: базовый файл (/srv/www) +
// записи, добавленные администратором (registry_additions).
func (s *Server) handleRegistryJSON(w http.ResponseWriter, r *http.Request) {
	base := s.registryBase()
	additions := s.registryAdditions(r.Context())

	merged := map[string]any{
		"schemaVersion": 1,
		"updatedAt":     time.Now().UTC().Format(time.RFC3339),
		"plugins":       additions,
	}
	if base != nil {
		for k, v := range base {
			if k != "plugins" {
				merged[k] = v
			}
		}
		if basePlugins, ok := base["plugins"].([]any); ok && len(basePlugins) > 0 {
			merged["plugins"] = append(basePlugins, additions...)
		} else if _, ok := merged["plugins"]; !ok {
			merged["plugins"] = []any{}
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	_ = enc.Encode(merged)
}

// registryBase читает базовый registry.json из примонтированного каталога.
func (s *Server) registryBase() map[string]any {
	data, err := os.ReadFile(registryBasePath)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// registryAdditions — все admin-добавления как []any для склейки с базой.
func (s *Server) registryAdditions(ctx context.Context) []any {
	rows, err := s.pool.Query(ctx, `SELECT entry FROM registry_additions ORDER BY created_at`)
	if err != nil {
		return []any{}
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil || len(raw) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out
}

var _ = errors.Is // сохраняем импорт errors при рефакторинге
var _ = pgx.ErrNoRows
