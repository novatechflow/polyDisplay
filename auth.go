// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 30 * 24 * time.Hour
	pinMaxFail = 5
	pinLockFor = 2 * time.Minute
)

type authTok struct {
	Typ string `json:"typ"` // access | refresh
	Exp int64  `json:"exp"`
	Jti string `json:"jti"`
}

func authEnabled() bool {
	return strings.TrimSpace(os.Getenv("POLYDISPLAY_PIN")) != ""
}

func tokenSecret() []byte {
	s := strings.TrimSpace(os.Getenv("POLYDISPLAY_TOKEN_SECRET"))
	if s != "" {
		return []byte(s)
	}
	sum := sha256.Sum256([]byte("polydisplay|" + os.Getenv("POLYDISPLAY_PIN")))
	return sum[:]
}

func mintToken(typ string, ttl time.Duration) (string, error) {
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	p, err := json.Marshal(authTok{
		Typ: typ,
		Exp: time.Now().Add(ttl).Unix(),
		Jti: base64.RawURLEncoding.EncodeToString(jti),
	})
	if err != nil {
		return "", err
	}
	pb := base64.RawURLEncoding.EncodeToString(p)
	mac := hmac.New(sha256.New, tokenSecret())
	mac.Write([]byte(pb))
	return pb + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func parseToken(s, wantTyp string) bool {
	s = strings.TrimSpace(s)
	i := strings.IndexByte(s, '.')
	if i <= 0 || i == len(s)-1 {
		return false
	}
	pb, sig := s[:i], s[i+1:]
	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, tokenSecret())
	mac.Write([]byte(pb))
	if !hmac.Equal(mac.Sum(nil), raw) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(pb)
	if err != nil {
		return false
	}
	var t authTok
	if json.Unmarshal(b, &t) != nil || t.Typ != wantTyp || time.Now().Unix() >= t.Exp {
		return false
	}
	return true
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) >= 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func writeTokens(w http.ResponseWriter) {
	access, err1 := mintToken("access", accessTTL)
	refresh, err2 := mintToken("refresh", refreshTTL)
	if err1 != nil || err2 != nil {
		http.Error(w, "token mint failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"token_type":    "Bearer",
		"access_token":  access,
		"refresh_token": refresh,
		"expires_in":    int(accessTTL.Seconds()),
	})
}

var (
	pinMu    sync.Mutex
	pinFails = map[string]struct {
		n     int
		until time.Time
	}{}
)

func pinLocked(ip string) bool {
	pinMu.Lock()
	defer pinMu.Unlock()
	st := pinFails[ip]
	return time.Now().Before(st.until)
}

func pinFail(ip string) {
	pinMu.Lock()
	defer pinMu.Unlock()
	st := pinFails[ip]
	st.n++
	if st.n >= pinMaxFail {
		st.until = time.Now().Add(pinLockFor)
		st.n = 0
	}
	pinFails[ip] = st
}

func pinOK(ip string) {
	pinMu.Lock()
	delete(pinFails, ip)
	pinMu.Unlock()
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func pinMatch(got string) bool {
	want := strings.TrimSpace(os.Getenv("POLYDISPLAY_PIN"))
	if want == "" {
		return false
	}
	a := sha256.Sum256([]byte(got))
	b := sha256.Sum256([]byte(want))
	return hmac.Equal(a[:], b[:])
}

func handleAuthPin(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		return
	}
	if !authEnabled() {
		http.Error(w, "auth disabled", 404)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	ip := clientIP(r)
	if pinLocked(ip) {
		http.Error(w, "locked", 429)
		return
	}
	var body struct {
		PIN string `json:"pin"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	got := strings.TrimSpace(body.PIN)
	if !pinMatch(got) {
		pinFail(ip)
		http.Error(w, "bad pin", 401)
		return
	}
	pinOK(ip)
	writeTokens(w)
}

func handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		return
	}
	if !authEnabled() {
		http.Error(w, "auth disabled", 404)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	if !parseToken(bearer(r), "refresh") {
		http.Error(w, "unauthorized", 401)
		return
	}
	writeTokens(w)
}

func requireAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method == http.MethodOptions {
			return
		}
		if authEnabled() && !parseToken(bearer(r), "access") {
			http.Error(w, "unauthorized", 401)
			return
		}
		next(w, r)
	}
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func blockedStatic(p string) bool {
	p = strings.TrimPrefix(p, "/")
	switch {
	case p == "config.json", p == ".env", p == "go.mod":
		return true
	case strings.HasPrefix(p, "polydisplay.log"), strings.HasPrefix(p, ".git"), strings.HasSuffix(p, ".go"), strings.HasSuffix(p, ".sh"):
		return true
	default:
		return false
	}
}
