package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const homeJSON = `{"code":{"code":"8","key":"ok","value":"ok"},"data":{"zones":[
{"zone_label":"OBYVAK","devices":[{"id_device":"C001-000","temperature_air":"751","consigne_confort":"717","consigne_eco":"536","gv_mode":"16","heating_up":"0","puissance_app":"3400","fan_speed":0,"on_off":null}]},
{"zone_label":"EMPTY","devices":[]}]}}`

func TestClient(t *testing.T) {
	var pushed map[string][]string
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "password" || r.Form.Get("username") != "a@b.c" {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"access_token":"tok"}`))
	})
	mux.HandleFunc("/api/v0.1/human/user/read/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauth", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"code":{"code":1,"key":"ok","value":"ok"},"data":{"smarthomes":[{"smarthome_id":"H1","latitude":"49.2","longitude":"17.6"}]}}`))
	})
	mux.HandleFunc("/api/v0.1/human/smarthome/read/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(homeJSON))
	})
	mux.HandleFunc("/api/v0.1/human/query/push/", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		pushed = r.Form
		w.Write([]byte(`{"code":{"code":"8","key":"ok","value":"ok"},"data":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{http: srv.Client(), tokenURL: srv.URL + "/token", apiBase: srv.URL + "/api/v0.1/"}
	if err := c.Login("a@b.c", "pw"); err != nil {
		t.Fatal(err)
	}
	if c.HomeID != "H1" || c.Lat != "49.2" {
		t.Fatalf("home not parsed: %+v", c)
	}

	zones, err := c.Zones()
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 1 || zones[0].Label != "OBYVAK" {
		t.Fatalf("zones: %+v", zones)
	}
	d := zones[0].Device
	if d.celsius("temperature_air") != 23.9 || d.str("gv_mode") != "16" {
		t.Fatalf("device: %+v", d)
	}

	if err := c.Write("C001-000", map[string]string{"consigne_confort": "707"}); err != nil {
		t.Fatal(err)
	}
	if pushed["query[id_device]"][0] != "C001-000" || pushed["query[consigne_confort]"][0] != "707" || pushed["smarthome_id"][0] != "H1" {
		t.Fatalf("push form: %v", pushed)
	}

	rows := logRows(zones, "T", "5.0")
	if len(rows) != 1 || rows[0][2] != "23.9" || rows[0][7] != "3400" || rows[0][8] != "5.0" {
		t.Fatalf("rows: %v", rows)
	}
}

func TestConversion(t *testing.T) {
	if toRaw(21.5) != 707 || toRaw(5) != 410 || toRaw(30) != 860 {
		t.Fatal("toRaw")
	}
	if toC(717) != 22.1 || toC(986) != 37 {
		t.Fatal("toC")
	}
}
