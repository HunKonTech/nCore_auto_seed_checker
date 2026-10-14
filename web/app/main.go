//go:build js && wasm

// Command app is the web interface, compiled to WebAssembly. It talks to the
// server over a websocket: the server pushes state, history, log lines and
// the login browser's screen; the app sends button presses and input.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"ncore_automation/api"
)

const maxHistoryRows = 200

var (
	doc     = js.Global().Get("document")
	ws      js.Value
	online  bool
	state   api.State
	history []api.RunRecord

	// Last values shown in the settings form, so a state update does not
	// overwrite what the user is typing.
	shownOutputDir, shownInterval string

	frameImg   js.Value
	previewOn  bool
	lastMoveMs float64
)

func main() {
	byID("boot").Get("classList").Call("add", "hidden")
	setupTabs()
	setupDashboard()
	setupSettings()
	setupBrowser()
	connect()

	// Keep the "next run in ..." countdown current.
	go func() {
		for range time.Tick(time.Second) {
			renderStatus()
		}
	}()
	select {}
}

// ---- DOM helpers ----

func byID(id string) js.Value { return doc.Call("getElementById", id) }

func setText(id, s string) { byID(id).Set("textContent", s) }

func show(id string, visible bool) {
	byID(id).Get("classList").Call("toggle", "hidden", !visible)
}

func create(tag, class, text string) js.Value {
	el := doc.Call("createElement", tag)
	if class != "" {
		el.Set("className", class)
	}
	if text != "" {
		el.Set("textContent", text)
	}
	return el
}

func on(el js.Value, event string, fn func(e js.Value)) {
	el.Call("addEventListener", event, js.FuncOf(func(this js.Value, args []js.Value) any {
		fn(args[0])
		return nil
	}))
}

// onActive is like on, but the handler may call preventDefault on events
// that browsers treat as passive by default (wheel).
func onActive(el js.Value, event string, fn func(e js.Value)) {
	el.Call("addEventListener", event, js.FuncOf(func(this js.Value, args []js.Value) any {
		fn(args[0])
		return nil
	}), map[string]any{"passive": false})
}

func onClick(id string, fn func()) { on(byID(id), "click", func(js.Value) { fn() }) }

func fmtTime(t time.Time) string {
	return js.Global().Get("Date").New(float64(t.UnixMilli())).Call("toLocaleString").String()
}

func fmtTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return fmtTime(*t)
}

func fmtUntil(t time.Time) string {
	d := time.Until(t).Round(time.Second)
	if d <= 0 {
		return "now"
	}
	if d >= time.Hour {
		return fmt.Sprintf("in %dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	if d >= time.Minute {
		return fmt.Sprintf("in %dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("in %ds", int(d.Seconds()))
}

func notice(level, msg string) {
	n := create("div", "notice "+level, msg)
	byID("notices").Call("append", n)
	js.Global().Call("setTimeout", js.FuncOf(func(js.Value, []js.Value) any {
		n.Call("remove")
		return nil
	}), 6000)
}

// ---- websocket ----

func connect() {
	loc := js.Global().Get("location")
	proto := "ws:"
	if loc.Get("protocol").String() == "https:" {
		proto = "wss:"
	}
	ws = js.Global().Get("WebSocket").New(proto + "//" + loc.Get("host").String() + "/ws")
	on(ws, "open", func(js.Value) { setOnline(true) })
	on(ws, "close", func(js.Value) {
		setOnline(false)
		js.Global().Call("setTimeout", js.FuncOf(func(js.Value, []js.Value) any {
			connect()
			return nil
		}), 2000)
	})
	on(ws, "message", func(e js.Value) { handle(e.Get("data").String()) })
}

func setOnline(v bool) {
	online = v
	byID("conn").Get("classList").Call("toggle", "online", v)
	if v {
		setText("connText", "Live")
	} else {
		setText("connText", "Reconnecting...")
	}
	renderStatus()
}

func send(typ string, data any) {
	if !online {
		notice("error", "Not connected to the server.")
		return
	}
	env := api.Envelope{Type: typ}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return
		}
		env.Data = raw
	}
	b, _ := json.Marshal(env)
	ws.Call("send", string(b))
}

func handle(raw string) {
	var env api.Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return
	}
	switch env.Type {
	case api.MsgState:
		var s api.State
		if json.Unmarshal(env.Data, &s) == nil {
			state = s
			renderStatus()
			renderSettings()
			renderBrowser()
		}
	case api.MsgHistory:
		var h []api.RunRecord
		if json.Unmarshal(env.Data, &h) == nil {
			history = h
			renderHistory()
		}
	case api.MsgHistoryAdd:
		var r api.RunRecord
		if json.Unmarshal(env.Data, &r) == nil {
			history = append([]api.RunRecord{r}, history...)
			renderHistory()
		}
	case api.MsgLog:
		var l api.LogLines
		if json.Unmarshal(env.Data, &l) == nil {
			appendLog(l)
		}
	case api.MsgFrame:
		var f api.Frame
		if json.Unmarshal(env.Data, &f) == nil {
			drawFrame(f)
		}
	case api.MsgRunFrame:
		var f api.Frame
		if json.Unmarshal(env.Data, &f) == nil {
			drawRunFrame(f)
		}
	case api.MsgNotice:
		var n api.Notice
		if json.Unmarshal(env.Data, &n) == nil {
			notice(n.Level, n.Message)
		}
	}
}

