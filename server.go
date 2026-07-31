// polyDisplay aggregator server.
//
// Polls Polymarket + (Binance-first, CoinGecko-fallback) on its own schedule,
// caches the result, and serves ONE cheap endpoint (/api/state) plus the static
// web app to the iPad. The iPad therefore makes a single local request and never
// touches an external API, cert, rate limit, or geoblock.
//
//   go run .            # dev
//   go build -o polydisplayd .   # binary for launchd (see install-macos.sh)
//
// Optional: set CG_DEMO_KEY to a free CoinGecko demo key for higher limits.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

/* ------------------------- config ------------------------- */

type Coin struct {
	Sym  string `json:"sym"`
	Name string `json:"name"`
	ID   string `json:"id"`           // CoinGecko id
	Bn   string `json:"bn,omitempty"` // Binance symbol override; default SYM+USDT
}

type Config struct {
	Wallet     string `json:"wallet"`
	CandleDays int    `json:"candleDays"`
	Port       int    `json:"port"`
	Sort       string `json:"sort"` // "az" (symbol A-Z) | "config" (as added)
	Coins      []Coin `json:"coins"`
}

const configPath = "config.json"

func defaultConfig() Config {
	return Config{
		Wallet:     "",
		CandleDays: 1,
		Port:       8080,
		Sort:       "trades",
		Coins: []Coin{
			{"BTC", "Bitcoin", "bitcoin", ""},
			{"ETH", "Ethereum", "ethereum", ""},
			{"FLR", "Flare", "flare-networks", ""},
			{"HBAR", "Hedera", "hedera-hashgraph", ""},
			{"POND", "Marlin", "marlin", ""},
			{"SOL", "Solana", "solana", ""},
			{"TRUMP", "Official Trump", "official-trump", ""},
			{"WIF", "dogwifhat", "dogwifcoin", ""},
			{"XLM", "Stellar", "stellar", ""},
			{"XRP", "XRP", "ripple", ""},
		},
	}
}

func loadConfig() Config {
	b, err := os.ReadFile(configPath)
	if err != nil {
		c := defaultConfig()
		saveConfig(c)
		return c
	}
	var c Config
	if json.Unmarshal(b, &c) != nil {
		return defaultConfig()
	}
	if c.Port == 0 {
		c.Port = 8080
	}
	if c.CandleDays == 0 {
		c.CandleDays = 7
	}
	if c.Sort == "" {
		c.Sort = "trades"
	}
	return c
}

// order coins for display per cfg.Sort
//   "trades" (default): tokens in current Polymarket positions first, then A-Z
//   "az": symbol A-Z    "config": as added
func sortedCoins(coins []Coin, mode string, active map[string]bool) []Coin {
	out := make([]Coin, len(coins))
	copy(out, coins)
	byAZ := func(i, j int) bool { return strings.ToUpper(out[i].Sym) < strings.ToUpper(out[j].Sym) }
	switch mode {
	case "config":
		// keep as added
	case "az":
		sort.SliceStable(out, byAZ)
	default: // "trades"
		sort.SliceStable(out, func(i, j int) bool {
			ai, aj := active[out[i].ID], active[out[j].ID]
			if ai != aj {
				return ai // traded assets float to the top
			}
			return byAZ(i, j)
		})
	}
	return out
}

// which watchlist tokens are referenced by current Polymarket positions
// (e.g. a "Will Ethereum reach $X" market activates ETH)
func activeCoins(positions []Position, coins []Coin) map[string]bool {
	active := map[string]bool{}
	for _, p := range positions {
		lt := strings.ToLower(p.Title)
		for _, cn := range coins {
			if strings.Contains(lt, strings.ToLower(cn.Name)) ||
				(len(cn.Sym) >= 3 && strings.Contains(lt, strings.ToLower(cn.Sym))) {
				active[cn.ID] = true
			}
		}
	}
	return active
}

func saveConfig(c Config) {
	b, _ := json.MarshalIndent(c, "", "  ")
	os.WriteFile(configPath, b, 0644)
}

/* ------------------------- state ------------------------- */

type Candle [5]float64 // [openTimeMs, open, high, low, close]

type CoinState struct {
	Sym     string   `json:"sym"`
	Name    string   `json:"name"`
	ID      string   `json:"id"`
	Price   float64  `json:"price"`
	Chg24h  float64  `json:"chg24h"`
	Source  string   `json:"source"` // "binance" | "coingecko" | ""
	Active  bool     `json:"active"` // referenced by a current Polymarket position
	Candles []Candle `json:"candles"`
}

