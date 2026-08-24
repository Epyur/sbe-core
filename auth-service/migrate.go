package main

import (
	"context"
)

func (s *Server) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			email      TEXT PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS devices (
			device_id  UUID PRIMARY KEY,
			user_id    TEXT NOT NULL REFERENCES users(email) ON DELETE CASCADE,
			label      TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS keys (
			key_hash   TEXT PRIMARY KEY,
			device_id  UUID NOT NULL REFERENCES devices(device_id) ON DELETE CASCADE,
			status     TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS apps (
			app_id         TEXT PRIMARY KEY,
			name           TEXT NOT NULL DEFAULT '',
			owner_email    TEXT NOT NULL DEFAULT '',
			service_secret TEXT NOT NULL DEFAULT ''
		)`,
		// last_seen_at — присутствие/онлайн (ЦУП, 2026-08-22): обновляется при каждой
		// выдаче токена (POST /auth/token) — единая точка, т.к. любой плагин сначала
		// получает токен здесь перед вызовом своего backend.
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ`,
		// Новости (ЦУП, 2026-08-22): сообщения от администрации — общие или для
		// ограниченного списка получателей (news_recipients), с флагом "обязательно
		// к прочтению" (mandatory) — отслеживается через news_reads.
		`CREATE TABLE IF NOT EXISTS news_messages (
			id           SERIAL PRIMARY KEY,
			author_email TEXT NOT NULL,
			title        TEXT NOT NULL,
			body         TEXT NOT NULL DEFAULT '',
			visibility   TEXT NOT NULL CHECK (visibility IN ('all','restricted')),
			mandatory    BOOLEAN NOT NULL DEFAULT false,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS news_recipients (
			message_id INTEGER NOT NULL REFERENCES news_messages(id) ON DELETE CASCADE,
			email      TEXT NOT NULL,
			PRIMARY KEY (message_id, email)
		)`,
		`CREATE TABLE IF NOT EXISTS news_reads (
			message_id INTEGER NOT NULL REFERENCES news_messages(id) ON DELETE CASCADE,
			email      TEXT NOT NULL,
			read_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (message_id, email)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
