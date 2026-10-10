// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const html = fs.readFileSync('index.html', 'utf8');
const now = 1800000000000;
class FixedDate extends Date { static now() { return now; } }
const ctx = {Date: FixedDate, isFinite, fmtUsd: String, esc: String,
  p2: v => String(v).padStart(2, '0')};
// Run the real renderer and analysis helpers without booting the page's DOM.
vm.createContext(ctx);
vm.runInContext(html.slice(html.indexOf('function chartMetrics('), html.indexOf('/* ---------- auth:')), ctx);
function series(days, direction = 1) {
  const interval = (days === 1 ? 15 : days === 7 ? 60 : 240) * 60000;
  const n = days * 86400000 / interval;
  const end = Math.floor(now / interval) * interval;
  return Array.from({length: n}, (_, i) => {
    const p = 1000 + direction * i * 2;
    return [end - (n - i) * interval, p, p + 1, p - 1, p];
  });
}
for (const days of [1, 7, 14, 30]) {
  test(`${days}d badge follows displayed period despite opposite 24h data`, () => {
    const up = series(days), down24 = series(1, -1);
    const result = ctx.chartWithAxis(up, 320, 72, days, up.at(-1)[4], down24);
    assert.match(result, /class="trend up">Uptrend/);
    assert.match(result, new RegExp(`&middot; ${days === 1 ? '24h' : days + 'd'}`));
    assert.equal(ctx.periodStructure(series(days, -1), days, now).trend.label, 'Downtrend');
    assert.equal(ctx.periodStructure(series(days, 0), days, now).trend.label, 'Neutral');
  });
  test(`${days}d unfinished candle cannot change the analysis`, () => {
    const d = series(days), interval = d[1][0] - d[0][0];
    const current = [d.at(-1)[0] + interval, 1000, 100000, 1, 2];
    // Real feed limits include the current candle, replacing the oldest candle.
    const actual = ctx.periodStructure([...d.slice(1), current], days, now);
    assert.equal(actual.data.length, d.length - 1);
    assert.equal(actual.trend.label, 'Uptrend');
    assert.ok(actual.data.every(c => c[0] !== current[0]));
  });
  test(`${days}d missing, stale, short or invalid candles fail closed`, () => {
    assert.equal(ctx.periodStructure(series(days), days, now, now - 900001).trend.why, 'stale candles');
    const d = series(days), missing = d.filter((_, i) => i !== 10);
    assert.equal(ctx.periodStructure(missing, days, now).trend.label, 'Unavailable');
    assert.equal(ctx.periodStructure(d.slice(-10), days, now).trend.label, 'Unavailable');
    const stale = d.map(c => [c[0] - 86400000, ...c.slice(1)]);
    assert.equal(ctx.periodStructure(stale, days, now).trend.why, 'stale candles');
    const malformed = d.map(c => [...c]); malformed[5][2] = NaN;
    assert.equal(ctx.periodStructure(malformed, days, now).trend.label, 'Unavailable');
    const result = ctx.chartWithAxis(missing, 320, 72, days, 1000);
    assert.match(result, /Unavailable/);
    assert.doesNotMatch(result, /stroke-dasharray="3,3"/); // no misleading channels
  });
}
test('structure formula handles expanding, coiling and a round trip', () => {
  const fixture = (highs, lows) => Array.from({length: 30}, (_, i) => {
    const block = Math.floor(i / 10), h = highs[block], l = lows[block];
    // One extreme per third, narrow typical ranges elsewhere.
    const p = (h + l) / 2;
    return [i, p, i % 10 === 5 ? h : p + 0.1, i % 10 === 5 ? l : p - 0.1, p];
  });
  assert.equal(ctx.trendVerdict(fixture([110, 120, 130], [90, 80, 70])).label, 'Expanding');
  assert.equal(ctx.trendVerdict(fixture([130, 120, 110], [70, 80, 90])).label, 'Coiling');
  assert.equal(ctx.trendVerdict(fixture([110, 130, 110], [90, 110, 90])).label, 'Neutral');
});
test('swing ties keep the first plateau and require both sides', () => {
  const d = [1, 2, 5, 5, 2, 1, 2].map((p, i) => [i, p, p + 1, p - 1, p]);
  assert.deepEqual(Array.from(ctx.swingPivots(d, 2).highs), [2]);
  assert.deepEqual(Array.from(ctx.swingPivots(series(7), 2).highs), []);
});
test('candle geometry preserves elapsed time across missing intervals', () => {
  const d = [[0, 2, 3, 1, 2], [1000, 2, 3, 1, 2], [4000, 2, 3, 1, 2]];
  const m = ctx.chartMetrics(d, 2);
  const x0 = ctx.chartX(m, 0, 300), x1 = ctx.chartX(m, 1, 300), x2 = ctx.chartX(m, 2, 300);
  assert.ok(Math.abs((x2 - x1) / (x1 - x0) - 3) < 1e-10);
});
test('commodity chart preserves session gaps without crypto trend signals', () => {
  const data = [[1000,10,12,9,11],[2000,11,13,10,12],[10000,12,14,11,13]];
  const chart = ctx.commodityChart(data,320,96,1);
  assert.match(chart,/polyline/);
  assert.doesNotMatch(chart,/trend|Unavailable|stroke-dasharray/);
  const points = chart.match(/points="([^"]+)"/)[1].split(' ').map(p => p.split(',').map(Number));
  assert.ok(points[2][0]-points[1][0] > 7*(points[1][0]-points[0][0]), 'session gap must occupy elapsed time');
});
