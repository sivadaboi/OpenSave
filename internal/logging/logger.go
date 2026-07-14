// Package logging provides the daemon's activity log: a bounded in-memory
// ring of recent entries (served to the dashboard's Activity Log panel on
// connect) plus a subscriber hook for live broadcast.
package logging

import (
	"fmt"
	"os"
	"sync"
	"time"
)

const historyLimit = 200

// maxLogFileBytes caps the on-disk log before it rotates to <path>.old —
// enough history to diagnose an issue without ever growing unbounded.
const maxLogFileBytes = 2 << 20 // 2 MiB

// Entry is one activity-log line.
type Entry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"` // info | warn | error | success
	Message   string `json:"message"`
}

// Logger is safe for concurrent use.
type Logger struct {
	mu          sync.Mutex
	history     []Entry
	subscribers []func(Entry)
	file        *os.File
	filePath    string
	fileSize    int64
}

// New creates a Logger.
func New() *Logger {
	return &Logger{}
}

// Log records an entry and notifies subscribers.
func (l *Logger) Log(level, message string) {
	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Message:   message,
	}

	l.mu.Lock()
	l.history = append(l.history, entry)
	if len(l.history) > historyLimit {
		l.history = l.history[len(l.history)-historyLimit:]
	}
	l.writeFileLocked(entry)
	subs := make([]func(Entry), len(l.subscribers))
	copy(subs, l.subscribers)
	l.mu.Unlock()

	for _, sub := range subs {
		sub(entry)
	}
}

// AttachFile mirrors every entry to an append-only log file so problems
// that kill the dashboard (daemon boot failures, unreachable API) are
// still diagnosable after the fact. Rotates to <path>.old past 2 MiB.
// Errors are non-fatal: worst case the app just has no file log.
func (l *Logger) AttachFile(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	size := int64(0)
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	l.mu.Lock()
	if l.file != nil {
		l.file.Close()
	}
	l.file = f
	l.filePath = path
	l.fileSize = size
	l.mu.Unlock()
}

// Close releases the log file, if any.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

func (l *Logger) writeFileLocked(entry Entry) {
	if l.file == nil {
		return
	}
	n, err := fmt.Fprintf(l.file, "%s [%s] %s\n", entry.Timestamp, entry.Level, entry.Message)
	if err != nil {
		return
	}
	l.fileSize += int64(n)
	if l.fileSize <= maxLogFileBytes {
		return
	}
	// Rotate: current becomes .old (replacing any previous .old).
	l.file.Close()
	l.file = nil
	_ = os.Remove(l.filePath + ".old")
	_ = os.Rename(l.filePath, l.filePath+".old")
	f, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	l.file = f
	l.fileSize = 0
}

// History returns a copy of the retained entries, oldest first.
func (l *Logger) History() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.history))
	copy(out, l.history)
	return out
}

// Subscribe registers a live-entry callback (e.g. the dashboard WS hub).
func (l *Logger) Subscribe(fn func(Entry)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.subscribers = append(l.subscribers, fn)
}
