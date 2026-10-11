package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"ncore_automation/api"
)

// The nCore login form has a reCAPTCHA, so it cannot be filled in
// automatically. Instead a headless Chrome on the server opens it, its screen
// is streamed to the Settings page and the user's mouse and keyboard input is
// sent back. Once the user is logged in, the cookies are read straight from
// that browser. (A plain iframe cannot work: the page could not read
// ncore.pro's cookies, and browsers block them in third-party frames.)

const (
	loginViewWidth  = 1200
	loginViewHeight = 860
	loginIdleLimit  = 10 * time.Minute
)

// LoginSession is one remote login browser.
type LoginSession struct {
	app    *App
	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	url          string
	message      string
	lastFrame    *api.Frame
	lastActivity time.Time
	captured     bool
	closed       bool
}

func startLoginSession(app *App) (*LoginSession, error) {
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		allocatorOptions(chromedp.WindowSize(loginViewWidth, loginViewHeight))...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithLogf(app.logger.Printf))
	s := &LoginSession{
		app: app,
		ctx: ctx,
		cancel: func() {
			cancelCtx()
			cancelAlloc()
		},
		message:      "Starting browser...",
		lastActivity: time.Now(),
	}

	chromedp.ListenTarget(ctx, func(ev any) {
		switch e := ev.(type) {
		case *page.EventScreencastFrame:
			s.onFrame(e)
		case *page.EventFrameNavigated:
			if e.Frame.ParentID == "" {
				s.setURL(e.Frame.URL)
			}
		case *page.EventLoadEventFired:
			go s.checkLoggedIn()
		}
	})

	err := chromedp.Run(ctx,
		// Headless Chrome announces itself as "HeadlessChrome", which makes
		// reCAPTCHA much harder to solve for the user.
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, _, ua, _, err := browser.GetVersion().Do(ctx)
			if err != nil {
				return err
			}
			return emulation.SetUserAgentOverride(strings.Replace(ua, "HeadlessChrome", "Chrome", 1)).Do(ctx)
		}),
		emulation.SetDeviceMetricsOverride(loginViewWidth, loginViewHeight, 1, false),
		network.ClearBrowserCookies(),
		page.StartScreencast().WithFormat(page.ScreencastFormatJpeg).WithQuality(70),
		chromedp.Navigate(loginUrl),
	)
	if err != nil {
		s.cancel()
		return nil, err
	}
	s.setMessage("Log in to nCore below. Tick \"stay logged in\" if offered so the cookie lasts longer.")
	go s.watchIdle()
	return s, nil
}

// Close shuts the browser down.
func (s *LoginSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
}

func (s *LoginSession) Status() api.LoginStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return api.LoginStatus{Active: !s.closed, URL: s.url, Message: s.message}
}

func (s *LoginSession) LastFrame() *api.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastFrame
}

func (s *LoginSession) onFrame(e *page.EventScreencastFrame) {
	f := &api.Frame{Data: e.Data}
	if e.Metadata != nil {
		f.Width, f.Height = e.Metadata.DeviceWidth, e.Metadata.DeviceHeight
	}
	s.mu.Lock()
	s.lastFrame = f
	s.mu.Unlock()
	s.app.hub.Broadcast(api.MsgFrame, f)
	// Chrome sends the next frame only after this one is acknowledged.
	go chromedp.Run(s.ctx, page.ScreencastFrameAck(e.SessionID))
}

func (s *LoginSession) setURL(u string) {
	s.mu.Lock()
	s.url = u
	s.mu.Unlock()
	s.app.broadcastState()
}

func (s *LoginSession) setMessage(m string) {
	s.mu.Lock()
	s.message = m
	s.mu.Unlock()
	s.app.broadcastState()
}

// checkLoggedIn runs after every page load. Any nCore page without a
// password field, other than the login page, means the login worked.
func (s *LoginSession) checkLoggedIn() {
	s.mu.Lock()
	cur, done := s.url, s.captured || s.closed
	s.mu.Unlock()
	if done {
		return
	}
	u, err := url.Parse(cur)
	if err != nil || !strings.HasSuffix(u.Hostname(), "ncore.pro") || strings.Contains(u.Path, "login.php") {
		return
	}
	var hasPassword bool
	if err := chromedp.Run(s.ctx, chromedp.Evaluate(`!!document.querySelector('input[type=password]')`, &hasPassword)); err != nil || hasPassword {
		return
	}
	if err := s.Capture(true); err != nil {
		s.app.logger.Println("Saving login cookies failed:", err)
	}
}

