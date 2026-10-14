package main

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
)

// settingsFile holds the session cookie and login credentials, so it is written with 0600.
const settingsFile = "settings.json"

// StoredCookie is an nCore cookie as captured from the login browser.
type StoredCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"` // unix seconds, 0 for a session cookie
	HTTPOnly bool    `json:"http_only,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"same_site,omitempty"`
}

// Settings is everything the web interface can change, persisted to settings.json.
type Settings struct {
	OutputDir       string         `json:"output_dir,omitempty"`
	Interval        string         `json:"interval,omitempty"`
	Cookies         []StoredCookie `json:"cookies,omitempty"`
	Username        string         `json:"username,omitempty"` // write-only from the web interface
	Password        string         `json:"password,omitempty"` // write-only from the web interface
	CookieSavedAt   time.Time      `json:"cookie_saved_at,omitempty"`
	CookieStatus    string         `json:"cookie_status,omitempty"`
	CookieCheckedAt time.Time      `json:"cookie_checked_at,omitempty"`
}

// SettingsStore guards Settings and writes every change to disk.
type SettingsStore struct {
	mu sync.Mutex
	s  Settings
}

func LoadSettings() (*SettingsStore, error) {
	st := &SettingsStore{}
	data, err := os.ReadFile(settingsFile)
	if os.IsNotExist(err) || len(data) == 0 {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &st.s); err != nil {
		return nil, err
	}
	return st, nil
}

// Get returns a copy of the current settings.
func (st *SettingsStore) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.s
	s.Cookies = append([]StoredCookie(nil), st.s.Cookies...)
	return s
}

// Update applies fn to the settings and saves them.
func (st *SettingsStore) Update(fn func(*Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	data, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsFile, data, 0600)
}

// cookiesFromBrowser keeps the nCore cookies returned by the browser.
func cookiesFromBrowser(cookies []*network.Cookie) []StoredCookie {
	var out []StoredCookie
	for _, c := range cookies {
		if !strings.HasSuffix(strings.TrimPrefix(c.Domain, "."), "ncore.pro") {
			continue
		}
		sc := StoredCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
			SameSite: c.SameSite.String(),
		}
		if !c.Session {
			sc.Expires = c.Expires
		}
		out = append(out, sc)
	}
	return out
}

// parseCookieString turns "name=value; name2=value2" (as copied from the
// browser's developer tools or NCORE_COOKIE) into cookies.
func parseCookieString(raw string) []StoredCookie {
	var out []StoredCookie
	for _, part := range strings.Split(raw, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 || kv[0] == "" {
			continue
		}
		out = append(out, StoredCookie{Name: kv[0], Value: kv[1], Domain: "ncore.pro", Path: "/", Secure: true})
	}
	return out
}

// cookieParams converts stored cookies into the form chromedp sets them with.
func cookieParams(cookies []StoredCookie) []*network.CookieParam {
	params := make([]*network.CookieParam, 0, len(cookies))
	for _, c := range cookies {
		p := &network.CookieParam{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
		}
		if p.Path == "" {
			p.Path = "/"
		}
		if c.SameSite != "" {
			p.SameSite = network.CookieSameSite(c.SameSite)
		}
		if c.Expires > 0 {
			t := cdp.TimeSinceEpoch(time.Unix(int64(c.Expires), 0))
			p.Expires = &t
		}
		params = append(params, p)
	}
	return params
}

// cookieNames returns the sorted cookie names, for display.
func cookieNames(cookies []StoredCookie) []string {
	names := make([]string, 0, len(cookies))
	for _, c := range cookies {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return names
}

// earliestExpiry returns when the first persistent cookie expires.
func earliestExpiry(cookies []StoredCookie) *time.Time {
	var min *time.Time
	for _, c := range cookies {
		if c.Expires <= 0 {
			continue
		}
		t := time.Unix(int64(c.Expires), 0)
		if min == nil || t.Before(*min) {
			min = &t
		}
	}
	return min
}
