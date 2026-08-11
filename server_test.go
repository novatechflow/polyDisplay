package main

import (
	"net/http"
	"net/http/httptest"
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

func TestShortURLDropsWallet(t *testing.T) {
	in := "https://data-api.polymarket.com/positions?user=0xdeadbeef&limit=100"
	if got := shortURL(in); got != "data-api.polymarket.com/positions" {
		t.Errorf("shortURL = %q, want the host+path with no query", got)
	}
}
