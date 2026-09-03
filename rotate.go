// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	logFileName = "polydisplay.log"
	logKeepDays = 5
)

var logNow = func() time.Time { return time.Now() }

type dailyLog struct {
	mu   sync.Mutex
	dir  string
	keep int
	day  string
	f    *os.File
}

func openDailyLog(dir string, keep int) (*dailyLog, error) {
	if dir == "" {
		dir = "."
	}
	if keep < 1 {
		keep = logKeepDays
	}
	d := &dailyLog{dir: dir, keep: keep}
	if err := d.reopen(logNow()); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *dailyLog) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := logNow()
	day := now.Format("2006-01-02")
	if d.f == nil || day != d.day {
		if err := d.reopen(now); err != nil {
			return 0, err
		}
	}
	return d.f.Write(p)
}

func (d *dailyLog) reopen(now time.Time) error {
	day := now.Format("2006-01-02")
	path := filepath.Join(d.dir, logFileName)
	if d.f != nil {
		oldDay := d.day
		d.f.Close()
		d.f = nil
		if oldDay != "" && oldDay != day {
			_ = os.Rename(path, path+"."+oldDay)
		}
	} else if err := d.rollIfStale(path, now); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	d.f = f
	d.day = day
	d.prune(now)
	return nil
}

func (d *dailyLog) rollIfStale(path string, now time.Time) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	fileDay := st.ModTime().In(now.Location()).Format("2006-01-02")
	today := now.Format("2006-01-02")
	if fileDay == today {
		return nil
	}
	return os.Rename(path, path+"."+fileDay)
}

func (d *dailyLog) prune(now time.Time) {
	cutoff := now.AddDate(0, 0, -d.keep).Format("2006-01-02")
	ents, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	prefix := logFileName + "."
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		day := strings.TrimPrefix(name, prefix)
		if len(day) != 10 || day < cutoff {
			os.Remove(filepath.Join(d.dir, name))
		}
	}
}

func setupLogging() {
	w, err := openDailyLog(".", logKeepDays)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, w))
}
