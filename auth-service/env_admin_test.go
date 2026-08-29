package main

import (
	"strings"
	"testing"
)

// isValidEnvValue — единственная защита от переноса control-символов
// (перевод строки/CR/NUL) в .env через admin-канал (env_admin.go); applier
// на хосте построчно парсит psql-вывод "app_id|env_key|value" — перевод
// строки в value сломал бы разбор строки/дал бы возможность внедрить
// произвольную СЛЕДУЮЩУЮ строку в .env.
func TestIsValidEnvValue(t *testing.T) {
	cases := []struct {
		name string
		v    string
		want bool
	}{
		{"обычный пароль", "p@ssw0rd!#$%^&*()", true},
		{"пустая строка — разрешена (пустое значение переменной)", "", true},
		{"перевод строки", "line1\nline2", false},
		{"CR", "line1\rline2", false},
		{"NUL", "abc\x00def", false},
		{"слишком длинное", strings.Repeat("a", 4097), false},
		{"ровно на границе", strings.Repeat("a", 4096), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isValidEnvValue(c.v); got != c.want {
				t.Errorf("isValidEnvValue(%q) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}

// allowedAppEnvKeys — белый список: единственное, что мешает admin-каналу
// стать способом переписать ЛЮБУЮ переменную сервера (JWT_SECRET,
// DATABASE_URL чужого сервиса и т.п.). Регресс-тест на непреднамеренное
// расширение списка/опечатку в имени приложения.
func TestAllowedAppEnvKeysLabWhitelist(t *testing.T) {
	lab, ok := allowedAppEnvKeys["lab"]
	if !ok {
		t.Fatal("allowedAppEnvKeys не содержит \"lab\"")
	}
	want := []string{
		"LAB_MAIL_ENABLED",
		"LAB_MAIL_IMAP_SERVER",
		"LAB_MAIL_LOGIN",
		"LAB_MAIL_PASSWORD",
		"LAB_MAIL_POLL_INTERVAL_SECONDS",
		"LAB_MAIL_METHOD_MAP",
		"LAB_MAIL_DEFAULT_PROJECT_CODE",
	}
	if len(lab) != len(want) {
		t.Fatalf("lab whitelist: got %d ключей, want %d: %v", len(lab), len(want), lab)
	}
	for _, k := range want {
		if !lab[k] {
			t.Errorf("lab whitelist: отсутствует ожидаемый ключ %q", k)
		}
	}
	// Опасные системные переменные не должны попасть НИ В ОДИН whitelist.
	dangerous := []string{"JWT_SECRET", "DATABASE_URL", "POSTGRES_PASSWORD", "AUTH_SERVICE_URL"}
	for app, keys := range allowedAppEnvKeys {
		for _, d := range dangerous {
			if keys[d] {
				t.Errorf("allowedAppEnvKeys[%q] содержит опасную системную переменную %q", app, d)
			}
		}
	}
}
