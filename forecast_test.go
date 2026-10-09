package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchForecast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("latitude") != "50.0800" || q.Get("timezone") != "auto" {
			t.Errorf("query: %v", q)
		}
		if q.Get("azimuth") == "" {
			w.Write([]byte(`{"current":{"time":"2026-10-09T14:15"},"daily":{"time":["2026-10-09","2026-10-10"],"temperature_2m_max":[14.2,9],"temperature_2m_min":[3,-1],"sunshine_duration":[36000,0]}}`))
			return
		}
		if q.Get("tilt") != "90" || q.Get("azimuth") != "-22" {
			t.Errorf("hourly query: %v", q)
		}
		w.Write([]byte(`{"hourly":{"time":["2026-10-09T11:00","2026-10-09T12:00"],"global_tilted_irradiance":[600,null]}}`))
	}))
	defer srv.Close()
	fc, err := fetchForecast(srv.URL, 50.08, 14.42, []float64{-22})
	if err != nil {
		t.Fatal(err)
	}
	if fc.Now != "2026-10-09T14:15" || fc.Days[1].Date != "2026-10-10" || fc.Days[0].Metrics["sunshine"] != 10 || fc.GTI[-22]["2026-10-09T11:00"] != 600 || len(fc.GTI[-22]) != 1 {
		t.Fatalf("forecast: %+v", fc)
	}
}

func gti(sunny map[string][]int) map[float64]map[string]float64 {
	m := map[string]float64{}
	for date, hours := range sunny {
		for _, h := range hours {
			m[fmt.Sprintf("%sT%02d:00", date, h)] = 600
		}
	}
	return map[float64]map[string]float64{-22: m}
}

var obyvak = Room{Sun: true, Windows: []Window{{Azimuth: -22, Width: 1, Height: 2.5, Count: 4}}}

func TestSunWindow(t *testing.T) {
	sc := SunConfig{}.withDefaults()
	w, ok := sunWindow(obyvak, 3400, gti(map[string][]int{"D": {10, 11, 12, 13, 14}}), "D", sc)
	if !ok || w != (Span{Start: 7, SunStart: 10, SunEnd: 14}) {
		t.Fatalf("window: %+v %v", w, ok)
	}
	if _, ok := sunWindow(obyvak, 10000, gti(map[string][]int{"D": {10}}), "D", sc); ok {
		t.Fatal("3.6 kW gain should not cover 50% of 10 kW")
	}
	if _, ok := sunWindow(obyvak, 3400, gti(nil), "D", sc); ok {
		t.Fatal("no sun")
	}
}

func TestStep(t *testing.T) {
	c, unit := fakeFenix(t)
	cfg := Config{AutoAdjust: true, Rooms: map[string]Room{"OBYVAK": obyvak}, Rules: []Rule{
		{ID: "cold", Day: "today", Metric: "tmax", Op: "<", Value: 0, Message: "Freezing"},
	}}
	var st State
	var sent []string
	sunny := map[string][]int{"2026-10-10": {10, 11, 12, 13, 14}, "2026-10-11": {9, 10, 11}}
	run := func(now string, tmax float64) {
		t.Helper()
		sent = nil
		date := now[:10]
		next := map[string]string{"2026-10-09": "2026-10-10", "2026-10-10": "2026-10-11", "2026-10-11": "2026-10-12", "2026-10-12": "2026-10-13"}[date]
		fc := Forecast{Now: now, GTI: gti(sunny), Days: []Day{
			{Date: date, Metrics: map[string]float64{"tmax": tmax}},
			{Date: next, Metrics: map[string]float64{"tmax": 10}},
		}}
		if err := step(c, cfg, &st, fc, func(m string) error { sent = append(sent, m); return nil }); err != nil {
			t.Fatal(err)
		}
	}

	run("2026-10-09T17:07", 5)
	if len(sent) != 0 {
		t.Fatalf("before heads-up hour: %q", sent)
	}
	run("2026-10-09T18:07", 5)
	if len(sent) != 1 || sent[0] != "Tomorrow: Obyvak sun 10–15 h, eco from 07 h" {
		t.Fatalf("heads-up: %q", sent)
	}
	run("2026-10-09T19:07", 5)
	if len(sent) != 0 {
		t.Fatalf("heads-up repeated: %q", sent)
	}

	run("2026-10-10T06:07", 5)
	if len(unit.pushed) != 0 {
		t.Fatalf("too early: %v", unit.pushed)
	}
	run("2026-10-10T07:07", -1)
	if unit.gv != "3" || len(sent) != 2 || !strings.HasPrefix(sent[0], "☀ Obyvak → eco until 15:00") || sent[1] != "Freezing (today max -1.0 °C)" {
		t.Fatalf("apply: gv=%s sent=%q", unit.gv, sent)
	}
	run("2026-10-10T12:07", -1)
	if len(sent) != 0 || len(unit.pushed) != 1 {
		t.Fatalf("mid window: sent=%q pushed=%d", sent, len(unit.pushed))
	}
	run("2026-10-10T15:07", -1)
	if unit.gv != "16" || unit.pushed[1].Get("query[nv_mode]") != "16" || len(sent) != 1 || !strings.Contains(sent[0], "sun is gone") {
		t.Fatalf("revert: gv=%s sent=%q", unit.gv, sent)
	}
	run("2026-10-10T16:07", -1)
	if len(unit.pushed) != 2 || len(sent) != 0 {
		t.Fatalf("after revert: %d %q", len(unit.pushed), sent)
	}

	run("2026-10-11T08:07", 5)
	if unit.gv != "3" {
		t.Fatalf("day 2 apply: %s", unit.gv)
	}
	sunny["2026-10-11"] = nil
	run("2026-10-11T09:07", 5)
	if unit.gv != "16" || len(sent) != 1 || !strings.Contains(sent[0], "less sun") {
		t.Fatalf("clouds: gv=%s sent=%q", unit.gv, sent)
	}

	sunny["2026-10-12"] = []int{10, 11}
	run("2026-10-12T08:07", 5)
	unit.gv = "0"
	run("2026-10-12T09:07", 5)
	if unit.gv != "0" || len(sent) != 1 || !strings.Contains(sent[0], "by hand") {
		t.Fatalf("manual: gv=%s sent=%q", unit.gv, sent)
	}
	run("2026-10-12T13:07", 5)
	if unit.gv != "0" || len(sent) != 0 {
		t.Fatalf("manual kept: gv=%s sent=%q", unit.gv, sent)
	}

	unit.gv = "16"
	sunny["2026-10-13"] = []int{10, 11}
	cfg.AutoAdjust = false
	n := len(unit.pushed)
	run("2026-10-13T10:07", 5)
	if len(unit.pushed) != n {
		t.Fatal("auto-adjust off must not write")
	}
}

