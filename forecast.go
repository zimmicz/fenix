package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Lat           *float64 `json:"lat"`
	Lon           *float64 `json:"lon"`
	FacadeAzimuth float64  `json:"facade_azimuth"`
	NtfyTopic     string   `json:"ntfy_topic"`
	AutoAdjust    bool     `json:"auto_adjust"`
	Rules         []Rule   `json:"rules"`
}

type Rule struct {
	ID      string  `json:"id"`
	Day     string  `json:"day"`
	Metric  string  `json:"metric"`
	Op      string  `json:"op"`
	Value   float64 `json:"value"`
	Message string  `json:"message"`
	Action  *Action `json:"action,omitempty"`
}

type Action struct {
	Zones []string `json:"zones"`
	Mode  string   `json:"mode"`
}

type Day struct {
	Date    string
	Metrics map[string]float64
}

type Revert struct {
	Date  string            `json:"date"`
	Zone  string            `json:"zone"`
	Modes map[string]string `json:"modes"`
}

type Pending struct {
	Date   string `json:"date"`
	RuleID string `json:"rule_id"`
	Action Action `json:"action"`
}

type State struct {
	Fired   map[string][]string `json:"fired"`
	Pending []Pending           `json:"pending"`
	Reverts []Revert            `json:"reverts"`
}

var dayIndex = map[string]int{"today": 0, "tomorrow": 1}

var metricUnits = map[string]string{"tmax": "°C", "tmin": "°C", "sunshine": "h", "solar": "kWh/m²"}

func (r Rule) validate() error {
	if r.ID == "" {
		return errors.New("rule without id")
	}
	if _, ok := dayIndex[r.Day]; !ok {
		return fmt.Errorf("rule %s: day must be today|tomorrow", r.ID)
	}
	if _, ok := metricUnits[r.Metric]; !ok {
		return fmt.Errorf("rule %s: unknown metric %q", r.ID, r.Metric)
	}
	if r.Op != ">" && r.Op != "<" {
		return fmt.Errorf("rule %s: op must be > or <", r.ID)
	}
	if r.Action != nil {
		if _, ok := modes[r.Action.Mode]; !ok {
			return fmt.Errorf("rule %s: unknown mode %q", r.ID, r.Action.Mode)
		}
	}
	return nil
}

func (r Rule) matches(v float64) bool {
	if r.Op == ">" {
		return v > r.Value
	}
	return v < r.Value
}