type Position struct {
	Title        string  `json:"title"`
	Outcome      string  `json:"outcome"`
	Asset        string  `json:"asset"`
	Size         float64 `json:"size"`
	AvgPrice     float64 `json:"avgPrice"`
	CurPrice     float64 `json:"curPrice"`
	CashPnl      float64 `json:"cashPnl"`
	PercentPnl   float64 `json:"percentPnl"`
	CurrentValue float64 `json:"currentValue"`
}

type Act struct {
	Time    int64   `json:"t"`
	Type    string  `json:"type"`    // TRADE, REDEEM, MERGE, SPLIT, REWARD, ...
	Side    string  `json:"side"`    // BUY / SELL (for TRADE)
	Size    float64 `json:"size"`
	Price   float64 `json:"price"`
	Usdc    float64 `json:"usdc"`
	Title   string  `json:"title"`
	Outcome string  `json:"outcome"`
}

type State struct {
	Updated    int64       `json:"updated"`
	Wallet     string      `json:"wallet"`
	CandleDays int         `json:"candleDays"`
	Positions  []Position  `json:"positions"`
	Coins      []CoinState `json:"coins"`
	Activity   []Act       `json:"activity"`
	Note       string      `json:"note"`
}

var (
	mu      sync.RWMutex
	cfg     Config
	state   State
	candles = map[string][]Candle{}    // id -> candles (refreshed slowly)
	csource = map[string]string{}      // id -> source
	bnHas   = map[string]bool{}        // id -> has a working Binance pair (absent = unknown)
	cgPrice = map[string][2]float64{}  // id -> {price, chg} cached CoinGecko price (non-Binance coins)
	// Fresh connection per request: a VPN's short idle timeout was dropping the
	// pooled keep-alive connections during the 20s gap between cycles, so the
	// first couple of requests each cycle failed (BTC/ETH showed price 0).
	client = &http.Client{
		Timeout:   12 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true},
	}
	trigger = make(chan struct{}, 1)
)

/* ------------------------- HTTP helpers ------------------------- */

func getJSON(url string, out interface{}, headers map[string]string) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ { // retry once on a network error
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return json.Unmarshal(b, out)
	}
	return lastErr
}

func cgHeaders() map[string]string {
	if k := os.Getenv("CG_DEMO_KEY"); k != "" {
		return map[string]string{"x-cg-demo-api-key": k}
	}
	return nil
}

/* ------------------------- data fetchers ------------------------- */

func bnSymbol(c Coin) string {
	if c.Bn != "" {
		return c.Bn
	}
	return c.Sym + "USDT"
}

func bnParams(days int) (string, int) {
	switch {
	case days <= 1:
		return "30m", 48
	case days <= 7:
		return "4h", 42
	case days <= 14:
		return "8h", 42
	default:
		return "12h", 60
	}
}

func fetchBinanceCandles(c Coin, days int) ([]Candle, error) {
	iv, lim := bnParams(days)
	url := fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=%s&interval=%s&limit=%d", bnSymbol(c), iv, lim)
	var raw [][]interface{}
	if err := getJSON(url, &raw, nil); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty")
	}
	out := make([]Candle, 0, len(raw))
	for _, k := range raw {
		if len(k) < 5 {
			continue
		}
		t, _ := k[0].(float64)
		o, _ := strconv.ParseFloat(fmt.Sprint(k[1]), 64)
		h, _ := strconv.ParseFloat(fmt.Sprint(k[2]), 64)
		l, _ := strconv.ParseFloat(fmt.Sprint(k[3]), 64)
		cl, _ := strconv.ParseFloat(fmt.Sprint(k[4]), 64)
		out = append(out, Candle{t, o, h, l, cl})
	}
	return out, nil
}

func fetchCgCandles(c Coin, days int) ([]Candle, error) {
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/coins/%s/ohlc?vs_currency=usd&days=%d", c.ID, days)
	var raw [][]float64
	if err := getJSON(url, &raw, cgHeaders()); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty")
	}
	out := make([]Candle, 0, len(raw))
	for _, k := range raw {
		if len(k) >= 5 {
			out = append(out, Candle{k[0], k[1], k[2], k[3], k[4]})
		}
	}
	return out, nil
}

