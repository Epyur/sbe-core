package main

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"
)

// maxJSONBody — максимальный размер JSON-тела запроса (1 МБ).
// Защита от DoS памятью медленными/огромными телами (ревью 1.4).
const maxJSONBody = 1 << 20

// decodeJSON читает JSON-тело с жёстким лимитом размера (ревью 1.4).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	return dec.Decode(v)
}

// clientIP достаёт IP из RemoteAddr ("IP:port" → "IP").
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipWindow — счётчик запросов в фиксированном окне для одного IP.
type ipWindow struct {
	start time.Time
	count int
}

// ipLimiter — простой fixed-window rate limiter в памяти (анти-brute-force).
// Достаточно одного инстанса: auth-service — единственный контейнер за Caddy.
type ipLimiter struct {
	mu   sync.Mutex
	win  time.Duration
	max  int
	seen map[string]*ipWindow
}

func newIPLimiter(win time.Duration, max int) *ipLimiter {
	return &ipLimiter{win: win, max: max, seen: make(map[string]*ipWindow)}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w, ok := l.seen[ip]
	if !ok || now.Sub(w.start) >= l.win {
		l.seen[ip] = &ipWindow{start: now, count: 1}
		return true
	}
	w.count++
	if len(l.seen) > 10000 {
		for k, v := range l.seen {
			if now.Sub(v.start) >= l.win {
				delete(l.seen, k)
			}
		}
	}
	return w.count <= l.max
}

// constTimeEqual — constant-time сравнение строк (ревью 2.1: сравнение
// секретов в authorizedRegister было обычным `==`, т.е. с утечкой по времени).
func constTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