// ---- tabs ----

func setupTabs() {
	buttons := byID("tabs").Call("querySelectorAll", "button")
	for i := 0; i < buttons.Length(); i++ {
		b := buttons.Index(i)
		name := b.Get("dataset").Get("tab").String()
		on(b, "click", func(js.Value) {
			js.Global().Get("location").Set("hash", name)
			selectTab(name)
		})
	}
	tab := strings.TrimPrefix(js.Global().Get("location").Get("hash").String(), "#")
	if tab != "log" && tab != "settings" {
		tab = "dashboard"
	}
	selectTab(tab)
}

func selectTab(name string) {
	for _, t := range []string{"dashboard", "log", "settings"} {
		show("tab-"+t, t == name)
	}
	buttons := byID("tabs").Call("querySelectorAll", "button")
	for i := 0; i < buttons.Length(); i++ {
		b := buttons.Index(i)
		b.Get("classList").Call("toggle", "active", b.Get("dataset").Get("tab").String() == name)
	}
	if name == "log" {
		scrollLog()
	}
}

// ---- dashboard ----

func setupDashboard() {
	onClick("runNow", func() { send(api.MsgRunNow, nil) })
	onClick("previewToggle", func() {
		previewOn = !previewOn
		show("preview", previewOn)
		if previewOn {
			setText("previewToggle", "Hide preview")
		} else {
			setText("previewToggle", "Show preview")
		}
	})
}

// drawRunFrame shows a screenshot of the checker browser while a run is in progress.
func drawRunFrame(f api.Frame) {
	if !previewOn {
		return
	}
	byID("previewImg").Set("src", "data:image/jpeg;base64,"+f.Data)
	show("previewWait", false)
}

func renderStatus() {
	switch {
	case !online:
		setText("stState", "Offline")
	case state.Running:
		setText("stState", "Checking...")
	default:
		setText("stState", "Idle")
	}
	byID("runNow").Set("disabled", !online || state.Running)
	if !state.Running {
		show("previewWait", true)
	}

	if state.NextRun != nil && !state.Running {
		setText("stNext", fmtUntil(*state.NextRun))
	} else if state.Running {
		setText("stNext", "-")
	} else {
		setText("stNext", "Only on \"Run now\"")
	}

	if r := state.LastRun; r != nil {
		setText("stLast", fmtTime(r.Timestamp))
		setText("stLastSummary", fmt.Sprintf("Last result: %s - found %d, downloaded %d in %s.",
			r.LogSummary, r.ItemsFound, r.ItemsDownloaded, r.Duration))
	} else {
		setText("stLast", "Never")
		setText("stLastSummary", "")
	}

	text, _ := cookieLabel(state.Settings.Cookie)
	setText("stCookie", text)
}