// live price + 24h change from Binance (primary source)
func fetchBinanceTicker(sym string) (float64, float64, error) {
	var t struct {
		LastPrice string `json:"lastPrice"`
		Pct       string `json:"priceChangePercent"`
	}
	if err := getJSON("https://api.binance.com/api/v3/ticker/24hr?symbol="+sym, &t, nil); err != nil {
		return 0, 0, err
	}
	if t.LastPrice == "" {
		return 0, 0, fmt.Errorf("no price")
	}
	p, _ := strconv.ParseFloat(t.LastPrice, 64)
	c, _ := strconv.ParseFloat(t.Pct, 64)
	return p, c, nil
}

// CoinGecko price fallback (only for tokens with no Binance pair, e.g. FLR)
func fetchCgPrice(id string) (float64, float64, error) {
	var m map[string]struct {
		USD float64 `json:"usd"`
		Chg float64 `json:"usd_24h_change"`
	}
	url := "https://api.coingecko.com/api/v3/simple/price?ids=" + id + "&vs_currencies=usd&include_24hr_change=true"
	if err := getJSON(url, &m, cgHeaders()); err != nil {
		return 0, 0, err
	}
	v, ok := m[id]
	if !ok {
		return 0, 0, fmt.Errorf("no price")
	}
	return v.USD, v.Chg, nil
}

func fetchPositions(wallet string) ([]Position, error) {
	url := "https://data-api.polymarket.com/positions?user=" + wallet +
		"&sizeThreshold=0.1&limit=100&sortBy=CURRENT&sortDirection=DESC"
	var out []Position
	err := getJSON(url, &out, nil)
	return out, err
}

func fetchActivity(wallet string) ([]Act, error) {
	url := "https://data-api.polymarket.com/activity?user=" + wallet + "&limit=20"
	var raw []struct {
		Timestamp int64   `json:"timestamp"`
		Type      string  `json:"type"`
		Side      string  `json:"side"`
		Size      float64 `json:"size"`
		Price     float64 `json:"price"`
		UsdcSize  float64 `json:"usdcSize"`
		Title     string  `json:"title"`
		Outcome   string  `json:"outcome"`
	}
	if err := getJSON(url, &raw, nil); err != nil {
		return nil, err
	}
	out := make([]Act, 0, len(raw))
	for _, a := range raw {
		out = append(out, Act{a.Timestamp, a.Type, a.Side, a.Size, a.Price, a.UsdcSize, a.Title, a.Outcome})
	}
	return out, nil
}

/* ------------------------- refresh loops ------------------------- */

// fast: positions + live prices (every 20s)
func refreshFast() {
	mu.RLock()
	c := cfg
	mu.RUnlock()

	note := ""
	positions, err := fetchPositions(c.Wallet)
	if err != nil {
		note = "polymarket: " + err.Error()
	}
	activity, aerr := fetchActivity(c.Wallet)
	if aerr != nil {
		activity = nil
	}

	// Binance-only hot path. Tokens with no Binance pair (e.g. FLR) use the
	// CoinGecko price cached by the slow loop, so CoinGecko is never called at
	// 20s cadence (that was the source of the 429s).
	type pr struct{ price, chg float64 }
	prices := map[string]pr{}
	for _, cn := range c.Coins {
		mu.RLock()
		known, seen := bnHas[cn.ID]
		cached, hasCache := cgPrice[cn.ID]
		mu.RUnlock()

		if !seen || known { // unknown or known-on-Binance -> try Binance
			if p, ch, e := fetchBinanceTicker(bnSymbol(cn)); e == nil {
				prices[cn.ID] = pr{p, ch}
				mu.Lock()
				bnHas[cn.ID] = true
				mu.Unlock()
				continue
			}
			mu.Lock()
			bnHas[cn.ID] = false
			mu.Unlock()
		}
		if hasCache { // non-Binance token -> use cached CoinGecko price
			prices[cn.ID] = pr{cached[0], cached[1]}
		}
		// else: not yet cached (first few seconds after start) -> shows blank, not an error
	}

	active := activeCoins(positions, c.Coins)

	mu.Lock()
	coinStates := make([]CoinState, 0, len(c.Coins))
	for _, cn := range sortedCoins(c.Coins, c.Sort, active) {
		p := prices[cn.ID]
		coinStates = append(coinStates, CoinState{
			Sym: cn.Sym, Name: cn.Name, ID: cn.ID,
			Price: p.price, Chg24h: p.chg,
			Source: csource[cn.ID], Active: active[cn.ID], Candles: candles[cn.ID],
		})
	}
	state = State{
		Updated:    time.Now().Unix(),
		Wallet:     c.Wallet,
		CandleDays: c.CandleDays,
		Positions:  positions,
		Coins:      coinStates,
		Activity:   activity,
		Note:       note,
	}
	mu.Unlock()
}

