// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestOrderPersistsWithoutClearingDataOrChangingSettings(t *testing.T) {
	t.Chdir(t.TempDir())
	oldCfg, oldState, rev := cfg, state, configRevision
	oldCandles := candles
	defer func() { cfg, state, configRevision = oldCfg, oldState, rev; candles = oldCandles }()
	cfg = Config{Wallet: "keep", MarketProvider: "coinbase", CandleDays: 7, Sort: "az", Coins: []Coin{{ID: "bitcoin", Sym: "BTC"}, commodityCatalog[0].Coin}}
	state = State{Coins: []CoinState{{ID: "bitcoin", Price: 100}, {ID: "yahoo:GC=F", Price: 200}}}
	candles = map[string][]Candle{"bitcoin": {{1, 1, 2, 1, 2}}}
	w := httptest.NewRecorder()
	handleOrder(w, httptest.NewRequest("POST", "/api/order", strings.NewReader(`{"ids":["yahoo:GC=F","bitcoin"]}`)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if cfg.Sort != "config" || cfg.Coins[0].ID != "yahoo:GC=F" || state.Coins[0].Price != 200 || len(candles["bitcoin"]) != 1 || configRevision != rev {
		t.Fatal("order did not preserve market data")
	}
	saved := loadConfig()
	if saved.Wallet != "keep" || saved.MarketProvider != "coinbase" || saved.CandleDays != 7 || saved.Sort != "config" || saved.Coins[0].ID != "yahoo:GC=F" {
		t.Fatalf("saved config=%+v", saved)
	}
	if _, err := os.Stat("config.json.tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary config remains")
	}
}

func TestOrderRejectsIncompleteDuplicateAndUnknownAssets(t *testing.T) {
	t.Chdir(t.TempDir())
	oldCfg := cfg
	defer func() { cfg = oldCfg }()
	cfg = Config{Sort: "az", Coins: []Coin{{ID: "a"}, {ID: "b"}}}
	for _, body := range []string{`{"ids":["a"]}`, `{"ids":["a","a"]}`, `{"ids":["a","unknown"]}`, `broken`} {
		w := httptest.NewRecorder()
		handleOrder(w, httptest.NewRequest("POST", "/api/order", strings.NewReader(body)))
		if w.Code < 400 || cfg.Sort != "az" || cfg.Coins[0].ID != "a" {
			t.Fatalf("bad order accepted %s", body)
		}
	}
}

func TestOrderSaveFailureLeavesConfigUnchanged(t *testing.T) {
	t.Chdir(t.TempDir())
	oldCfg := cfg
	defer func() { cfg = oldCfg }()
	cfg = Config{Sort: "az", Coins: []Coin{{ID: "a"}, {ID: "b"}}}
	if err := os.Mkdir("config.json.tmp", 0700); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleOrder(w, httptest.NewRequest("POST", "/api/order", strings.NewReader(`{"ids":["b","a"]}`)))
	if w.Code != 500 || cfg.Sort != "az" || cfg.Coins[0].ID != "a" {
		t.Fatal("failed save changed order")
	}
	if !blockedStatic("/config.json.tmp") {
		t.Fatal("temporary config is publicly served")
	}
}

func TestInFlightPriceRefreshKeepsNewCustomOrder(t *testing.T) {
	t.Chdir(t.TempDir())
	oldCfg, oldState, oldBase, oldPrices := cfg, state, krakenBase, marketPrice
	defer func() { cfg, state, krakenBase, marketPrice = oldCfg, oldState, oldBase, oldPrices }()
	cfg = Config{MarketProvider: "kraken", Sort: "az", Coins: []Coin{{ID: "bitcoin", Sym: "BTC"}, {ID: "ethereum", Sym: "ETH"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orderResponse := httptest.NewRecorder()
		handleOrder(orderResponse, httptest.NewRequest("POST", "/api/order", strings.NewReader(`{"ids":["ethereum","bitcoin"]}`)))
		if orderResponse.Code != 200 {
			t.Error(orderResponse.Body.String())
		}
		w.Write([]byte(`{"error":[],"result":{"XBTUSD":{"c":["100"]},"ETHUSD":{"c":["20"]}}}`))
	}))
	defer server.Close()
	krakenBase = server.URL
	refreshFast()
	if len(state.Coins) != 2 || state.Coins[0].ID != "ethereum" || state.Coins[1].ID != "bitcoin" {
		t.Fatalf("refresh overwrote custom order: %v", state.Coins)
	}
}