// Capture saves the browser's nCore cookies and closes the session.
// loggedIn is false when the user pressed "Save cookie" without the login
// being detected; the cookie is then saved as untested.
func (s *LoginSession) Capture(loggedIn bool) error {
	var cookies []*network.Cookie
	err := chromedp.Run(s.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().WithURLs([]string{"https://ncore.pro/"}).Do(ctx)
		return err
	}))
	if err != nil {
		return err
	}
	stored := cookiesFromBrowser(cookies)
	if len(stored) == 0 {
		return errors.New("the browser has no nCore cookies yet")
	}

	s.mu.Lock()
	if s.captured {
		s.mu.Unlock()
		return nil
	}
	s.captured = true
	s.mu.Unlock()

	status, msg := "valid", "Logged in, session cookie saved."
	if !loggedIn {
		status, msg = "", "Session cookie saved. Use \"Test cookie\" to check that it works."
	}
	if err := s.app.saveCookies(stored, status); err != nil {
		return err
	}
	s.app.logger.Printf("Saved %d nCore cookies from the login browser.", len(stored))
	s.app.notify("success", msg)
	s.app.stopLogin()
	return nil
}

// Navigate handles the back/reload/home buttons above the screen.
func (s *LoginSession) Navigate(action string) error {
	s.touch()
	switch action {
	case "back":
		return chromedp.Run(s.ctx, chromedp.NavigateBack())
	case "reload":
		return chromedp.Run(s.ctx, chromedp.Reload())
	case "home":
		return chromedp.Run(s.ctx, chromedp.Navigate(loginUrl))
	}
	return fmt.Errorf("unknown navigation %q", action)
}

func (s *LoginSession) Mouse(m api.MouseInput) error {
	s.touch()
	x, y := m.X*loginViewWidth, m.Y*loginViewHeight
	p := input.DispatchMouseEvent(input.MouseType(m.Type), x, y).
		WithModifiers(input.Modifier(m.Modifiers)).
		WithButtons(m.Buttons)
	if m.Button != "" {
		p = p.WithButton(input.MouseButton(m.Button))
	}
	if m.ClickCount > 0 {
		p = p.WithClickCount(m.ClickCount)
	}
	if m.Type == string(input.MouseWheel) {
		p = p.WithDeltaX(m.DeltaX).WithDeltaY(m.DeltaY)
	}
	return chromedp.Run(s.ctx, p)
}

func (s *LoginSession) Key(k api.KeyInput) error {
	s.touch()
	typ := input.KeyUp
	if k.Type == "keyDown" {
		// Keys that produce text use keyDown, others (Tab, Backspace,
		// arrows...) rawKeyDown, the same way chromedp's own typing does.
		typ = input.KeyRawDown
		if k.Text != "" {
			typ = input.KeyDown
		}
	}
	p := input.DispatchKeyEvent(typ).
		WithKey(k.Key).
		WithCode(k.Code).
		WithWindowsVirtualKeyCode(k.KeyCode).
		WithNativeVirtualKeyCode(k.KeyCode).
		WithModifiers(input.Modifier(k.Modifiers))
	if typ == input.KeyDown {
		p = p.WithText(k.Text).WithUnmodifiedText(k.Text)
	}
	return chromedp.Run(s.ctx, p)
}

func (s *LoginSession) Text(t string) error {
	s.touch()
	return chromedp.Run(s.ctx, input.InsertText(t))
}

func (s *LoginSession) touch() {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
}

// watchIdle closes a forgotten login browser.
func (s *LoginSession) watchIdle() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			idle := time.Since(s.lastActivity)
			s.mu.Unlock()
			if idle > loginIdleLimit {
				s.app.logger.Println("Closing idle login browser.")
				s.app.notify("info", "The login browser was closed after 10 minutes without input.")
				s.app.stopLogin()
				return
			}
		}
	}
}
