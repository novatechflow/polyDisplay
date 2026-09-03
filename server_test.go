// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"30", 30 * time.Second},
		{" 5 ", 5 * time.Second},
		{"0", 0},
		{"-1", 0},
		{"garbage", 0},
		{http.TimeFormat, 0}, // unparseable as a date -> 0
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	// HTTP-date form: a minute out should land near a minute.
	got := parseRetryAfter(time.Now().Add(time.Minute).UTC().Format(http.TimeFormat))
	if got < 50*time.Second || got > time.Minute {
		t.Errorf("parseRetryAfter(date +1m) = %v, want ~1m", got)
	}
	// A date in the past must not produce a negative wait.
	if got := parseRetryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)); got != 0 {
		t.Errorf("parseRetryAfter(past date) = %v, want 0", got)
	}
}

func TestPolyRateLimitedBackoff(t *testing.T) {
	polyNextAt, polyBackoff = time.Time{}, 0
	t.Cleanup(func() { polyNextAt, polyBackoff = time.Time{}, 0 })

	// doubles from the floor, then clamps at the ceiling
	want := []time.Duration{
		polyBackoffMin,
		2 * polyBackoffMin,
		4 * polyBackoffMin,
		8 * polyBackoffMin,
		polyBackoffMax,
		polyBackoffMax,
	}
	for i, w := range want {
		polyRateLimited(0)
		if polyBackoff != w {
			t.Fatalf("after %d rate limits: backoff = %v, want %v", i+1, polyBackoff, w)
		}
		if d := time.Until(polyNextAt); d > w || d < w-time.Second {
			t.Fatalf("after %d rate limits: next call in %v, want ~%v", i+1, d, w)
		}
	}
}

func TestPolyRateLimitedHonoursRetryAfter(t *testing.T) {
	polyNextAt, polyBackoff = time.Time{}, 0
	t.Cleanup(func() { polyNextAt, polyBackoff = time.Time{}, 0 })

	// Retry-After longer than our own backoff wins...
	polyRateLimited(polyBackoffMin + time.Minute)
	if d := time.Until(polyNextAt); d < polyBackoffMin+50*time.Second {
		t.Errorf("next call in %v, want the longer Retry-After", d)
	}
	// ...and a shorter one does not shorten the backoff.
	polyNextAt, polyBackoff = time.Time{}, 0
	polyRateLimited(time.Second)
	if d := time.Until(polyNextAt); d < polyBackoffMin-time.Second {
		t.Errorf("next call in %v, want at least the %v floor", d, polyBackoffMin)
	}
}