// slow: candles + polymarket history (every 5 min), spaced out
func refreshSlow() {
	mu.RLock()
	c := cfg
	mu.RUnlock()

	for _, cn := range c.Coins {
		var cs []Candle
		var src string
		if bc, err := fetchBinanceCandles(cn, c.CandleDays); err == nil {
			cs, src = bc, "binance"
		} else if gc, err := fetchCgCandles(cn, c.CandleDays); err == nil {
			cs, src = gc, "coingecko"
		}
		if cs != nil {
			mu.Lock()
			candles[cn.ID] = cs
			csource[cn.ID] = src
			bnHas[cn.ID] = src == "binance"
			mu.Unlock()
		}
		// non-Binance token: cache a CoinGecko price so the hot path never calls CG
		if src == "coingecko" {
			if p, ch, e := fetchCgPrice(cn.ID); e == nil {
				mu.Lock()
				cgPrice[cn.ID] = [2]float64{p, ch}
				mu.Unlock()
			}
			time.Sleep(1500 * time.Millisecond) // gentle on CoinGecko free tier
		} else {
			time.Sleep(200 * time.Millisecond) // Binance handles bursts fine -> snappy refresh
		}
	}
}

func loops() {
	refreshFast()
	go refreshSlow()
	fast := time.NewTicker(20 * time.Second)
	slow := time.NewTicker(5 * time.Minute)
	for {
		select {
		case <-fast.C:
			refreshFast()
		case <-slow.C:
			go refreshSlow()
		case <-trigger:
			refreshFast()
			go refreshSlow()
		}
	}
}

/* ------------------------- HTTP handlers ------------------------- */

func handleState(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	b, _ := json.Marshal(state)
	mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(b)
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == "POST" {
		var nc Config
		if err := json.NewDecoder(r.Body).Decode(&nc); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		mu.Lock()
		if nc.Wallet != "" {
			cfg.Wallet = nc.Wallet
		}
		if nc.CandleDays != 0 {
			cfg.CandleDays = nc.CandleDays
		}
		if nc.Sort != "" {
			cfg.Sort = nc.Sort
		}
		if nc.Coins != nil {
			cfg.Coins = nc.Coins
		}
		saved := cfg
		candles = map[string][]Candle{}
		csource = map[string]string{}
		bnHas = map[string]bool{}
		cgPrice = map[string][2]float64{}
		mu.Unlock()
		saveConfig(saved)
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	mu.RLock()
	b, _ := json.Marshal(cfg)
	mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// proxy CoinGecko search so the iPad never calls out directly
func handleSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	q := r.URL.Query().Get("q")
	if q == "" {
		w.Write([]byte("[]"))
		return
	}
	var raw struct {
		Coins []struct {
			ID     string `json:"id"`
			Symbol string `json:"symbol"`
			Name   string `json:"name"`
		} `json:"coins"`
	}
	if err := getJSON("https://api.coingecko.com/api/v3/search?query="+q, &raw, cgHeaders()); err != nil {
		http.Error(w, "[]", 200)
		return
	}
	type res struct {
		Sym, Name, ID string
	}
	out := []res{}
	for i, c := range raw.Coins {
		if i >= 10 {
			break
		}
		out = append(out, res{c.Symbol, c.Name, c.ID})
	}
	b, _ := json.Marshal(out)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func main() {
	cfg = loadConfig()
	log.Printf("polyDisplay: %d assets, wallet %s, port %d", len(cfg.Coins), cfg.Wallet, cfg.Port)

	go loops()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", handleState)
	mux.HandleFunc("/api/config", handleConfig)
	mux.HandleFunc("/api/search", handleSearch)

	fs := http.FileServer(http.Dir("."))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// AppCache manifest needs the right MIME type on iOS
		if len(r.URL.Path) >= 9 && r.URL.Path[len(r.URL.Path)-9:] == ".appcache" {
			w.Header().Set("Content-Type", "text/cache-manifest")
		}
		w.Header().Set("Cache-Control", "no-cache")
		fs.ServeHTTP(w, r)
	}))

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("listening on http://0.0.0.0%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
