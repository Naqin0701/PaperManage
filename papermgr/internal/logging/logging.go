// Package logging writes structured JSON logs with daily rotation.
package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger appends JSON lines to <dir>/YYYY-MM-DD.log.
type Logger struct {
	mu  sync.Mutex
	dir string
}

// New creates a logger rooted at dir.
func New(dir string) *Logger {
	l := &Logger{dir: dir}
	_ = os.MkdirAll(dir, 0o755)
	return l
}

// Log writes one event.
func (l *Logger) Log(level, event string, fields map[string]interface{}) {
	if l == nil {
		return
	}
	rec := map[string]interface{}{
		"ts":    time.Now().Format(time.RFC3339Nano),
		"level": level,
		"event": event,
	}
	for k, v := range fields {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	name := time.Now().Format("2006-01-02") + ".log"
	f, err := os.OpenFile(filepath.Join(l.dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// Info logs at info level.
func (l *Logger) Info(event string, fields map[string]interface{}) { l.Log("info", event, fields) }

// Warn logs at warn level.
func (l *Logger) Warn(event string, fields map[string]interface{}) { l.Log("warn", event, fields) }

// Error logs at error level.
func (l *Logger) Error(event string, fields map[string]interface{}) { l.Log("error", event, fields) }

// Tail returns the last n lines from today's and recent log files.
func (l *Logger) Tail(n int) []string {
	if l == nil {
		return nil
	}
	var lines []string
	entries, _ := os.ReadDir(l.dir)
	// Files are date-named; iterate and append the newest last.
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(l.dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range splitLines(string(data)) {
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// ParseLine renders a JSON log line compactly for the TUI.
func ParseLine(line string) string {
	var rec struct {
		TS     string `json:"ts"`
		Level  string `json:"level"`
		Event  string `json:"event"`
		Msg    string `json:"msg"`
		Plugin string `json:"plugin"`
		Err    string `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return line
	}
	s := rec.TS
	if len(s) > 19 {
		s = s[11:19]
	}
	if rec.Plugin != "" {
		s += " [" + rec.Plugin + "]"
	}
	s += " " + rec.Level + " " + rec.Event
	if rec.Msg != "" {
		s += ": " + rec.Msg
	}
	if rec.Err != "" {
		s += ": " + rec.Err
	}
	return s
}

// Sprintf is a tiny convenience for log messages.
func Sprintf(format string, a ...interface{}) string { return fmt.Sprintf(format, a...) }
