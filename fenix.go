package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	http     *http.Client
	tokenURL string
	apiBase  string
	token    string
	HomeID   string
	Lat, Lon string
}

type Device map[string]any

type Zone struct {
	Label  string
	Device Device
}

func NewClient(host, realm string) *Client {
	return &Client{
		http:     &http.Client{Timeout: 30 * time.Second},
		tokenURL: fmt.Sprintf("https://auth.%s/realms/%s/protocol/openid-connect/token", host, realm),
		apiBase:  fmt.Sprintf("https://%s/api/v0.1/", host),
	}
}

func toC(raw int) float64 {
	return math.Round(float64(raw-320)/18*10) / 10
}

func toRaw(celsius float64) int {
	return int(math.Round(celsius*18 + 320))
}

func (d Device) str(key string) string {
	return fmt.Sprint(d[key])
}

func (d Device) celsius(key string) float64 {
	raw, _ := strconv.Atoi(d.str(key))
	return toC(raw)
}

func (c *Client) Login(email, password string) error {
	resp, err := c.http.PostForm(c.tokenURL, url.Values{
		"grant_type": {"password"},
		"client_id":  {"app-front"},
		"username":   {email},
		"password":   {password},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login: %s", resp.Status)
	}
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return err
	}
	c.token = t.AccessToken

	data, err := c.post("human/user/read/", url.Values{"email": {email}}, 1, 8)
	if err != nil {
		return err
	}
	var u struct {
		Smarthomes []struct {
			ID  string `json:"smarthome_id"`
			Lat string `json:"latitude"`
			Lon string `json:"longitude"`
		} `json:"smarthomes"`
	}
	if err := json.Unmarshal(data, &u); err != nil {
		return err
	}
	if len(u.Smarthomes) == 0 {
		return fmt.Errorf("account has no homes")
	}
	h := u.Smarthomes[0]
	c.HomeID, c.Lat, c.Lon = h.ID, h.Lat, h.Lon
	return nil
}

func (c *Client) post(endpoint string, form url.Values, okCodes ...int64) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodPost, c.apiBase+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s: %s", endpoint, resp.Status)
	}
	var r struct {
		Code struct {
			Code  json.Number `json:"code"`
			Key   string      `json:"key"`
			Value string      `json:"value"`
		} `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	code, _ := r.Code.Code.Int64()
	for _, ok := range okCodes {
		if code == ok {
			return r.Data, nil
		}
	}
	return nil, fmt.Errorf("%s: api code %d %s %s", endpoint, code, r.Code.Key, r.Code.Value)
}

func (c *Client) Zones() ([]Zone, error) {
	data, err := c.post("human/smarthome/read/", url.Values{"smarthome_id": {c.HomeID}}, 1, 8)
	if err != nil {
		return nil, err
	}
	var h struct {
		Zones []struct {
			Label   string   `json:"zone_label"`
			Devices []Device `json:"devices"`
		} `json:"zones"`
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	var zones []Zone
	for _, z := range h.Zones {
		if len(z.Devices) > 0 {
			zones = append(zones, Zone{Label: z.Label, Device: z.Devices[0]})
		}
	}
	return zones, nil
}

func (c *Client) Write(deviceID string, params map[string]string) error {
	form := url.Values{
		"smarthome_id":     {c.HomeID},
		"context":          {"1"},
		"peremption":       {"15000"},
		"query[id_device]": {deviceID},
	}
	for k, v := range params {
		form.Set("query["+k+"]", v)
	}
	_, err := c.post("human/query/push/", form, 8)
	return err
}
