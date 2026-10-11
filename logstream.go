package main

import (
	"strings"
	"sync"

	"ncore_automation/api"
)

const logBacklog = 1000

// LogStream is an io.Writer that keeps the last log lines and pushes new
// ones to the web interface.
type LogStream struct {
	mu      sync.Mutex
	lines   []string
	partial string
	hub     *Hub
}

func (l *LogStream) Write(p []byte) (int, error) {
	l.mu.Lock()
	text := l.partial + string(p)
	parts := strings.Split(text, "\n")
	l.partial = parts[len(parts)-1]
	newLines := parts[:len(parts)-1]
	l.lines = append(l.lines, newLines...)
	if over := len(l.lines) - logBacklog; over > 0 {
		l.lines = append([]string(nil), l.lines[over:]...)
	}
	l.mu.Unlock()

	if len(newLines) > 0 {
		l.hub.Broadcast(api.MsgLog, api.LogLines{Lines: newLines})
	}
	return len(p), nil
}

// Backlog returns a copy of the stored lines.
func (l *LogStream) Backlog() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}