// cookieLabel returns the cookie state text and its badge class.
func cookieLabel(c api.CookieInfo) (string, string) {
	switch {
	case !c.Present:
		return "Not logged in", "status-error"
	case c.Status == "rejected":
		return "Expired - log in again", "status-error"
	case c.Status == "valid":
		return "Valid", "status-success"
	case c.Source == "env":
		return "From NCORE_COOKIE", "status-info"
	default:
		return "Saved, not tested yet", "status-warning"
	}
}

func renderHistory() {
	tbody := byID("historyBody")
	tbody.Set("textContent", "")
	show("historyEmpty", len(history) == 0)
	show("historyTable", len(history) > 0)

	for i, run := range history {
		if i == maxHistoryRows {
			break
		}
		tr := create("tr", "", "")
		tr.Call("append", create("td", "time-cell", fmtTime(run.Timestamp)))
		tr.Call("append", create("td", "", run.Duration))
		tr.Call("append", create("td", "", fmt.Sprint(run.ItemsFound)))
		tr.Call("append", create("td", "", fmt.Sprint(run.ItemsDownloaded)))

		badge := "status-badge status-error"
		if run.LogSummary == "Success" {
			badge = "status-badge status-success"
		}
		td := create("td", "", "")
		td.Call("append", create("span", badge, run.LogSummary))
		tr.Call("append", td)

		td = create("td", "", "")
		if len(run.Downloads) == 0 {
			td.Set("textContent", "-")
		} else {
			ul := create("ul", "downloads-list", "")
			for _, d := range run.Downloads {
				ul.Call("append", create("li", "", d))
			}
			td.Call("append", ul)
		}
		tr.Call("append", td)
		tbody.Call("append", tr)
	}
}

// ---- log ----

func appendLog(l api.LogLines) {
	el := byID("log")
	if l.Reset {
		el.Set("textContent", "")
	}
	if len(l.Lines) > 0 {
		el.Call("append", strings.Join(l.Lines, "\n")+"\n")
	}
	// Each append is one text node; keep the page light.
	for el.Get("childNodes").Length() > 2000 {
		el.Get("firstChild").Call("remove")
	}
	scrollLog()
}

func scrollLog() {
	if byID("logFollow").Get("checked").Bool() {
		el := byID("log")
		el.Set("scrollTop", el.Get("scrollHeight"))
	}
}

// ---- settings ----

func setupSettings() {
	onClick("settingsSave", func() {
		send(api.MsgSaveSettings, api.SettingsUpdate{
			OutputDir: byID("setOutputDir").Get("value").String(),
			Interval:  byID("setInterval").Get("value").String(),
		})
	})
	onClick("cookieSave", func() {
		in := byID("cookieInput")
		send(api.MsgSetCookie, api.CookieInput{Cookie: in.Get("value").String()})
		in.Set("value", "")
	})
	onClick("loginPwSave", func() {
		user, pass := byID("loginUser"), byID("loginPass")
		send(api.MsgSetLogin, api.LoginInput{
			Username: user.Get("value").String(),
			Password: pass.Get("value").String(),
		})
		user.Set("value", "")
		pass.Set("value", "")
	})
	onClick("loginPwClear", func() {
		if js.Global().Call("confirm", "Delete the saved username and password?").Bool() {
			send(api.MsgClearLogin, nil)
		}
	})
	onClick("cookieTest", func() { send(api.MsgTestCookie, nil) })
	onClick("cookieClear", func() {
		if js.Global().Call("confirm", "Delete the saved session cookie?").Bool() {
			send(api.MsgClearCookie, nil)
		}
	})
	onClick("loginStart", func() {
		show("browser", true)
		show("screenWait", true)
		setText("screenWait", "Starting browser...")
		send(api.MsgLoginStart, nil)
	})
}

