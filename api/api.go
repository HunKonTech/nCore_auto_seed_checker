// Package api holds the types shared between the Go server and the
// WebAssembly web interface. Every websocket message is an Envelope whose
// Data field is one of the payload types below, selected by Type.
package api

import (
	"encoding/json"
	"time"
)

// Server -> client message types.
const (
	MsgState      = "state"       // State
	MsgHistory    = "history"     // []RunRecord, newest first
	MsgHistoryAdd = "history_add" // RunRecord
	MsgLog        = "log"         // LogLines
	MsgFrame      = "frame"       // Frame
	MsgRunFrame   = "run_frame"   // Frame of the checker browser during a run
	MsgNotice     = "notice"      // Notice
)

// Client -> server message types.
const (
	MsgRunNow       = "run_now"
	MsgSaveSettings = "save_settings" // SettingsUpdate
	MsgSetCookie    = "set_cookie"    // CookieInput
	MsgClearCookie  = "clear_cookie"
	MsgTestCookie   = "test_cookie"
	MsgLoginStart   = "login_start"
	MsgLoginStop    = "login_stop"
	MsgLoginCapture = "login_capture"
	MsgLoginNav     = "login_nav" // NavAction
	MsgMouse        = "mouse"     // MouseInput
	MsgKey          = "key"       // KeyInput
	MsgText         = "text"      // TextInput
)

// Envelope wraps every websocket message.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// RunRecord represents a single execution of the checker
type RunRecord struct {
	Timestamp       time.Time `json:"timestamp"`
	Duration        string    `json:"duration"`
	ItemsFound      int       `json:"items_found"`
	ItemsDownloaded int       `json:"items_downloaded"`
	LogSummary      string    `json:"log_summary"`
	Downloads       []string  `json:"downloads"`
}

// State is the full application state, re-sent whenever something changes.
type State struct {
	Running  bool         `json:"running"`
	Testing  bool         `json:"testing"`
	NextRun  *time.Time   `json:"next_run,omitempty"`
	LastRun  *RunRecord   `json:"last_run,omitempty"`
	Settings SettingsView `json:"settings"`
	Login    LoginStatus  `json:"login"`
}

// SettingsView is what the settings page shows. Cookie values never leave
// the server; only their metadata does.
type SettingsView struct {
	OutputDir       string     `json:"output_dir"`
	OutputDirLocked bool       `json:"output_dir_locked"` // set by the -o flag
	Interval        string     `json:"interval"`
	IntervalLocked  bool       `json:"interval_locked"` // set by the -interval flag
	HasPassword     bool       `json:"has_password"`    // -u and -p were given
	Cookie          CookieInfo `json:"cookie"`
}

// CookieInfo describes the session cookie used to log in.
type CookieInfo struct {
	Present   bool       `json:"present"`
	Source    string     `json:"source,omitempty"` // "saved" or "env"
	Names     []string   `json:"names,omitempty"`
	SavedAt   *time.Time `json:"saved_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"` // earliest non-session expiry
	Status    string     `json:"status,omitempty"`     // "valid", "rejected" or ""
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

// LoginStatus describes the remote browser used to log in to nCore.
type LoginStatus struct {
	Active  bool   `json:"active"`
	URL     string `json:"url,omitempty"`
	Message string `json:"message,omitempty"`
}

// LogLines carries one or more log lines.
type LogLines struct {
	Lines []string `json:"lines"`
	Reset bool     `json:"reset,omitempty"` // replace instead of append
}

// Frame is one JPEG screenshot of the remote login browser.
type Frame struct {
	Data   string  `json:"data"` // base64 JPEG
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Notice is a short message shown to the user.
type Notice struct {
	Level   string `json:"level"` // "info", "success" or "error"
	Message string `json:"message"`
}

// SettingsUpdate is sent when the user saves the general settings.
type SettingsUpdate struct {
	OutputDir string `json:"output_dir"`
	Interval  string `json:"interval"`
}

// CookieInput is a manually pasted "name=value; name2=value2" cookie string.
type CookieInput struct {
	Cookie string `json:"cookie"`
}

// NavAction is "back", "reload" or "home" for the remote login browser.
type NavAction struct {
	Action string `json:"action"`
}

// MouseInput is a mouse event on the remote browser. X and Y are relative to
// the screen, from 0 to 1.
type MouseInput struct {
	Type       string  `json:"type"` // mousePressed, mouseReleased, mouseMoved, mouseWheel
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Button     string  `json:"button,omitempty"`
	Buttons    int64   `json:"buttons,omitempty"`
	ClickCount int64   `json:"click_count,omitempty"`
	DeltaX     float64 `json:"delta_x,omitempty"`
	DeltaY     float64 `json:"delta_y,omitempty"`
	Modifiers  int64   `json:"modifiers,omitempty"`
}

// KeyInput is a key event on the remote browser.
type KeyInput struct {
	Type      string `json:"type"` // keyDown or keyUp
	Key       string `json:"key"`
	Code      string `json:"code"`
	Text      string `json:"text,omitempty"`
	KeyCode   int64  `json:"key_code"`
	Modifiers int64  `json:"modifiers,omitempty"`
}

// TextInput inserts text into the focused field (paste, mobile keyboards).
type TextInput struct {
	Text string `json:"text"`
}
