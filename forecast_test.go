package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchForecast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("tilt") != "90" || q.Get("azimuth") != "-22" || q.Get("latitude") != "50.0800" {
			t.Errorf("query: %v", q)
		}
		w.Write([]byte(`{"daily":{"time":["2026-10-09","2026-10-10"],"temperature_2m_max":[14.2,9],"temperature_2m_min":[3,-1],"sunshine_duration":[36000,0]},
"hourly":{"time":["2026-10-09T11:00","2026-10-09T12:00","2026-10-10T12:00","2026-10-10T13:00"],"global_tilted_irradiance":[600,700,50,null]}}`))
	}))
	defer srv.Close()
	days, err := fetchForecast(srv.URL, 50.08, 14.42, -22)
	if err != nil {
		t.Fatal(err)
	}
	m0, m1 := days[0].Metrics, days[1].Metrics
	if days[1].Date != "2026-10-10" || m0["solar"] != 1.3 || m0["sunshine"] != 10 || m1["solar"] != 0.05 || m1["tmin"] != -1 {
		t.Fatalf("days: %+v", days)
	}
}

func day(date string, solar, tmax float64) Day {
	return Day{Date: date, Metrics: map[string]float64{"solar": solar, "tmax": tmax}}
}

func TestStep(t *testing.T) {
	c, pushed := fakeFenix(t)
	cfg := Config{AutoAdjust: true, Rules: []Rule{
		{ID: "sunny", Day: "tomorrow", Metric: "solar", Op: ">", Value: 3, Message: "Sunny tomorrow", Action: &Action{Zones: []string{"OBYVAK"}, Mode: "eco"}},
		{ID: "cold", Day: "today", Metric: "tmax", Op: "<", Value: 0, Message: "Freezing"},
	}}
	var st State
	var sent []string
	send := func(m string) error { sent = append(sent, m); return nil }
	run := func(days ...Day) {
		t.Helper()
		sent = nil
		if err := step(c, cfg, &st, days, send); err != nil {
			t.Fatal(err)
		}
	}

	run(day("2026-10-09", 1, 5), day("2026-10-10", 4, 8))
	if len(sent) != 1 || !strings.Contains(sent[0], "Will apply 2026-10-10") || len(st.Pending) != 1 || len(*pushed) != 0 {
		t.Fatalf("fire: sent=%q st=%+v", sent, st)
	}

	run(day("2026-10-09", 1, 5), day("2026-10-10", 4, 8))
	if len(sent) != 0 || len(st.Pending) != 1 {
		t.Fatalf("dedupe: sent=%q st=%+v", sent, st)
	}

	run(day("2026-10-10", 4, -2), day("2026-10-11", 0, 8))
	if len(sent) != 2 || !strings.HasPrefix(sent[0], "Applied sunny") || !strings.HasPrefix(sent[1], "Freezing") {
		t.Fatalf("apply: sent=%q", sent)
	}
	if len(*pushed) != 1 || (*pushed)[0].Get("query[gv_mode]") != "3" || len(st.Pending) != 0 || len(st.Reverts) != 1 || st.Reverts[0].Modes["gv_mode"] != "16" {
		t.Fatalf("apply: pushed=%v st=%+v", *pushed, st)
	}

	run(day("2026-10-11", 0, 8), day("2026-10-12", 0, 8))
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "Reverted OBYVAK") || len(*pushed) != 2 || (*pushed)[1].Get("query[gv_mode]") != "16" || (*pushed)[1].Get("query[nv_mode]") != "16" {
		t.Fatalf("revert: sent=%q pushed=%v", sent, *pushed)
	}
	if len(st.Reverts) != 0 || len(st.Fired) != 0 {
		t.Fatalf("revert state: %+v", st)
	}

	cfg.AutoAdjust = false
	run(day("2026-10-11", 0, 8), day("2026-10-12", 5, 8))
	if len(sent) != 1 || strings.Contains(sent[0], "apply") || len(st.Pending) != 0 {
		t.Fatalf("notify only: sent=%q st=%+v", sent, st)
	}
}