// End-to-end: a rate-limited data-api must not blank the dashboard, must stop
// calling until the backoff expires, and must say why in the banner.
func TestRefreshFastBacksOffAndKeepsLastPositions(t *testing.T) {
	var hits int32
	rateLimited := int32(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if atomic.LoadInt32(&rateLimited) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/positions") {
			w.Write([]byte(`[{"title":"Will Bitcoin win","outcome":"Yes","size":1,"currentValue":2}]`))
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	origBase := polyBase
	polyBase = srv.URL
	cfg = Config{Wallet: "0xtest", CandleDays: 1, Sort: "az"} // no coins -> no Binance calls
	polyNextAt, polyBackoff, polyWallet = time.Time{}, 0, ""
	lastPositions, lastActivity = nil, nil
	t.Cleanup(func() {
		polyBase = origBase
		cfg, state = Config{}, State{}
		polyNextAt, polyBackoff, polyWallet = time.Time{}, 0, ""
		lastPositions, lastActivity = nil, nil
	})

	// First cycle succeeds: we have real positions to protect.
	atomic.StoreInt32(&rateLimited, 0)
	refreshFast()
	if len(state.Positions) != 1 || state.Note != "" {
		t.Fatalf("healthy cycle: positions=%d note=%q, want 1 and no note", len(state.Positions), state.Note)
	}

	// Now the API starts rate limiting. Force the next call to be due.
	atomic.StoreInt32(&rateLimited, 1)
	polyNextAt = time.Time{}
	refreshFast()
	if len(state.Positions) != 1 {
		t.Errorf("after 429: positions=%d, want the last good ones kept", len(state.Positions))
	}
	if !strings.Contains(state.Note, "rate limited") {
		t.Errorf("after 429: note=%q, want a rate-limit explanation", state.Note)
	}
	if polyBackoff != polyBackoffMin {
		t.Errorf("after 429: backoff=%v, want %v", polyBackoff, polyBackoffMin)
	}

	// Subsequent cycles inside the backoff window must not touch the API.
	before := atomic.LoadInt32(&hits)
	refreshFast()
	refreshFast()
	if got := atomic.LoadInt32(&hits); got != before {
		t.Errorf("made %d calls during backoff, want 0", got-before)
	}
	if len(state.Positions) != 1 || !strings.Contains(state.Note, "rate limited") {
		t.Errorf("during backoff: positions=%d note=%q", len(state.Positions), state.Note)
	}

	// Once the window passes and the API recovers, we resume and clear the note.
	atomic.StoreInt32(&rateLimited, 0)
	polyNextAt = time.Now().Add(-time.Second)
	refreshFast()
	if state.Note != "" || polyBackoff != 0 {
		t.Errorf("after recovery: note=%q backoff=%v, want cleared", state.Note, polyBackoff)
	}
}

func TestFetchPositionsSortsByEndTime(t *testing.T) {
	// Same calendar day for noon/evening: only gamma's timestamp separates them.
	poly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"title":"later","conditionId":"0xlater","endDate":"2026-09-01","currentValue":90},
			{"title":"undated","conditionId":"0xnone","currentValue":80},
			{"title":"evening-big","conditionId":"0xeve","endDate":"2026-08-12","currentValue":70},
			{"title":"noon-small","conditionId":"0xnoon","endDate":"2026-08-12","currentValue":5}
		]`))
	}))
	defer poly.Close()

	var gammaCalls int
	gamma := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gammaCalls++
		w.Write([]byte(`[
			{"conditionId":"0xlater","endDate":"2026-09-01T16:00:00Z"},
			{"conditionId":"0xeve","endDate":"2026-08-12T18:00:00Z"},
			{"conditionId":"0xnoon","endDate":"2026-08-12T12:00:00Z"}
		]`))
	}))
	defer gamma.Close()

	origPoly, origGamma := polyBase, gammaBase
	polyBase, gammaBase = poly.URL, gamma.URL
	endTimes = map[string]string{}
	t.Cleanup(func() {
		polyBase, gammaBase = origPoly, origGamma
		endTimes = map[string]string{}
	})

	got, err := fetchPositions("0xtest")
	if err != nil {
		t.Fatalf("fetchPositions: %v", err)
	}
	want := []string{"noon-small", "evening-big", "later", "undated"}
	for i, w := range want {
		if got[i].Title != w {
			t.Errorf("position %d = %q, want %q", i, got[i].Title, w)
		}
	}

	// Second cycle must be served from cache, including the market gamma
	// didn't know about.
	if _, err := fetchPositions("0xtest"); err != nil {
		t.Fatalf("second fetchPositions: %v", err)
	}
	if gammaCalls != 1 {
		t.Errorf("gamma calls = %d, want 1", gammaCalls)
	}
}

func TestBinanceDue(t *testing.T) {
	bnProbe = map[string]time.Time{}
	t.Cleanup(func() { bnProbe = map[string]time.Time{} })

	if !binanceDue("never-probed") {
		t.Error("unknown token: want a probe")
	}
	bnProbe["flare-networks"] = time.Now().Add(bnProbeEvery)
	if binanceDue("flare-networks") {
		t.Error("recently 400'd: want the probe skipped")
	}
	bnProbe["flare-networks"] = time.Now().Add(-time.Minute)
	if !binanceDue("flare-networks") {
		t.Error("probe window elapsed: want a re-probe")
	}
}

func TestShortURLDropsWallet(t *testing.T) {
	in := "https://data-api.polymarket.com/positions?user=0xdeadbeef&limit=100"
	if got := shortURL(in); got != "data-api.polymarket.com/positions" {
		t.Errorf("shortURL = %q, want the host+path with no query", got)
	}
}

func TestDefaultConfigHasNoWallet(t *testing.T) {
	if got := defaultConfig().Wallet; got != "" {
		t.Errorf("default wallet = %q, want empty", got)
	}
}

func TestEnvExampleIsPolymarketCore(t *testing.T) {
	b, err := os.ReadFile(".env.example")
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "POLYDISPLAY_ASSETS=") {
			raw = strings.Trim(strings.TrimPrefix(line, "POLYDISPLAY_ASSETS="), `"'`)
			break
		}
	}
	if raw == "" {
		t.Fatal(".env.example missing POLYDISPLAY_ASSETS")
	}
	got := parseAssets(raw)
	want := []string{"BTC", "ETH", "SOL", "XRP"}
	if len(got) != len(want) {
		t.Fatalf("example assets=%d (%v), want the 4 Polymarket names %v", len(got), got, want)
	}
	for i, sym := range want {
		if got[i].Sym != sym {
			t.Errorf("example[%d]=%q, want %q", i, got[i].Sym, sym)
		}
		if got[i].ID == "" {
			t.Errorf("example[%d] %s missing coingecko id", i, sym)
		}
	}
}

