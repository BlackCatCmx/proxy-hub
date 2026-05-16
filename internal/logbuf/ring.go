package logbuf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type Logger struct {
	mu       sync.RWMutex
	entries  []Entry
	next     int
	full     bool
	filePath string
	fileOn   bool
	fileMax  int64
	subs     map[chan Entry]struct{}
}

func New(capacity int) *Logger {
	if capacity < 1 {
		capacity = 1
	}
	return &Logger{
		entries: make([]Entry, capacity),
		subs:    make(map[chan Entry]struct{}),
	}
}

func (l *Logger) ConfigureFile(path string, enabled bool, maxBytes int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.filePath = path
	l.fileOn = enabled
	l.fileMax = maxBytes
	if !enabled {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
}

func (l *Logger) Info(message string, fields map[string]string) {
	l.Add("INFO", message, fields)
}

func (l *Logger) Warn(message string, fields map[string]string) {
	l.Add("WARN", message, fields)
}

func (l *Logger) Error(message string, fields map[string]string) {
	l.Add("ERR", message, fields)
}

func (l *Logger) Add(level, message string, fields map[string]string) {
	entry := Entry{
		Time:    time.Now(),
		Level:   level,
		Message: message,
		Fields:  sanitizeFields(fields),
	}

	l.mu.Lock()
	l.entries[l.next] = entry
	l.next = (l.next + 1) % len(l.entries)
	if l.next == 0 {
		l.full = true
	}
	filePath := l.filePath
	fileOn := l.fileOn
	fileMax := l.fileMax
	subs := make([]chan Entry, 0, len(l.subs))
	for ch := range l.subs {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
		}
	}
	if fileOn {
		_ = appendFileLog(filePath, fileMax, entry)
	}
}

func (l *Logger) Recent(limit int) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 || limit > len(l.entries) {
		limit = len(l.entries)
	}
	var ordered []Entry
	if l.full {
		ordered = append(ordered, l.entries[l.next:]...)
		ordered = append(ordered, l.entries[:l.next]...)
	} else {
		ordered = append(ordered, l.entries[:l.next]...)
	}
	if len(ordered) > limit {
		ordered = ordered[len(ordered)-limit:]
	}
	out := make([]Entry, len(ordered))
	copy(out, ordered)
	return out
}

func (l *Logger) Subscribe() chan Entry {
	ch := make(chan Entry, 32)
	l.mu.Lock()
	l.subs[ch] = struct{}{}
	l.mu.Unlock()
	return ch
}

func (l *Logger) Unsubscribe(ch chan Entry) {
	l.mu.Lock()
	delete(l.subs, ch)
	close(ch)
	l.mu.Unlock()
}

func appendFileLog(path string, maxBytes int64, entry Entry) error {
	if maxBytes > 0 {
		if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
			if err := rotateLogFile(path); err != nil {
				return err
			}
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(file, "%s\n", line)
	return err
}

func rotateLogFile(path string) error {
	rotated := path + ".1"
	if err := os.Remove(rotated); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(path, rotated); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func sanitizeFields(fields map[string]string) map[string]string {
	if len(fields) == 0 {
		return nil
	}
	clean := make(map[string]string, len(fields))
	for key, value := range fields {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "pass") || strings.Contains(lower, "key") || strings.Contains(lower, "cookie") || strings.Contains(lower, "raw") || strings.Contains(lower, "token") {
			clean[key] = "***"
			continue
		}
		clean[key] = value
	}
	return clean
}
