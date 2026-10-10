// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommodityQuoteAndSessionHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v8/finance/chart/GC=F" || r.URL.Query().Get("interval") != "15m" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"chart":{"error":null,"result":[{"meta":{"currency":"USD","regularMarketPrice":2100,"regularMarketTime":1700000000,"previousClose":2000,"chartPreviousClose":1800,"shortName":"Gold Dec 26"},"timestamp":[100,200,300,400],"indicators":{"quote":[{"open":[2000,null,2010,2010],"high":[2020,null,2020,2000],"low":[1990,null,2000,2020],"close":[2010,null,2015,2010]}]}}]}}`))
	}))
	defer srv.Close()
	old := yahooBase
	yahooBase = srv.URL
	defer func() { yahooBase = old }()
	cs, q, err := fetchCommodity(commodityCatalog[0].Coin, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[1][0] != 300000 {
		t.Fatalf("null and malformed bars not filtered: %v", cs)
	}
	if q.Change == nil || *q.Change < 4.99 || *q.Change > 5.01 || q.Contract != "Gold Dec 26" || q.Time != 1700000000 {
		t.Fatalf("wrong session reference or quote: %+v", q)
	}
}

func TestCommodityRejectsUpstreamFailuresAndWrongCurrency(t *testing.T) {
	for _, body := range []string{
		`{"chart":{"error":{"description":"not found"},"result":null}}`,
		`{"chart":{"result":[{"meta":{"currency":"EUR","regularMarketPrice":2000,"regularMarketTime":100}}]}}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		old := yahooBase
		yahooBase = srv.URL
		_, _, err := fetchCommodity(commodityCatalog[0].Coin, 7)
		yahooBase = old
		srv.Close()
		if err == nil {
			t.Fatal("bad quote accepted")
		}
	}
}

func TestCommoditySearchAndCryptoIsolation(t *testing.T) {
	w := httptest.NewRecorder()
	handleSearch(w, httptest.NewRequest("GET", "/api/search?type=commodity&q=gold", nil))
	var found []struct{ ID, Kind string }
	if err := json.Unmarshal(w.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "yahoo:GC=F" || found[0].Kind != "commodity" {
		t.Fatalf("search=%s", w.Body.String())
	}
	mixed := append([]Coin{{Sym: "BTC", ID: "bitcoin"}}, commodityCatalog[0].Coin)
	cs := cryptoCoins(mixed)
	if len(cs) != 1 || cs[0].ID != "bitcoin" {
		t.Fatal("commodity reached crypto feed")
	}
}

func TestWalletCanBeClearedWithoutChangingWatchlist(t *testing.T) {
	t.Chdir(t.TempDir())
	oldCfg, oldState, oldRev := cfg, state, configRevision
	defer func() { cfg, state, configRevision = oldCfg, oldState, oldRev }()
	wallet := "0x" + strings.Repeat("1", 40)
	cfg = Config{Wallet: wallet, Coins: []Coin{{ID: "bitcoin", Sym: "BTC"}}}
	state = State{Wallet: wallet, Positions: []Position{{Title: "old"}}, Pnl: &PnL{}}
	w := httptest.NewRecorder()
	handleConfig(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(`{"wallet":""}`)))
	if w.Code != 200 || cfg.Wallet != "" || state.Wallet != "" || len(state.Positions) != 0 || state.Pnl != nil || len(cfg.Coins) != 1 {
		t.Fatalf("wallet clear failed: %d", w.Code)
	}
	if loadConfig().Wallet != "" {
		t.Fatal("wallet persisted")
	}
	cfg.Wallet = wallet
	w = httptest.NewRecorder()
	handleConfig(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(`{"candleDays":7}`)))
	if cfg.Wallet != wallet {
		t.Fatal("omitted wallet unexpectedly cleared")
	}
	for _, body := range []string{`{"wallet":"bad"}`, `{"coins":[{"id":"yahoo:GC=F"}]}`, `{"coins":[{"id":"yahoo:FAKE","kind":"commodity"}]}`} {
		w = httptest.NewRecorder()
		handleConfig(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("bad config accepted %s", body)
		}
	}
}