func TestDefaultConfigHasNoCoins(t *testing.T) {
	if n := len(defaultConfig().Coins); n != 0 {
		t.Errorf("default coins=%d, want none (list comes from POLYDISPLAY_ASSETS)", n)
	}
}

func TestParseAssets(t *testing.T) {
	got := parseAssets("BTC:Bitcoin:bitcoin, ETH:ethereum, TRUMP:Official Trump:official-trump, WIF:dogwifhat:dogwifcoin:WIFUSDT, skipme, :noid")
	want := []Coin{
		{Sym: "BTC", Name: "Bitcoin", ID: "bitcoin"},
		{Sym: "ETH", Name: "ETH", ID: "ethereum"},
		{Sym: "TRUMP", Name: "Official Trump", ID: "official-trump"},
		{Sym: "WIF", Name: "dogwifhat", ID: "dogwifcoin", Bn: "WIFUSDT"},
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]=%+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListenPort(t *testing.T) {
	t.Setenv("POLYDISPLAY_PORT", "")
	if p := listenPort(0); p != 8080 {
		t.Errorf("empty env, no file: %d, want 8080", p)
	}
	if p := listenPort(9090); p != 9090 {
		t.Errorf("empty env, file 9090: %d", p)
	}
	t.Setenv("POLYDISPLAY_PORT", "3000")
	if p := listenPort(9090); p != 3000 {
		t.Errorf("env 3000 should win over file, got %d", p)
	}
	t.Setenv("POLYDISPLAY_PORT", "nope")
	if p := listenPort(0); p != 8080 {
		t.Errorf("bad env: %d, want 8080", p)
	}
}

func TestCoinsFromEnv(t *testing.T) {
	t.Setenv("POLYDISPLAY_ASSETS", "SOL:Solana:solana,XRP:XRP:ripple")
	got := coinsFromEnv()
	if len(got) != 2 || got[0].Sym != "SOL" || got[1].ID != "ripple" {
		t.Fatalf("coinsFromEnv=%+v", got)
	}
}

func TestLoadEnvFileDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.env"
	if err := os.WriteFile(path, []byte("POLYDISPLAY_ASSETS=BTC:bitcoin\nCG_DEMO_KEY=fromfile\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CG_DEMO_KEY", "fromproc")
	t.Setenv("POLYDISPLAY_ASSETS", "")
	loadEnvFile(path)
	if os.Getenv("CG_DEMO_KEY") != "fromproc" {
		t.Errorf("CG_DEMO_KEY=%q, want process env to win", os.Getenv("CG_DEMO_KEY"))
	}
	if os.Getenv("POLYDISPLAY_ASSETS") != "BTC:bitcoin" {
		t.Errorf("POLYDISPLAY_ASSETS=%q, want value from file", os.Getenv("POLYDISPLAY_ASSETS"))
	}
}

func TestRefreshFastSkipsPolymarketWithoutWallet(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		t.Errorf("unexpected call %s", r.URL.Path)
	}))
	defer srv.Close()

	origBase := polyBase
	polyBase = srv.URL
	cfg = Config{Wallet: "  ", CandleDays: 1, Sort: "az"}
	polyNextAt, polyBackoff, polyWallet = time.Time{}, 0, "stale"
	lastPositions = []Position{{Title: "leftover"}}
	lastActivity = []Act{{Title: "leftover"}}
	t.Cleanup(func() {
		polyBase = origBase
		cfg, state = Config{}, State{}
		polyNextAt, polyBackoff, polyWallet = time.Time{}, 0, ""
		lastPositions, lastActivity = nil, nil
	})

	refreshFast()
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("called Polymarket %d times with no wallet, want 0", hits)
	}
	if len(state.Positions) != 0 || len(state.Activity) != 0 {
		t.Errorf("positions=%d activity=%d, want both empty", len(state.Positions), len(state.Activity))
	}
	if state.Wallet != "" || state.Note != "" {
		t.Errorf("wallet=%q note=%q, want both empty", state.Wallet, state.Note)
	}
}

