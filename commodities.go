// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
package main

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

// Yahoo futures symbols are curated so display units are explicit and stable.
// Quotes identify the current contract; this is not a spot commodity feed.
type commodity struct {
	Coin
	Ticker, Unit string
}

var commodityCatalog = []commodity{
	{Coin{Sym: "GOLD", Name: "Gold futures", ID: "yahoo:GC=F", Kind: "commodity"}, "GC=F", "troy oz"},
	{Coin{Sym: "SILVER", Name: "Silver futures", ID: "yahoo:SI=F", Kind: "commodity"}, "SI=F", "troy oz"},
	{Coin{Sym: "COPPER", Name: "Copper futures", ID: "yahoo:HG=F", Kind: "commodity"}, "HG=F", "lb"},
	{Coin{Sym: "WTI", Name: "WTI crude oil futures", ID: "yahoo:CL=F", Kind: "commodity"}, "CL=F", "barrel"},
	{Coin{Sym: "BRENT", Name: "Brent crude oil futures", ID: "yahoo:BZ=F", Kind: "commodity"}, "BZ=F", "barrel"},
	{Coin{Sym: "NATGAS", Name: "Natural gas futures", ID: "yahoo:NG=F", Kind: "commodity"}, "NG=F", "MMBtu"},
}
var yahooBase = "https://query1.finance.yahoo.com"
var commodityQuotes = map[string]commodityQuote{} // guarded by mu, updated on slow cadence

type commodityQuote struct {
	Price    float64
	Change   *float64
	Time     int64
	Contract string
}

func findCommodity(id string) (commodity, bool) {
	for _, c := range commodityCatalog {
		if c.ID == id {
			return c, true
		}
	}
	return commodity{}, false
}

func cryptoCoins(coins []Coin) []Coin {
	out := []Coin{}
	for _, c := range coins {
		if c.Kind != "commodity" {
			out = append(out, c)
		}
	}
	return out
}

func cryptoChange(price float64, data []Candle) *float64 {
	if price <= 0 || len(data) == 0 || data[0][1] <= 0 {
		return nil
	}
	v := candleChange24(price, data)
	return &v
}

func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func fetchCommodity(c Coin, days int) ([]Candle, commodityQuote, error) {
	asset, ok := findCommodity(c.ID)
	if !ok {
		return nil, commodityQuote{}, fmt.Errorf("unsupported commodity")
	}
	interval := "1h"
	if days == 1 {
		interval = "15m"
	}
	now := time.Now()
	params := url.Values{"interval": {interval}, "period1": {fmt.Sprint(now.Add(-time.Duration(days) * 24 * time.Hour).Unix())}, "period2": {fmt.Sprint(now.Unix())}}
	var raw struct {
		Chart struct {
			Error *struct {
				Description string `json:"description"`
			} `json:"error"`
			Result []struct {
				Meta struct {
					Price    float64  `json:"regularMarketPrice"`
					Previous *float64 `json:"previousClose"`
					Time     int64    `json:"regularMarketTime"`
					Name     string   `json:"shortName"`
					Currency string   `json:"currency"`
				} `json:"meta"`
				Times      []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Open  []*float64 `json:"open"`
						High  []*float64 `json:"high"`
						Low   []*float64 `json:"low"`
						Close []*float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := getJSON(yahooBase+"/v8/finance/chart/"+url.PathEscape(asset.Ticker)+"?"+params.Encode(), &raw, map[string]string{"User-Agent": "Mozilla/5.0"}); err != nil {
		return nil, commodityQuote{}, err
	}
	if raw.Chart.Error != nil || len(raw.Chart.Result) == 0 {
		return nil, commodityQuote{}, fmt.Errorf("Yahoo quote unavailable")
	}
	r := raw.Chart.Result[0]
	if r.Meta.Currency != "USD" || !finitePositive(r.Meta.Price) || r.Meta.Time <= 0 {
		return nil, commodityQuote{}, fmt.Errorf("invalid Yahoo quote")
	}
	quote := commodityQuote{Price: r.Meta.Price, Time: r.Meta.Time, Contract: r.Meta.Name}
	// previousClose is the previous session reference, independent of chart range.
	// chartPreviousClose is deliberately not used: it can refer to the range start.
	if r.Meta.Previous != nil && finitePositive(*r.Meta.Previous) {
		v := (quote.Price / *r.Meta.Previous - 1) * 100
		quote.Change = &v
	}
	out := []Candle{}
	if len(r.Indicators.Quote) > 0 {
		q := r.Indicators.Quote[0]
		for i, ts := range r.Times {
			if i >= len(q.Open) || i >= len(q.High) || i >= len(q.Low) || i >= len(q.Close) {
				continue
			}
			if q.Open[i] == nil || q.High[i] == nil || q.Low[i] == nil || q.Close[i] == nil {
				continue
			}
			o, h, l, c := *q.Open[i], *q.High[i], *q.Low[i], *q.Close[i]
			if !finitePositive(o) || !finitePositive(h) || !finitePositive(l) || !finitePositive(c) || l > h || o < l || o > h || c < l || c > h {
				continue
			}
			out = append(out, Candle{float64(ts) * 1000, o, h, l, c})
		}
	}
	// Session breaks remain gaps; no continuous-market trend badge is applied.
	return trimCandles(out, len(out)), quote, nil
}

func searchCommodities(q string) []struct{ Sym, Name, ID, Kind string } {
	out := []struct{ Sym, Name, ID, Kind string }{}
	q = strings.ToLower(strings.TrimSpace(q))
	for _, c := range commodityCatalog {
		if strings.Contains(strings.ToLower(c.Sym+" "+c.Name+" "+c.Ticker), q) {
			out = append(out, struct{ Sym, Name, ID, Kind string }{c.Sym, c.Name, c.ID, "commodity"})
		}
	}
	return out
}

func validWallet(wallet string) bool {
	if len(wallet) != 42 || !strings.HasPrefix(wallet, "0x") {
		return false
	}
	for _, r := range wallet[2:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
