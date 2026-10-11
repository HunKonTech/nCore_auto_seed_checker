package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"ncore_automation/api"
)

// Config is what was given on the command line.
type Config struct {
	User, Pass string
	Debug      bool
	OutputDir  string        // -o, empty if not given
	Interval   time.Duration // -interval
	// IntervalSet is true when -interval was given explicitly; it then
	// overrides the interval saved on the Settings page.
	IntervalSet bool
	EnvCookies  []StoredCookie // NCORE_COOKIE
}

// App runs the checks on schedule and serves the web interface's state.
type App struct {
	cfg      Config
	logger   *log.Logger
	hub      *Hub
	logs     *LogStream
	history  *HistoryManager
	settings *SettingsStore

	runNow     chan struct{}
	reschedule chan struct{}

	mu      sync.Mutex
	running bool
	testing bool
	nextRun time.Time
	lastRun *RunRecord
	login   *LoginSession
	// loginStarting is true while the login browser is being started.
	loginStarting bool
}

func NewApp(cfg Config, logger *log.Logger, hub *Hub, logs *LogStream, history *HistoryManager, settings *SettingsStore) *App {
	a := &App{
		cfg:        cfg,
		logger:     logger,
		hub:        hub,
		logs:       logs,
		history:    history,
		settings:   settings,
		runNow:     make(chan struct{}, 1),
		reschedule: make(chan struct{}, 1),
	}
	if records := history.GetRecords(); len(records) > 0 {
		a.lastRun = &records[0]
	}
	if hub != nil {
		hub.OnConnect = a.onConnect
		hub.OnMessage = a.onMessage
	}
	return a
}

func (a *App) outputDir() string {
	if a.cfg.OutputDir != "" {
		return a.cfg.OutputDir
	}
	return a.settings.Get().OutputDir
}

func (a *App) interval() time.Duration {
	if a.cfg.IntervalSet {
		return a.cfg.Interval
	}
	if d, err := time.ParseDuration(a.settings.Get().Interval); err == nil {
		return d
	}
	return a.cfg.Interval
}

// cookies returns the session cookies to log in with: the ones saved from
// the Settings page, or else NCORE_COOKIE.
func (a *App) cookies() ([]StoredCookie, string) {
	if c := a.settings.Get().Cookies; len(c) > 0 {
		return c, "saved"
	}
	if len(a.cfg.EnvCookies) > 0 {
		return a.cfg.EnvCookies, "env"
	}
	return nil, ""
}

func (a *App) newChecker() *Checker {
	cookies, _ := a.cookies()
	return &Checker{
		User:      a.cfg.User,
		Pass:      a.cfg.Pass,
		OutputDir: a.outputDir(),
		Debug:     a.cfg.Debug,
		Logger:    a.logger,
		Cookies:   cookies,
	}
}

// ready reports why a check cannot run yet.
func (a *App) ready() error {
	if a.outputDir() == "" {
		return errors.New("no output directory set (use -o or the Settings page)")
	}
	if c, _ := a.cookies(); len(c) == 0 && (a.cfg.User == "" || a.cfg.Pass == "") {
		return errors.New("not logged in: log in on the Settings page, or use -u and -p")
	}
	return nil
}