func TestLicenseHeaders(t *testing.T) {
	out, err := exec.Command("git", "ls-files").Output()
	if err != nil {
		t.Fatal(err)
	}
	notice := "Copyright 2026ff novatechflow (Alexander Alten)"
	for _, path := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if !strings.Contains(string(b), notice) {
			t.Errorf("%s: missing %q", path, notice)
		}
	}
}

func TestInstallScriptSyntax(t *testing.T) {
	for _, sh := range []string{"install.sh"} {
		out, err := exec.Command("bash", "-n", sh).CombinedOutput()
		if err != nil {
			t.Errorf("%s: bash -n: %v\n%s", sh, err, out)
		}
	}
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"POLYMARKET_WALLET",
		"darwin) install_macos",
		"linux) install_linux",
		"https://go.dev/dl/",
		".env.example",
		"POLYDISPLAY_ASSETS",
		"POLYDISPLAY_EXTRA_ASSETS",
		"api.coingecko.com/api/v3/search",
		"api.binance.com/api/v3/ticker/24hr",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install.sh missing %q", want)
		}
	}
	if strings.Contains(s, "install_windows") {
		t.Error("install.sh should not install a Windows service")
	}
}

func TestLiveConfigIsGitignored(t *testing.T) {
	out, err := exec.Command("git", "check-ignore", "-v", "config.json").Output()
	if err != nil {
		t.Fatalf("config.json must be gitignored: %v", err)
	}
	if !strings.Contains(string(out), "config.json") {
		t.Errorf("check-ignore: %s", out)
	}
	if _, err := exec.Command("git", "ls-files", "--error-unmatch", "config.json").Output(); err == nil {
		t.Error("config.json is tracked; it must not be")
	}
}

func TestAutoThemeFollowsColorScheme(t *testing.T) {
	b, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "prefers-color-scheme:light") && !strings.Contains(s, "prefers-color-scheme: light") {
		t.Error("index.html missing prefers-color-scheme light palette")
	}
	if !strings.Contains(s, "--bg:#0b0e13") {
		t.Error("dark default palette missing")
	}
	if !strings.Contains(s, `col=up?"var(--green)":"var(--red)"`) {
		t.Error("candles must use theme variables, not hardcoded colors")
	}
}

func TestNoAppCacheManifest(t *testing.T) {
	b, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `manifest=`) {
		t.Error("index.html must not set an AppCache manifest")
	}
	if _, err := os.Stat("polydisplay.appcache"); err == nil {
		t.Error("must not ship an .appcache file")
	}
}

func TestIndexExplainsMissingWallet(t *testing.T) {
	b, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "No Polymarket wallet") {
		t.Error("index.html missing the no-wallet empty state")
	}
	if !strings.Contains(s, "paste an address") {
		t.Error("index.html missing how to add a wallet")
	}
}
