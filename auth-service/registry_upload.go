package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ---- Инструмент ручной загрузки файлов плагина (2026-08-29) ----
// См. docs/superpowers/specs/2026-08-29-sbe-plugin-file-upload-design.md. Владелец
// плагина (или admin) заливает main.js/manifest.json/styles.css через ЦУП без
// доступа к серверу по SSH — сервер сам считает SHA-256 от реально принятых байт
// (клиенту не доверяем) и пишет в registry_file_overrides (см. registry_admin.go
// handleRegistryJSON — единственное место, что читает эту таблицу).

const pluginsBasePath = "/srv/www/plugins"

// uploadFieldFiles — какие form-file поля принимает POST /auth/registry/upload и в
// какое имя файла на диске каждое сохраняется. main/manifest обязательны в запросе
// (проверяется явно ниже), styles — опционален: не у каждого плагина есть стили.
var uploadFieldFiles = map[string]string{
	"main":     "main.js",
	"manifest": "manifest.json",
	"styles":   "styles.css",
}

// handleRegistryUpload — POST /auth/registry/upload (за requireKey). Multipart form:
// dir (какая запись реестра) + файлы main/manifest (обязательны)/styles
// (опционален). Права: JWT-email == ownerEmail найденной записи ИЛИ admin — иначе
// 403. Запись с таким dir не найдена вовсе (ни в базе, ни в registry_additions) —
// 404: регистрация НОВОГО плагина этим эндпоинтом не делается (см. границы спеки).
func (s *Server) handleRegistryUpload(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	// 32 МБ с запасом — main.js собранного Obsidian-плагина обычно десятки-сотни КБ,
	// самый крупный виденный в этом реестре (mermaid) — уже отдельный сервис, не
	// через этот путь.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid multipart form"})
		return
	}
	dir := strings.TrimSpace(r.FormValue("dir"))
	if dir == "" || !safeRegistryDirRe.MatchString(strings.ToLower(dir)) || strings.Contains(dir, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid dir"})
		return
	}

	entry, found := s.findRegistryEntry(r.Context(), dir)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "registry entry not found: register the plugin first"})
		return
	}
	if u.Email != entry.OwnerEmail && !s.isAdmin(u.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden: not the plugin owner"})
		return
	}

	if r.MultipartForm.File["main"] == nil || r.MultipartForm.File["manifest"] == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "main and manifest files are required"})
		return
	}

	destDir := filepath.Join(pluginsBasePath, dir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "storage error"})
		return
	}

	newHashes := map[string]string{}
	for field, fileName := range uploadFieldFiles {
		fileHeaders := r.MultipartForm.File[field]
		if len(fileHeaders) == 0 {
			continue
		}
		f, err := fileHeaders[0].Open()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cannot read " + field})
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cannot read " + field})
			return
		}
		if err := os.WriteFile(filepath.Join(destDir, fileName), data, 0o644); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "cannot write " + field})
			return
		}
		sum := sha256.Sum256(data)
		newHashes[field] = hex.EncodeToString(sum[:])
	}

	// Частичная загрузка (напр. без styles) не должна затирать хэш файла, который
	// уже был раньше от предыдущей загрузки — смёржить с уже сохранённым оверлеем.
	if existing := s.registryFileOverrides(r.Context())[dir]; existing.Hashes != nil {
		for field, hash := range existing.Hashes {
			if _, uploadedNow := newHashes[field]; !uploadedNow {
				newHashes[field] = hash
			}
		}
	}

	hashesJSON, err := json.Marshal(newHashes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "encode error"})
		return
	}
	if _, err := s.pool.Exec(r.Context(), `
INSERT INTO registry_file_overrides (dir, hashes, uploaded_by, uploaded_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (dir) DO UPDATE SET hashes = EXCLUDED.hashes, uploaded_by = EXCLUDED.uploaded_by, uploaded_at = now()`,
		dir, string(hashesJSON), u.Email); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "hashes": newHashes})
}
