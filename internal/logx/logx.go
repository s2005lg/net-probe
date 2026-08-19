package logx

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

type Logger struct {
	mu    sync.Mutex
	level Level
	out   io.Writer
}

func New(level string) *Logger {
	return &Logger{level: ParseLevel(level), out: os.Stderr}
}

func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w == nil {
		w = io.Discard
	}
	l.out = w
}

func (l *Logger) log(lv Level, format string, args ...any) {
	if lv < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tag := map[Level]string{
		LevelDebug: "DEBUG",
		LevelInfo:  "INFO",
		LevelWarn:  "WARN",
		LevelError: "ERROR",
	}[lv]
	fmt.Fprintf(l.out, "%s %-5s %s\n", time.Now().Format(time.RFC3339), tag, fmt.Sprintf(format, args...))
}

func (l *Logger) Debugf(format string, args ...any) { l.log(LevelDebug, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.log(LevelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.log(LevelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.log(LevelError, format, args...) }
