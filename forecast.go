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
	"sort"
	"strconv"
	"strings"
	"time"
)

const glassGain = 0.6

type Config struct {
	Lat        *float64        `json:"lat"`
	Lon        *float64        `json:"lon"`
	NtfyTopic  string          `json:"ntfy_topic"`
	Lang       string          `json:"lang"`
	AutoAdjust bool            `json:"auto_adjust"`
	Sun        SunConfig       `json:"sun"`
	Rooms      map[string]Room `json:"rooms"`
	Rules      []Rule          `json:"rules"`
	Away       *Away           `json:"away,omitempty"`
	AwayWarmup *int            `json:"away_warmup_hours,omitempty"`
}

type Away struct {
	Until string                       `json:"until"`
	Prev  map[string]map[string]string `json:"prev"`
}

type SunConfig struct {
	LeadHours   int     `json:"lead_hours"`
	Share       float64 `json:"share"`
	HeadsUpHour int     `json:"heads_up_hour"`
}

type Room struct {
	Sun     bool     `json:"sun"`
	Windows []Window `json:"windows"`
}

type Window struct {
	Azimuth float64 `json:"azimuth"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Count   int     `json:"count"`
}

type Rule struct {
	ID      string  `json:"id"`
	Day     string  `json:"day"`
	Metric  string  `json:"metric"`
	Op      string  `json:"op"`
	Value   float64 `json:"value"`
	Message string  `json:"message"`
}

type Day struct {
	Date    string
	Metrics map[string]float64
}

type Forecast struct {
	Days []Day
	Now  string
	At   time.Time
	GTI  map[float64]map[string]float64
}

type SunState struct {
	Date    string            `json:"date"`
	Prev    map[string]string `json:"prev,omitempty"`
	Applied bool              `json:"applied,omitempty"`
	Done    bool              `json:"done,omitempty"`
}

type State struct {
	Fired   map[string][]string  `json:"fired"`
	HeadsUp string               `json:"heads_up,omitempty"`
	LastRun string               `json:"last_run"`
	Sun     map[string]*SunState `json:"sun"`
	Away    string               `json:"away_restored,omitempty"`
}

type Span struct{ Start, SunStart, SunEnd int }

var dayIndex = map[string]int{"today": 0, "tomorrow": 1}

var metricUnits = map[string]string{"tmax": "°C", "tmin": "°C", "sunshine": "h"}

var messages = map[string]map[string]string{
	"en": {
		"title":       "Heating",
		"back":        "%s back to its normal mode (%s)",
		"newDay":      "new day",
		"sunOff":      "sun control turned off",
		"lessSun":     "less sun than forecast",
		"sunGone":     "sun is gone",
		"headsUp":     "Tomorrow: %s",
		"headsUpRoom": "%s sun %02d–%02d h, eco from %02d h",
		"autoOff":     " (auto-adjust is off)",
		"apply":       "☀ %s → eco until %02d:00, sun expected %02d–%02d h",
		"manual":      "%s was changed by hand, leaving it alone today",
		"rule":        "%s (%s %s %.1f %s)",
		"today":       "today",
		"tomorrow":    "tomorrow",
		"tmax":        "max",
		"tmin":        "min",
		"sunshine":    "sunshine",
		"welcome":     "Heating restored, welcome home",
	},
	"cs": {
		"title":       "Topení",
		"back":        "%s zpět v původním režimu (%s)",
		"newDay":      "nový den",
		"sunOff":      "řízení podle slunce vypnuto",
		"lessSun":     "méně slunce, než se čekalo",
		"sunGone":     "slunce už nesvítí",
		"headsUp":     "Zítra: %s",
		"headsUpRoom": "%s slunce %02d–%02d h, útlum od %02d h",
		"autoOff":     " (automatika je vypnutá)",
		"apply":       "☀ %s → útlum do %02d:00, slunce čekáme %02d–%02d h",
		"manual":      "%s někdo přepnul ručně, dnes ho nechávám být",
		"rule":        "%s (%s %s %.1f %s)",
		"today":       "dnes",
		"tomorrow":    "zítra",
		"tmax":        "max.",
		"tmin":        "min.",
		"sunshine":    "slunce",
		"welcome":     "Topení je zpátky v normálu, vítej doma",
	},
}

func tr(lang, key string) string {
	if m, ok := messages[lang]; ok {
		return m[key]
	}
	return messages["en"][key]
}

func (sc SunConfig) withDefaults() SunConfig {
	if sc == (SunConfig{}) {
		return SunConfig{LeadHours: 3, Share: 50, HeadsUpHour: 18}
	}
	return sc
}

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
	return nil
}

func (r Rule) matches(v float64) bool {
	if r.Op == ">" {
		return v > r.Value
	}
	return v < r.Value
}

func sunWindow(r Room, powerW float64, gti map[float64]map[string]float64, date string, sc SunConfig) (Span, bool) {
	first, last := -1, -1
	for h := 0; h < 24; h++ {
		t := fmt.Sprintf("%sT%02d:00", date, h)
		gain := 0.0
		for _, w := range r.Windows {
			gain += w.Width * w.Height * float64(max(w.Count, 1)) * gti[w.Azimuth][t] * glassGain
		}
		if powerW > 0 && gain >= sc.Share/100*powerW {
			if first < 0 {
				first = h
			}
			last = h
		}
	}
	if first < 0 {
		return Span{}, false
	}
	return Span{Start: max(0, first-sc.LeadHours), SunStart: first, SunEnd: last}, true
}

func getJSON(u string, v any) error {
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("forecast: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func fetchForecast(base string, lat, lon float64, azimuths []float64) (Forecast, error) {
	query := func(extra url.Values) string {
		q := url.Values{
			"latitude":      {strconv.FormatFloat(lat, 'f', 4, 64)},
			"longitude":     {strconv.FormatFloat(lon, 'f', 4, 64)},
			"timezone":      {"auto"},
			"forecast_days": {"2"},
		}
		for k, v := range extra {
			q[k] = v
		}
		return base + "?" + q.Encode()
	}
	var r struct {
		Current struct {
			Time string `json:"time"`
		} `json:"current"`
		Daily struct {
			Time     []string  `json:"time"`
			Tmax     []float64 `json:"temperature_2m_max"`
			Tmin     []float64 `json:"temperature_2m_min"`
			Sunshine []float64 `json:"sunshine_duration"`
		} `json:"daily"`
	}
	if err := getJSON(query(url.Values{"daily": {"temperature_2m_max,temperature_2m_min,sunshine_duration"}, "current": {"temperature_2m"}}), &r); err != nil {
		return Forecast{}, err
	}
	d := r.Daily
	if len(d.Time) < 2 || len(d.Tmax) < 2 || len(d.Tmin) < 2 || len(d.Sunshine) < 2 || len(r.Current.Time) < 13 {
		return Forecast{}, errors.New("forecast: incomplete response")
	}
	fc := Forecast{Now: r.Current.Time, GTI: map[float64]map[string]float64{}}
	for i := range 2 {
		fc.Days = append(fc.Days, Day{Date: d.Time[i], Metrics: map[string]float64{"tmax": d.Tmax[i], "tmin": d.Tmin[i], "sunshine": d.Sunshine[i] / 3600}})
	}
	for _, az := range azimuths {
		var h struct {
			Hourly struct {
				Time []string   `json:"time"`
				GTI  []*float64 `json:"global_tilted_irradiance"`
			} `json:"hourly"`
		}
		if err := getJSON(query(url.Values{"hourly": {"global_tilted_irradiance"}, "tilt": {"90"}, "azimuth": {strconv.FormatFloat(az, 'f', 0, 64)}}), &h); err != nil {
			return Forecast{}, err
		}
		if len(h.Hourly.Time) != len(h.Hourly.GTI) {
			return Forecast{}, errors.New("forecast: incomplete hourly response")
		}
		m := map[string]float64{}
		for i, t := range h.Hourly.Time {
			if g := h.Hourly.GTI[i]; g != nil {
				m[t] = *g
			}
		}
		fc.GTI[az] = m
	}
	return fc, nil
}

func notify(base, topic, title, msg string) error {
	fmt.Println("notify:", msg)
	if topic == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodPost, base+"/"+url.PathEscape(topic)+"?"+url.Values{"title": {title}}.Encode(), strings.NewReader(msg))
	if err != nil {
		return err
	}
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

func title(label string) string {
	if label == "" {
		return label
	}
	return strings.ToUpper(label[:1]) + strings.ToLower(label[1:])
}

func step(c *Client, cfg Config, st *State, fc Forecast, send func(string) error) error {
	today, tomorrow := fc.Days[0].Date, fc.Days[1].Date
	hour, _ := strconv.Atoi(fc.Now[11:13])
	sc := cfg.Sun.withDefaults()
	T := func(k string) string { return tr(cfg.Lang, k) }
	var errs []error
	if st.Fired == nil {
		st.Fired = map[string][]string{}
	}
	if st.Sun == nil {
		st.Sun = map[string]*SunState{}
	}

	away := cfg.Away != nil && st.Away != cfg.Away.Until
	if away {
		if back, err := backHome(c, cfg, st, sc, fc.At); err != nil {
			errs = append(errs, err)
		} else if back {
			away = false
			errs = append(errs, send(T("welcome")))
		}
	}

	var labels []string
	for l := range st.Sun {
		labels = append(labels, l)
	}
	for l, r := range cfg.Rooms {
		if r.Sun && len(r.Windows) > 0 && st.Sun[l] == nil {
			labels = append(labels, l)
		}
	}
	if away {
		labels = nil
	}
	sort.Strings(labels)

	var zones []Zone
	if len(labels) > 0 {
		var err error
		if zones, err = c.Zones(); err != nil {
			return err
		}
	}
	var headsUp []string
	for _, label := range labels {
		i := slices.IndexFunc(zones, func(z Zone) bool { return z.Label == label })
		if i < 0 {
			errs = append(errs, fmt.Errorf("unknown zone %q", label))
			continue
		}
		d := zones[i].Device
		power, _ := strconv.ParseFloat(d.str("puissance_app"), 64)
		room := cfg.Rooms[label]
		enabled := room.Sun && len(room.Windows) > 0
		restore := func(s *SunState, why string) {
			if err := c.Write(d.str("id_device"), s.Prev); err != nil {
				errs = append(errs, err)
				return
			}
			s.Done = true
			errs = append(errs, send(fmt.Sprintf(T("back"), title(label), T(why))))
		}

		s := st.Sun[label]
		if s != nil && s.Date != today {
			if s.Applied && !s.Done {
				restore(s, "newDay")
				if !s.Done {
					continue
				}
			}
			s = nil
		}
		if !enabled && (s == nil || !s.Applied || s.Done) {
			delete(st.Sun, label)
			continue
		}
		if s == nil {
			s = &SunState{Date: today}
			st.Sun[label] = s
		}

		if enabled && hour >= sc.HeadsUpHour && st.HeadsUp != today {
			if w, ok := sunWindow(room, power, fc.GTI, tomorrow, sc); ok {
				headsUp = append(headsUp, fmt.Sprintf(T("headsUpRoom"), title(label), w.SunStart, w.SunEnd+1, w.Start))
			}
		}

		if s.Done {
			continue
		}
		w, ok := sunWindow(room, power, fc.GTI, today, sc)
		ok = ok && enabled
		gv := d.str("gv_mode")
		if !s.Applied {
			if !cfg.AutoAdjust || !ok || hour < w.Start || hour > w.SunEnd {
				continue
			}
			if slices.Contains([]string{modes["eco"], modes["frost"], modes["off"], modes["boost"], modes["manual"]}, gv) {
				s.Done = true
				continue
			}
			s.Prev = map[string]string{}
			for _, k := range []string{"gv_mode", "nv_mode"} {
				if v, has := d[k]; has && v != nil {
					s.Prev[k] = fmt.Sprint(v)
				}
			}
			eco := modes["eco"]
			if err := c.Write(d.str("id_device"), map[string]string{"gv_mode": eco, "nv_mode": eco}); err != nil {
				errs = append(errs, err)
				continue
			}
			s.Applied = true
			errs = append(errs, send(fmt.Sprintf(T("apply"), title(label), w.SunEnd+1, w.SunStart, w.SunEnd+1)))
			continue
		}
		switch {
		case gv != modes["eco"]:
			s.Done = true
			errs = append(errs, send(fmt.Sprintf(T("manual"), title(label))))
		case !enabled:
			restore(s, "sunOff")
		case !ok:
			restore(s, "lessSun")
		case hour > w.SunEnd:
			restore(s, "sunGone")
		}
	}
	if hour >= sc.HeadsUpHour && st.HeadsUp != today {
		st.HeadsUp = today
		if len(headsUp) > 0 {
			msg := fmt.Sprintf(T("headsUp"), strings.Join(headsUp, "; "))
			if !cfg.AutoAdjust {
				msg += T("autoOff")
			}
			errs = append(errs, send(msg))
		}
	}

	for _, r := range cfg.Rules {
		if err := r.validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		day := fc.Days[dayIndex[r.Day]]
		v := day.Metrics[r.Metric]
		if slices.Contains(st.Fired[day.Date], r.ID) || !r.matches(v) {
			continue
		}
		st.Fired[day.Date] = append(st.Fired[day.Date], r.ID)
		errs = append(errs, send(fmt.Sprintf(T("rule"), r.Message, T(r.Day), T(r.Metric), v, metricUnits[r.Metric])))
	}
	for date := range st.Fired {
		if date < today {
			delete(st.Fired, date)
		}
	}
	return errors.Join(errs...)
}

func backHome(c *Client, cfg Config, st *State, sc SunConfig, now time.Time) (bool, error) {
	until, err := time.Parse(time.RFC3339, cfg.Away.Until)
	if err != nil {
		return false, fmt.Errorf("away: %w", err)
	}
	warm := sc.LeadHours
	if cfg.AwayWarmup != nil {
		warm = *cfg.AwayWarmup
	}
	if now.Before(until.Add(-time.Duration(warm) * time.Hour)) {
		return false, nil
	}
	zones, err := c.Zones()
	if err != nil {
		return false, err
	}
	var errs []error
	for _, z := range zones {
		p := cfg.Away.Prev[z.Label]
		if p["gv_mode"] == "" || p["gv_mode"] == modes["eco"] || z.Device.str("gv_mode") != modes["eco"] {
			continue
		}
		errs = append(errs, c.Write(z.Device.str("id_device"), p))
	}
	if err := errors.Join(errs...); err != nil {
		return false, err
	}
	st.Away = cfg.Away.Until
	st.Sun = map[string]*SunState{}
	return true, nil
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
	var az []float64
	for _, r := range cfg.Rooms {
		for _, w := range r.Windows {
			if r.Sun && !slices.Contains(az, w.Azimuth) {
				az = append(az, w.Azimuth)
			}
		}
	}
	fc, err := fetchForecast(env("FORECAST_URL", "https://api.open-meteo.com/v1/forecast"), lat, lon, az)
	if err != nil {
		return err
	}
	fc.At = time.Now()
	fmt.Println("now", fc.Now)
	for _, d := range fc.Days {
		fmt.Printf("%s tmax=%.1f tmin=%.1f sunshine=%.1fh\n", d.Date, d.Metrics["tmax"], d.Metrics["tmin"], d.Metrics["sunshine"])
	}
	ntfy := env("NTFY_URL", "https://ntfy.sh")
	stepErr := step(c, cfg, &st, fc, func(msg string) error { return notify(ntfy, cfg.NtfyTopic, tr(cfg.Lang, "title"), msg) })
	st.LastRun = time.Now().UTC().Format(time.RFC3339)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		return err
	}
	return errors.Join(stepErr, os.WriteFile(statePath, buf.Bytes(), 0o644))
}