func TestStaleRevert(t *testing.T) {
	c, unit := fakeFenix(t)
	unit.gv = "3"
	st := State{Sun: map[string]*SunState{"OBYVAK": {Date: "2026-10-09", Applied: true, Prev: map[string]string{"gv_mode": "16", "nv_mode": "16"}}}}
	cfg := Config{AutoAdjust: true, Rooms: map[string]Room{"OBYVAK": obyvak}}
	fc := Forecast{Now: "2026-10-10T00:07", GTI: gti(nil), Days: []Day{{Date: "2026-10-10"}, {Date: "2026-10-11"}}}
	var sent []string
	if err := step(c, cfg, &st, fc, func(m string) error { sent = append(sent, m); return nil }); err != nil {
		t.Fatal(err)
	}
	if unit.gv != "16" || len(sent) != 1 || st.Sun["OBYVAK"].Date != "2026-10-10" || st.Sun["OBYVAK"].Applied {
		t.Fatalf("stale: gv=%s sent=%q st=%+v", unit.gv, sent, st.Sun["OBYVAK"])
	}
}

func TestAway(t *testing.T) {
	c, unit := fakeFenix(t)
	unit.gv = "3"
	until := "2026-10-11T16:00:00.000Z"
	cfg := Config{AutoAdjust: true, Rooms: map[string]Room{"OBYVAK": obyvak}, Away: &Away{Until: until, Prev: map[string]map[string]string{"OBYVAK": {"gv_mode": "16", "nv_mode": "16"}}}}
	var st State
	var sent []string
	sunny := map[string][]int{"2026-10-11": {10, 11, 12, 13, 14}}
	run := func(at string) {
		t.Helper()
		sent = nil
		now, _ := time.Parse(time.RFC3339, at)
		fc := Forecast{Now: "2026-10-11T12:07", At: now, GTI: gti(sunny), Days: []Day{{Date: "2026-10-11"}, {Date: "2026-10-12"}}}
		if err := step(c, cfg, &st, fc, func(m string) error { sent = append(sent, m); return nil }); err != nil {
			t.Fatal(err)
		}
	}

	unit.gv = "16"
	run("2026-10-11T12:00:00Z")
	if len(unit.pushed) != 0 || len(st.Sun) != 0 || st.Away != "" || len(sent) != 0 {
		t.Fatalf("sun control must skip while away: pushed=%d sun=%v sent=%q", len(unit.pushed), st.Sun, sent)
	}

	sunny = nil
	unit.gv = "3"
	run("2026-10-11T12:59:00Z")
	if len(unit.pushed) != 0 || st.Away != "" {
		t.Fatalf("restored before warm-up: pushed=%d away=%q", len(unit.pushed), st.Away)
	}
	run("2026-10-11T13:00:00Z")
	if unit.gv != "16" || unit.pushed[0].Get("query[nv_mode]") != "16" || st.Away != until || len(sent) != 1 || sent[0] != "Heating restored, welcome home" {
		t.Fatalf("restore: gv=%s away=%q sent=%q", unit.gv, st.Away, sent)
	}

	unit.gv = "3"
	run("2026-10-11T14:00:00Z")
	if len(unit.pushed) != 1 || len(sent) != 0 {
		t.Fatalf("double restore: pushed=%d sent=%q", len(unit.pushed), sent)
	}

	st = State{}
	unit.gv = "0"
	run("2026-10-11T13:00:00Z")
	if len(unit.pushed) != 1 || unit.gv != "0" || st.Away != until {
		t.Fatalf("hand change must be kept: gv=%s pushed=%d away=%q", unit.gv, len(unit.pushed), st.Away)
	}
}

func TestCzech(t *testing.T) {
	c, _ := fakeFenix(t)
	cfg := Config{Lang: "cs", Rooms: map[string]Room{"OBYVAK": obyvak}, Rules: []Rule{{ID: "f", Day: "tomorrow", Metric: "tmin", Op: "<", Value: 0, Message: "Mráz"}}}
	var st State
	var sent []string
	fc := Forecast{Now: "2026-10-09T18:07", GTI: gti(map[string][]int{"2026-10-10": {10, 11}}), Days: []Day{
		{Date: "2026-10-09", Metrics: map[string]float64{}},
		{Date: "2026-10-10", Metrics: map[string]float64{"tmin": -2}},
	}}
	if err := step(c, cfg, &st, fc, func(m string) error { sent = append(sent, m); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []string{"Zítra: Obyvak slunce 10–12 h, útlum od 07 h (automatika je vypnutá)", "Mráz (zítra min. -2.0 °C)"}
	if strings.Join(sent, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", sent)
	}
}
