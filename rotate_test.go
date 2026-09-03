// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDailyLogRotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { logNow = func() time.Time { return time.Now() } })

	yesterday := time.Date(2026, 9, 2, 23, 0, 0, 0, time.Local)
	logNow = func() time.Time { return yesterday }
	w, err := openDailyLog(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("yesterday\n")); err != nil {
		t.Fatal(err)
	}

	today := time.Date(2026, 9, 3, 1, 0, 0, 0, time.Local)
	logNow = func() time.Time { return today }
	if _, err := w.Write([]byte("today\n")); err != nil {
		t.Fatal(err)
	}
	cur := filepath.Join(dir, logFileName)
	if _, err := os.Stat(cur + ".2026-09-02"); err != nil {
		t.Fatalf("expected rolled file: %v", err)
	}
	b, err := os.ReadFile(cur)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "today\n" {
		t.Errorf("current log = %q, want today only", b)
	}

	stale := cur + ".2026-08-28"
	if err := os.WriteFile(stale, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	keep := cur + ".2026-08-30"
	if err := os.WriteFile(keep, []byte("keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	w.prune(today)
	w.mu.Unlock()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("archive older than 5 days should be gone")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("archive within 5 days should remain: %v", err)
	}
}