func renderSettings() {
	s := state.Settings
	setInput("setOutputDir", s.OutputDir, &shownOutputDir, s.OutputDirLocked)
	setInput("setInterval", s.Interval, &shownInterval, s.IntervalLocked)
	show("setOutputDirLocked", s.OutputDirLocked)
	show("setIntervalLocked", s.IntervalLocked)

	c := s.Cookie
	text, class := cookieLabel(c)
	el := byID("ckStatus")
	el.Set("textContent", "")
	el.Call("append", create("span", "status-badge "+class, text))
	if state.Testing {
		el.Call("append", create("span", "muted", " testing..."))
	}
	setText("ckSaved", fmtTimePtr(c.SavedAt))
	if c.Present && c.ExpiresAt == nil {
		setText("ckExpires", "When nCore ends the session")
	} else {
		setText("ckExpires", fmtTimePtr(c.ExpiresAt))
	}
	setText("ckChecked", fmtTimePtr(c.CheckedAt))
	if len(c.Names) > 0 {
		setText("ckNames", "Cookies: "+strings.Join(c.Names, ", "))
	} else if s.HasPassword {
		setText("ckNames", "No cookie saved: the saved or command line username and password are used, which fails when nCore asks for a reCAPTCHA.")
	} else {
		setText("ckNames", "")
	}

	switch s.LoginSource {
	case "flags":
		setText("loginPwState", "Set on the command line (-u/-p), not changeable here.")
	case "saved":
		setText("loginPwState", "A username and password are saved.")
	default:
		setText("loginPwState", "Nothing saved.")
	}
	locked := s.LoginSource == "flags"
	byID("loginUser").Set("disabled", locked)
	byID("loginPass").Set("disabled", locked)
	byID("loginPwSave").Set("disabled", locked)
	byID("loginPwClear").Set("disabled", s.LoginSource != "saved")

	byID("cookieTest").Set("disabled", !c.Present || state.Testing)
	byID("cookieClear").Set("disabled", c.Source != "saved")
	byID("loginStart").Set("disabled", state.Login.Active)
}

// setInput updates a form field from the server unless the user is editing it.
func setInput(id, value string, shown *string, locked bool) {
	el := byID(id)
	el.Set("disabled", locked)
	if value == *shown {
		return
	}
	if !el.Equal(doc.Get("activeElement")) {
		el.Set("value", value)
	}
	*shown = value
}

// ---- remote login browser ----

