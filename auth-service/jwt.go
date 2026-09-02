package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// newDeviceID генерирует UUID v4 для устройств, заводимых сервером (веб-портал,
// у которого своего device_id ещё нет — в отличие от Obsidian-плагина, который
// генерирует его сам на клиенте). Формат совместим с uuidRe.
func newDeviceID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16]), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// signJWT подписывает токен указанным секретом (Блок D, ревью 1.2): auth-service
// больше не использует общий JWT_SECRET — каждый токен подписывается КЛЮЧОМ
// ПРИЛОЖЕНИЯ (apps.service_secret / {APP}_SERVICE_SECRET). Добавлены iss/aud.
func signJWT(email, deviceID, appID, channel, secret string, ttl time.Duration) (string, time.Time, error) {
	if secret == "" {
		return "", time.Time{}, jwt.ErrInvalidKey
	}
	now := time.Now()
	exp := now.Add(ttl)
	claims := jwt.MapClaims{
		"email":     email,
		"device_id": deviceID,
		"app_id":    appID,
		// channel — "plugin" (Obsidian, ключ доставлен по exim) или "web"
		// (magic-link) — plugin-services урезают запись/superadmin для "web".
		"channel": channel,
		"iss":     "auth-service",
		"aud":     appID,
		"iat":     now.Unix(),
		"exp":     exp.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	return signed, exp, err
}

// tokenTTL — срок жизни токена (env AUTH_TOKEN_TTL, по умолчанию 1 час).
func tokenTTL() time.Duration {
	ttl := time.Hour
	if v := os.Getenv("AUTH_TOKEN_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			ttl = d
		}
	}
	return ttl
}
