package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

// maxFeedbackLength — верхняя граница длины текста обращения (защита от спама/мусора).
const maxFeedbackLength = 4000

// handleFeedback — POST /auth/feedback: обратная связь от авторизованного пользователя.
// Замечание уходит на email владельца выбранного плагина (ownerEmail из реестра),
// «идея» (пустой plugin_id) — собственнику ЦУП (первый ADMIN_EMAILS). Ведётся журнал
// feedback_messages (аудит + запись даже при сбое доставки).
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req struct {
		PluginID string `json:"plugin_id"`
		Text     string `json:"text"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	req.PluginID = strings.TrimSpace(req.PluginID)
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "text is required"})
		return
	}
	if len(req.Text) > maxFeedbackLength {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "text is too long"})
		return
	}

	recipient, pluginName, err := s.feedbackRecipient(r.Context(), req.PluginID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	subject := "SBE: идея от " + u.Email
	pluginLabel := "— (идея)"
	if req.PluginID != "" {
		subject = fmt.Sprintf("SBE: замечание по «%s» от %s", pluginName, u.Email)
		pluginLabel = pluginName
	}
	body := fmt.Sprintf(
		"Пользователь %s оставил обращение в ЦУП СБЕ ПМиПИР.\n\n"+
			"Плагин: %s\n"+
			"Отправитель: %s\n\n"+
			"%s\n\n"+
			"---\nЭто автоматическое письмо из ЦУП СБЕ ПМиПИР.\n",
		u.Email, pluginLabel, u.Email, req.Text)

	if err := sendMail(recipient, subject, body); err != nil {
		log.Printf("handleFeedback sendMail: %v", err)
		s.storeFeedback(r.Context(), u.Email, req.PluginID, pluginLabel, recipient, req.Text, "failed")
		internalError(w, err)
		return
	}
	s.storeFeedback(r.Context(), u.Email, req.PluginID, pluginLabel, recipient, req.Text, "sent")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// feedbackRecipient определяет email получателя обращения:
//   - пустой plugin_id («идея») → собственник ЦУП (первый ADMIN_EMAILS);
//   - известный плагин → его ownerEmail из реестра (база /srv/www + добавления),
//     при отсутствии ownerEmail — первый ADMIN_EMAILS (все плагины компании сейчас
//     ведёт один владелец);
//   - неизвестный плагин → ошибка (400).
func (s *Server) feedbackRecipient(ctx context.Context, pluginID string) (recipient, pluginName string, err error) {
	if pluginID == "" {
		email := firstAdminEmail()
		if email == "" {
			return "", "", errors.New("feedback recipient is not configured")
		}
		return email, "", nil
	}
	for _, e := range s.registryEntries(ctx) {
		if e.ID == pluginID {
			email := strings.TrimSpace(e.OwnerEmail)
			if email == "" {
				email = firstAdminEmail()
			}
			if email == "" {
				return "", e.Name, errors.New("plugin has no owner email")
			}
			return email, e.Name, nil
		}
	}
	return "", "", errors.New("unknown plugin")
}

// registryEntries — объединённый список записей реестра: базовый файл
// (/srv/www/registry.json) + записи, добавленные администратором (registry_additions).
func (s *Server) registryEntries(ctx context.Context) []registryEntry {
	out := make([]registryEntry, 0)
	if base := s.registryBase(); base != nil {
		if list, ok := base["plugins"].([]any); ok {
			for _, raw := range list {
				if e, ok := asRegistryEntry(raw); ok {
					out = append(out, e)
				}
			}
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT entry FROM registry_additions ORDER BY created_at`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil || len(raw) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		if e, ok := asRegistryEntry(v); ok {
			out = append(out, e)
		}
	}
	return out
}

// asRegistryEntry декодирует произвольное JSON-значение реестра в registryEntry.
func asRegistryEntry(v any) (registryEntry, bool) {
	var e registryEntry
	buf, err := json.Marshal(v)
	if err != nil {
		return e, false
	}
	if err := json.Unmarshal(buf, &e); err != nil {
		return e, false
	}
	return e, true
}

// storeFeedback — журнал обращений (feedback_messages): фиксирует даже неудачную
// доставку (status='failed'), чтобы обращение не потерялось при сбое SMTP.
func (s *Server) storeFeedback(ctx context.Context, authorEmail, pluginID, pluginName, recipient, text, status string) {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO feedback_messages (author_email, plugin_id, plugin_name, recipient, text, status)
VALUES ($1, $2, $3, $4, $5, $6)`,
		authorEmail, pluginID, pluginName, recipient, text, status); err != nil {
		log.Printf("storeFeedback: %v", err)
	}
}

// firstAdminEmail — первый (по порядку в env) адрес ADMIN_EMAILS — используется
// как получатель «идей» (собственник ЦУП) и fallback-владелец плагина.
func firstAdminEmail() string {
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			return e
		}
	}
	return ""
}