func setupBrowser() {
	canvas := byID("screen")
	kbd := byID("kbd")
	frameImg = js.Global().Get("Image").New()
	on(frameImg, "load", func(js.Value) {
		w, h := frameImg.Get("naturalWidth").Int(), frameImg.Get("naturalHeight").Int()
		if canvas.Get("width").Int() != w || canvas.Get("height").Int() != h {
			canvas.Set("width", w)
			canvas.Set("height", h)
		}
		canvas.Call("getContext", "2d").Call("drawImage", frameImg, 0, 0)
		show("screenWait", false)
	})

	onClick("navBack", func() { send(api.MsgLoginNav, api.NavAction{Action: "back"}) })
	onClick("navReload", func() { send(api.MsgLoginNav, api.NavAction{Action: "reload"}) })
	onClick("navHome", func() { send(api.MsgLoginNav, api.NavAction{Action: "home"}) })
	onClick("loginCapture", func() { send(api.MsgLoginCapture, nil) })
	onClick("loginStop", func() { send(api.MsgLoginStop, nil) })

	mouse := func(typ string, e js.Value) api.MouseInput {
		r := canvas.Call("getBoundingClientRect")
		return api.MouseInput{
			Type:      typ,
			X:         (e.Get("clientX").Float() - r.Get("left").Float()) / r.Get("width").Float(),
			Y:         (e.Get("clientY").Float() - r.Get("top").Float()) / r.Get("height").Float(),
			Buttons:   int64(e.Get("buttons").Int()),
			Modifiers: modifiers(e),
		}
	}
	buttonName := func(e js.Value) string {
		return [...]string{"left", "middle", "right", "back", "forward"}[e.Get("button").Int()%5]
	}

	on(canvas, "mousedown", func(e js.Value) {
		e.Call("preventDefault")
		// Keys go through a hidden textarea so pasting and mobile keyboards work.
		kbd.Call("focus", map[string]any{"preventScroll": true})
		m := mouse("mousePressed", e)
		m.Button = buttonName(e)
		m.ClickCount = int64(e.Get("detail").Int())
		send(api.MsgMouse, m)
	})
	on(canvas, "mouseup", func(e js.Value) {
		m := mouse("mouseReleased", e)
		m.Button = buttonName(e)
		m.ClickCount = int64(e.Get("detail").Int())
		send(api.MsgMouse, m)
	})
	on(canvas, "mousemove", func(e js.Value) {
		now := e.Get("timeStamp").Float()
		if now-lastMoveMs < 30 {
			return
		}
		lastMoveMs = now
		m := mouse("mouseMoved", e)
		m.Button = "none"
		if m.Buttons&1 != 0 {
			m.Button = "left"
		}
		send(api.MsgMouse, m)
	})
	onActive(canvas, "wheel", func(e js.Value) {
		e.Call("preventDefault")
		scale := 1.0
		if e.Get("deltaMode").Int() == 1 { // lines
			scale = 40
		}
		m := mouse("mouseWheel", e)
		m.DeltaX = e.Get("deltaX").Float() * scale
		m.DeltaY = e.Get("deltaY").Float() * scale
		send(api.MsgMouse, m)
	})
	on(canvas, "contextmenu", func(e js.Value) { e.Call("preventDefault") })

	key := func(typ string, e js.Value) {
		k := e.Get("key").String()
		if k == "Unidentified" || e.Get("keyCode").Int() == 229 {
			return // composing on a mobile keyboard; arrives as an input event
		}
		mods := modifiers(e)
		if mods&(2|4) != 0 && strings.EqualFold(k, "v") {
			return // let the browser fire a paste event
		}
		e.Call("preventDefault")
		in := api.KeyInput{Type: typ, Key: k, Code: e.Get("code").String(), KeyCode: int64(e.Get("keyCode").Int()), Modifiers: mods}
		if typ == "keyDown" && mods&(2|4) == 0 {
			if k == "Enter" {
				in.Text = "\r"
			} else if len([]rune(k)) == 1 {
				in.Text = k
			}
		}
		send(api.MsgKey, in)
	}
	on(kbd, "keydown", func(e js.Value) { key("keyDown", e) })
	on(kbd, "keyup", func(e js.Value) { key("keyUp", e) })
	on(kbd, "paste", func(e js.Value) {
		e.Call("preventDefault")
		if t := e.Get("clipboardData").Call("getData", "text").String(); t != "" {
			send(api.MsgText, api.TextInput{Text: t})
		}
	})
	on(kbd, "input", func(js.Value) {
		if v := kbd.Get("value").String(); v != "" {
			send(api.MsgText, api.TextInput{Text: v})
		}
		kbd.Set("value", "")
	})
}

func modifiers(e js.Value) int64 {
	var m int64
	if e.Get("altKey").Bool() {
		m |= 1
	}
	if e.Get("ctrlKey").Bool() {
		m |= 2
	}
	if e.Get("metaKey").Bool() {
		m |= 4
	}
	if e.Get("shiftKey").Bool() {
		m |= 8
	}
	return m
}

func renderBrowser() {
	l := state.Login
	show("browser", l.Active)
	if !l.Active {
		return
	}
	setText("browserUrl", l.URL)
	setText("browserMsg", l.Message)
}

func drawFrame(f api.Frame) {
	if !state.Login.Active {
		return
	}
	frameImg.Set("src", "data:image/jpeg;base64,"+f.Data)
}
