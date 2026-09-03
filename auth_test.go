// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/pin", handleAuthPin)
	mux.HandleFunc("/api/auth/refresh", handleAuthRefresh)
	mux.HandleFunc("/api/state", requireAccess(handleState))
	mux.Handle("/", staticHandler())
	return mux
}

func TestStateOpenWithoutPin(t *testing.T) {
	t.Setenv("POLYDISPLAY_PIN", "")
	t.Setenv("POLYDISPLAY_TOKEN_SECRET", "")
	srv := httptest.NewServer(authMux())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d, want 200 when PIN unset", resp.StatusCode)
	}
}

func TestPinIssuesBearerAndGatesAPI(t *testing.T) {
	t.Setenv("POLYDISPLAY_PIN", "123456")
	t.Setenv("POLYDISPLAY_TOKEN_SECRET", "unit-test-secret")
	srv := httptest.NewServer(authMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauthed state %d, want 401", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/api/auth/pin", "application/json", strings.NewReader(`{"pin":"000000"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("bad pin %d, want 401", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/api/auth/pin", "application/json", strings.NewReader(`{"pin":"123456"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("good pin %d, want 200", resp.StatusCode)
	}
	var tok struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Type    string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	if tok.Type != "Bearer" || tok.Access == "" || tok.Refresh == "" {
		t.Fatalf("tokens: %+v", tok)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/state", nil)
	req.Header.Set("Authorization", "Bearer "+tok.Access)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("bearer state %d, want 200", resp.StatusCode)
	}

	req, _ = http.NewRequest("POST", srv.URL+"/api/auth/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+tok.Refresh)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("refresh %d %s", resp.StatusCode, b)
	}
	var tok2 struct {
		Access string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok2); err != nil {
		t.Fatal(err)
	}
	if tok2.Access == "" {
		t.Fatal("refresh returned empty access")
	}
}

func TestBlockedStaticSecrets(t *testing.T) {
	srv := httptest.NewServer(staticHandler())
	defer srv.Close()
	for _, p := range []string{"/config.json", "/.env", "/server.go", "/install.sh"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s status %d, want 404", p, resp.StatusCode)
		}
	}
	resp, err := http.Get(srv.URL + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("index.html %d, want 200", resp.StatusCode)
	}
}