func fetchForecast(base string, lat, lon, azimuth float64) ([]Day, error) {
	q := url.Values{
		"latitude":      {strconv.FormatFloat(lat, 'f', 4, 64)},
		"longitude":     {strconv.FormatFloat(lon, 'f', 4, 64)},
		"daily":         {"temperature_2m_max,temperature_2m_min,sunshine_duration"},
		"hourly":        {"global_tilted_irradiance"},
		"tilt":          {"90"},
		"azimuth":       {strconv.FormatFloat(azimuth, 'f', 0, 64)},
		"timezone":      {"auto"},
		"forecast_days": {"2"},
	}
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(base + "?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("forecast: %s", resp.Status)
	}
	var r struct {
		Daily struct {
			Time     []string  `json:"time"`
			Tmax     []float64 `json:"temperature_2m_max"`
			Tmin     []float64 `json:"temperature_2m_min"`
			Sunshine []float64 `json:"sunshine_duration"`
		} `json:"daily"`
		Hourly struct {
			Time []string   `json:"time"`
			GTI  []*float64 `json:"global_tilted_irradiance"`
		} `json:"hourly"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	d := r.Daily
	if len(d.Time) < 2 || len(d.Tmax) < 2 || len(d.Tmin) < 2 || len(d.Sunshine) < 2 || len(r.Hourly.Time) != len(r.Hourly.GTI) {
		return nil, errors.New("forecast: incomplete response")
	}
	solar := map[string]float64{}
	for i, t := range r.Hourly.Time {
		if g := r.Hourly.GTI[i]; g != nil {
			solar[t[:10]] += *g
		}
	}
	days := make([]Day, 2)
	for i := range days {
		days[i] = Day{Date: d.Time[i], Metrics: map[string]float64{
			"tmax": d.Tmax[i], "tmin": d.Tmin[i], "sunshine": d.Sunshine[i] / 3600, "solar": solar[d.Time[i]] / 1000,
		}}
	}
	return days, nil
}

func notify(base, topic, msg string) error {
	fmt.Println("notify:", msg)
	if topic == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodPost, base+"/"+url.PathEscape(topic), strings.NewReader(msg))
	if err != nil {
		return err
	}
	req.Header.Set("Title", "Heating")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ntfy: %s", resp.Status)
	}
	return nil
}

func applyAction(c *Client, st *State, date string, a Action) error {
	zones, err := c.Zones()
	if err != nil {
		return err
	}
	for _, label := range a.Zones {
		i := slices.IndexFunc(zones, func(z Zone) bool { return z.Label == label })
		if i < 0 {
			return fmt.Errorf("unknown zone %q", label)
		}
		d := zones[i].Device
		if !slices.ContainsFunc(st.Reverts, func(r Revert) bool { return r.Zone == label }) {
			prev := map[string]string{}
			for _, k := range []string{"gv_mode", "nv_mode"} {
				if v, ok := d[k]; ok && v != nil {
					prev[k] = fmt.Sprint(v)
				}
			}
			st.Reverts = append(st.Reverts, Revert{Date: date, Zone: label, Modes: prev})
		}
		code := modes[a.Mode]
		if err := c.Write(d.str("id_device"), map[string]string{"gv_mode": code, "nv_mode": code}); err != nil {
			return err
		}
	}
	return nil
}

func step(c *Client, cfg Config, st *State, days []Day, send func(string) error) error {
	today := days[0].Date
	var errs []error

	var keep []Revert
	var reverted []string
	var zones []Zone
	for _, r := range st.Reverts {
		if r.Date >= today {
			keep = append(keep, r)
			continue
		}
		if zones == nil {
			var err error
			if zones, err = c.Zones(); err != nil {
				return err
			}
		}
		i := slices.IndexFunc(zones, func(z Zone) bool { return z.Label == r.Zone })
		if i < 0 {
			errs = append(errs, fmt.Errorf("revert: unknown zone %q", r.Zone))
			continue
		}
		if err := c.Write(zones[i].Device.str("id_device"), r.Modes); err != nil {
			errs = append(errs, err)
			keep = append(keep, r)
			continue
		}
		reverted = append(reverted, r.Zone)
	}
	st.Reverts = keep
	if len(reverted) > 0 {
		errs = append(errs, send("Reverted "+strings.Join(reverted, ", ")+" to previous mode"))
	}

	var pending []Pending
	for _, p := range st.Pending {
		switch {
		case p.Date > today:
			pending = append(pending, p)
		case p.Date == today && cfg.AutoAdjust:
			if err := applyAction(c, st, today, p.Action); err != nil {
				errs = append(errs, err)
				continue
			}
			errs = append(errs, send(fmt.Sprintf("Applied %s: %s → %s", p.RuleID, strings.Join(p.Action.Zones, ", "), p.Action.Mode)))
		}
	}
	st.Pending = pending

	if st.Fired == nil {
		st.Fired = map[string][]string{}
	}
	for _, r := range cfg.Rules {
		if err := r.validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		day := days[dayIndex[r.Day]]
		v := day.Metrics[r.Metric]
		if slices.Contains(st.Fired[day.Date], r.ID) || !r.matches(v) {
			continue
		}
		msg := fmt.Sprintf("%s (%s %s %.1f %s)", r.Message, r.Day, r.Metric, v, metricUnits[r.Metric])
		if r.Action != nil && cfg.AutoAdjust {
			a := fmt.Sprintf("%s → %s", strings.Join(r.Action.Zones, ", "), r.Action.Mode)
			if day.Date == today {
				if err := applyAction(c, st, today, *r.Action); err != nil {
					errs = append(errs, err)
					continue
				}
				msg += "\nApplied: " + a
			} else {
				st.Pending = append(st.Pending, Pending{Date: day.Date, RuleID: r.ID, Action: *r.Action})
				msg += "\nWill apply " + day.Date + ": " + a
			}
		}
		st.Fired[day.Date] = append(st.Fired[day.Date], r.ID)
		errs = append(errs, send(msg))
	}
	for date := range st.Fired {
		if date < today {
			delete(st.Fired, date)
		}
	}
	return errors.Join(errs...)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func forecast(c *Client, configPath, statePath string) error {
	var cfg Config
	if err := readJSON(configPath, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("no", configPath, "- skipping")
			return nil
		}
		return err
	}
	var st State
	if err := readJSON(statePath, &st); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lat, _ := strconv.ParseFloat(c.Lat, 64)
	lon, _ := strconv.ParseFloat(c.Lon, 64)
	if cfg.Lat != nil && cfg.Lon != nil {
		lat, lon = *cfg.Lat, *cfg.Lon
	}
	days, err := fetchForecast(env("FORECAST_URL", "https://api.open-meteo.com/v1/forecast"), lat, lon, cfg.FacadeAzimuth)
	if err != nil {
		return err
	}
	for _, d := range days {
		fmt.Printf("%s tmax=%.1f tmin=%.1f sunshine=%.1fh solar=%.2fkWh/m²\n", d.Date, d.Metrics["tmax"], d.Metrics["tmin"], d.Metrics["sunshine"], d.Metrics["solar"])
	}
	ntfy := env("NTFY_URL", "https://ntfy.sh")
	stepErr := step(c, cfg, &st, days, func(msg string) error { return notify(ntfy, cfg.NtfyTopic, msg) })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		return err
	}
	return errors.Join(stepErr, os.WriteFile(statePath, buf.Bytes(), 0o644))
}
