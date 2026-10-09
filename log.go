package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var logHeader = []string{"ts", "zone", "air_c", "comfort_c", "eco_c", "mode", "heating", "power_w", "outdoor_c"}

func outdoorC(lat, lon string) string {
	client := http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%s&longitude=%s&current=temperature_2m", lat, lon))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var r struct {
		Current struct {
			Temp *float64 `json:"temperature_2m"`
		} `json:"current"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&r) != nil || r.Current.Temp == nil {
		return ""
	}
	return strconv.FormatFloat(*r.Current.Temp, 'f', 1, 64)
}

func logRows(zones []Zone, ts, outdoor string) [][]string {
	rows := make([][]string, 0, len(zones))
	for _, z := range zones {
		d := z.Device
		rows = append(rows, []string{
			ts, z.Label,
			fmt.Sprintf("%.1f", d.celsius("temperature_air")),
			fmt.Sprintf("%.1f", d.celsius("consigne_confort")),
			fmt.Sprintf("%.1f", d.celsius("consigne_eco")),
			d.str("gv_mode"), d.str("heating_up"), d.str("puissance_app"), outdoor,
		})
	}
	return rows
}

func logZones(c *Client, path string) error {
	zones, err := c.Zones()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if statErr != nil {
		w.Write(logHeader)
	}
	rows := logRows(zones, time.Now().UTC().Format(time.RFC3339), outdoorC(c.Lat, c.Lon))
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	fmt.Printf("logged %d zones\n", len(rows))
	return nil
}