// Loop runs a check now and then every interval, or whenever "Run now" is
// pressed. The interval can change while it is waiting.
func (a *App) Loop() {
	a.runCheck()
	for {
		var timer *time.Timer
		var tick <-chan time.Time
		if d := a.interval(); d > 0 {
			timer = time.NewTimer(d)
			tick = timer.C
			a.setNextRun(time.Now().Add(d))
		} else {
			a.setNextRun(time.Time{})
		}
		select {
		case <-tick:
			a.runCheck()
		case <-a.runNow:
			a.runCheck()
		case <-a.reschedule:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (a *App) setNextRun(t time.Time) {
	a.mu.Lock()
	a.nextRun = t
	a.mu.Unlock()
	a.broadcastState()
}

// runCheck runs a single check and records it.
func (a *App) runCheck() {
	if err := a.ready(); err != nil {
		a.logger.Println("Skipping check:", err)
		return
	}
	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	a.broadcastState()

	c := a.newChecker()
	record := c.Run()
	a.updateCookieStatus(c.CookieStatus)

	if err := a.history.SaveRecord(record); err != nil {
		a.logger.Println("Failed to save run record:", err)
	} else {
		a.logger.Println("Run recorded to history.json")
	}
	a.hub.Broadcast(api.MsgHistoryAdd, record)

	a.mu.Lock()
	a.running = false
	a.lastRun = &record
	a.mu.Unlock()
	a.broadcastState()
}

func (a *App) updateCookieStatus(status string) {
	if status == "" {
		return
	}
	if _, src := a.cookies(); src != "saved" {
		return
	}
	err := a.settings.Update(func(s *Settings) {
		s.CookieStatus = status
		s.CookieCheckedAt = time.Now()
	})
	if err != nil {
		a.logger.Println("Failed to save settings:", err)
	}
}

func (a *App) saveCookies(cookies []StoredCookie, status string) error {
	err := a.settings.Update(func(s *Settings) {
		s.Cookies = cookies
		s.CookieSavedAt = time.Now()
		s.CookieStatus = status
		s.CookieCheckedAt = time.Time{}
		if status != "" {
			s.CookieCheckedAt = time.Now()
		}
	})
	a.broadcastState()
	return err
}

func (a *App) State() api.State {
	s := a.settings.Get()
	cookies, src := a.cookies()
	view := api.SettingsView{
		OutputDir:       a.outputDir(),
		OutputDirLocked: a.cfg.OutputDir != "",
		IntervalLocked:  a.cfg.IntervalSet,
		HasPassword:     a.cfg.User != "" && a.cfg.Pass != "",
		Cookie: api.CookieInfo{
			Present:   len(cookies) > 0,
			Source:    src,
			Names:     cookieNames(cookies),
			ExpiresAt: earliestExpiry(cookies),
		},
	}
	if d := a.interval(); d > 0 {
		view.Interval = d.String()
	}
	if src == "saved" {
		view.Cookie.Status = s.CookieStatus
		if !s.CookieSavedAt.IsZero() {
			t := s.CookieSavedAt
			view.Cookie.SavedAt = &t
		}
		if !s.CookieCheckedAt.IsZero() {
			t := s.CookieCheckedAt
			view.Cookie.CheckedAt = &t
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	st := api.State{Running: a.running, Testing: a.testing, LastRun: a.lastRun, Settings: view}
	if !a.nextRun.IsZero() {
		t := a.nextRun
		st.NextRun = &t
	}
	if a.login != nil {
		st.Login = a.login.Status()
	} else if a.loginStarting {
		st.Login = api.LoginStatus{Active: true, Message: "Starting browser..."}
	}
	return st
}

func (a *App) broadcastState() {
	if a.hub != nil {
		a.hub.Broadcast(api.MsgState, a.State())
	}
}

func (a *App) notify(level, msg string) {
	a.hub.Broadcast(api.MsgNotice, api.Notice{Level: level, Message: msg})
}

func (a *App) onConnect(c *wsClient) {
	c.Send(api.MsgState, a.State())
	c.Send(api.MsgHistory, a.history.GetRecords())
	c.Send(api.MsgLog, api.LogLines{Lines: a.logs.Backlog(), Reset: true})
	a.mu.Lock()
	login := a.login
	a.mu.Unlock()
	if login != nil {
		if f := login.LastFrame(); f != nil {
			c.Send(api.MsgFrame, f)
		}
	}
}

func (a *App) onMessage(c *wsClient, msg api.Envelope) {
	if err := a.handle(msg); err != nil {
		c.Send(api.MsgNotice, api.Notice{Level: "error", Message: err.Error()})
	}
}

func (a *App) handle(msg api.Envelope) error {
	decode := func(v any) error { return json.Unmarshal(msg.Data, v) }

	switch msg.Type {
	case api.MsgRunNow:
		if err := a.ready(); err != nil {
			return err
		}
		select {
		case a.runNow <- struct{}{}:
		default:
		}
		return nil

	case api.MsgSaveSettings:
		var u api.SettingsUpdate
		if err := decode(&u); err != nil {
			return err
		}
		u.Interval = strings.TrimSpace(u.Interval)
		if u.Interval != "" {
			d, err := time.ParseDuration(u.Interval)
			if err != nil {
				return fmt.Errorf("invalid interval %q (examples: 30m, 1h, 0 to disable)", u.Interval)
			}
			if d > 0 && d < time.Minute {
				return errors.New("the interval must be at least 1m")
			}
		}
		err := a.settings.Update(func(s *Settings) {
			s.OutputDir = strings.TrimSpace(u.OutputDir)
			s.Interval = u.Interval
		})
		if err != nil {
			return err
		}
		select {
		case a.reschedule <- struct{}{}:
		default:
		}
		a.broadcastState()
		a.notify("success", "Settings saved.")
		return nil

	case api.MsgSetCookie:
		var in api.CookieInput
		if err := decode(&in); err != nil {
			return err
		}
		cookies := parseCookieString(in.Cookie)
		if len(cookies) == 0 {
			return errors.New(`no cookies found, expected "name=value; name2=value2"`)
		}
		if err := a.saveCookies(cookies, ""); err != nil {
			return err
		}
		a.notify("success", "Cookie saved. Use \"Test cookie\" to check it.")
		return nil

	case api.MsgClearCookie:
		err := a.settings.Update(func(s *Settings) {
			s.Cookies = nil
			s.CookieSavedAt = time.Time{}
			s.CookieStatus = ""
			s.CookieCheckedAt = time.Time{}
		})
		a.broadcastState()
		return err

	case api.MsgTestCookie:
		return a.testCookie()

	case api.MsgLoginStart:
		return a.startLogin()

	case api.MsgLoginStop:
		a.stopLogin()
		return nil
	}

	// The rest are for the login browser.
	a.mu.Lock()
	login := a.login
	a.mu.Unlock()
	if login == nil {
		return nil
	}
	switch msg.Type {
	case api.MsgLoginCapture:
		return login.Capture(false)
	case api.MsgLoginNav:
		var n api.NavAction
		if err := decode(&n); err != nil {
			return err
		}
		return login.Navigate(n.Action)
	case api.MsgMouse:
		var m api.MouseInput
		if err := decode(&m); err != nil {
			return err
		}
		return login.Mouse(m)
	case api.MsgKey:
		var k api.KeyInput
		if err := decode(&k); err != nil {
			return err
		}
		return login.Key(k)
	case api.MsgText:
		var t api.TextInput
		if err := decode(&t); err != nil {
			return err
		}
		return login.Text(t.Text)
	}
	return fmt.Errorf("unknown message %q", msg.Type)
}

func (a *App) testCookie() error {
	cookies, _ := a.cookies()
	if len(cookies) == 0 {
		return errors.New("there is no session cookie to test")
	}
	a.mu.Lock()
	if a.testing {
		a.mu.Unlock()
		return nil
	}
	a.testing = true
	a.mu.Unlock()
	a.broadcastState()

	go func() {
		// Only the cookie is tested, without the password fallback.
		c := &Checker{Logger: a.logger, Cookies: cookies}
		err := c.TestLogin()
		a.updateCookieStatus(c.CookieStatus)
		a.mu.Lock()
		a.testing = false
		a.mu.Unlock()
		a.broadcastState()
		if err != nil {
			a.notify("error", "Cookie test failed: "+err.Error())
		} else {
			a.notify("success", "The session cookie works.")
		}
	}()
	return nil
}

func (a *App) startLogin() error {
	a.mu.Lock()
	if a.login != nil || a.loginStarting {
		a.mu.Unlock()
		return nil
	}
	a.loginStarting = true
	a.mu.Unlock()
	a.broadcastState()

	a.logger.Println("Starting login browser...")
	s, err := startLoginSession(a)

	a.mu.Lock()
	a.loginStarting = false
	if err == nil {
		a.login = s
	}
	a.mu.Unlock()
	a.broadcastState()
	if err != nil {
		return fmt.Errorf("could not start the login browser: %w", err)
	}
	return nil
}

func (a *App) stopLogin() {
	a.mu.Lock()
	s := a.login
	a.login = nil
	a.mu.Unlock()
	if s != nil {
		s.Close()
		a.broadcastState()
	}
}
