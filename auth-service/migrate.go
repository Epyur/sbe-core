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
		// Управление service_secret приложений (2026-08-25): обновление apps.updated_at,
		// очередь ротаций (применяет хост-скрипт secret-applier) и журнал действий.
		`ALTER TABLE apps ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ`,
		`CREATE TABLE IF NOT EXISTS secret_rotations (
			app_id       TEXT PRIMARY KEY,
			new_secret   TEXT NOT NULL,
			status       TEXT NOT NULL DEFAULT 'pending',
			requested_by TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			applied_at   TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS secret_audit (
			id           SERIAL PRIMARY KEY,
			app_id       TEXT NOT NULL,
			action       TEXT NOT NULL,
			requested_by TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		// Динамический реестр (ЦУП, 2026-08-25): записи, добавленные администратором
		// из настроек ЦУП, сливаются с базовым registry.json в GET /registry.json.
		`CREATE TABLE IF NOT EXISTS registry_additions (
			id         SERIAL PRIMARY KEY,
			entry      JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		// app_env_pending (ЦУП, 2026-08-26): очередь admin-заданных значений
		// произвольных env-переменных приложения (напр. LAB_MAIL_LOGIN/PASSWORD) —
		// применяет тот же хост-скрипт secret-applier.sh, что и secret_rotations,
		// но ключ/значение свои у каждого приложения (белый список — env_admin.go),
		// не единственный "new_secret" на app_id. value обнуляется после applied —
		// секрет живёт в БД только до переноса в .env, не хранится тут постоянно.
		`CREATE TABLE IF NOT EXISTS app_env_pending (
			app_id       TEXT NOT NULL,
			env_key      TEXT NOT NULL,
			value        TEXT,
			status       TEXT NOT NULL DEFAULT 'pending',
			requested_by TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			applied_at   TIMESTAMPTZ,
			PRIMARY KEY (app_id, env_key)
		)`,
		// Обратная связь (ЦУП, 2026-08-28): журнал обращений пользователей —
		// замечания уходят владельцу выбранного плагина, «идеи» — собственнику
		// ЦУП. Статус фиксирует исход доставки (sent/failed).
		`CREATE TABLE IF NOT EXISTS feedback_messages (
			id           SERIAL PRIMARY KEY,
			author_email TEXT NOT NULL,
			plugin_id    TEXT NOT NULL DEFAULT '',
			plugin_name  TEXT NOT NULL DEFAULT '',
			recipient    TEXT NOT NULL DEFAULT '',
			text         TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'sent',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		// Инструмент ручной загрузки файлов плагина (ЦУП, 2026-08-29) — см.
		// docs/superpowers/specs/2026-08-29-sbe-plugin-file-upload-design.md. Одна
		// строка на dir реестра — оверлей поверх статического registry.json/
		// registry_additions (тот же паттерн), не мутирует их. Наличие строки для
		// dir'а — единственный сигнал клиенту (registry.ts) переключиться на раздачу
		// файлов с epyur.fvds.ru/plugins/* вместо raw.githubusercontent.com
		// (handleRegistryJSON проставляет selfHosted:true, см. registry_admin.go).
		`CREATE TABLE IF NOT EXISTS registry_file_overrides (
			dir         TEXT PRIMARY KEY,
			hashes      JSONB NOT NULL,
			uploaded_by TEXT NOT NULL,
			uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
