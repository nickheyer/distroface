package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pkgcfg "github.com/nickheyer/distroface/pkg/config"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Logger struct {
	*log.Logger
	module     string
	fileLogger *lumberjack.Logger
	mu         sync.Mutex
	buffer     []string
	maxBuffer  int
	config     *Config
	children   []*Logger
}

type Config struct {
	Enabled       bool
	Dir           string
	DefaultModule string
	MaxSize       int
	MaxBackups    int
	MaxAge        int
	Compress      bool
}

func New() *Logger {
	return &Logger{
		Logger:    log.New(os.Stdout, "", 0),
		module:    "distroface",
		buffer:    make([]string, 0, 1000),
		maxBuffer: 1000,
	}
}

func NewWithConfig(cfg *Config) *Logger {
	module := cfg.DefaultModule

	writers := []io.Writer{os.Stdout}

	var fileLogger *lumberjack.Logger
	if cfg != nil && cfg.Enabled && cfg.Dir != "" {
		fileLogger = &lumberjack.Logger{
			Filename:   filepath.Join(cfg.Dir, module+".log"),
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		}
		writers = append(writers, fileLogger)
	}

	multiWriter := io.MultiWriter(writers...)

	return &Logger{
		Logger:     log.New(multiWriter, "", 0),
		module:     module,
		fileLogger: fileLogger,
		buffer:     make([]string, 0, 1000),
		maxBuffer:  1000,
		config:     cfg,
	}
}

// Module creates a child logger that writes to stdout and its own
// <dir>/<name>.log file using the same rotation settings as the root.
func (l *Logger) Module(name string) *Logger {
	writers := []io.Writer{os.Stdout}

	var fileLogger *lumberjack.Logger
	if l.config != nil && l.config.Enabled && l.config.Dir != "" {
		fileLogger = &lumberjack.Logger{
			Filename:   filepath.Join(l.config.Dir, name+".log"),
			MaxSize:    l.config.MaxSize,
			MaxBackups: l.config.MaxBackups,
			MaxAge:     l.config.MaxAge,
			Compress:   l.config.Compress,
		}
		writers = append(writers, fileLogger)
	}

	multiWriter := io.MultiWriter(writers...)

	child := &Logger{
		Logger:     log.New(multiWriter, "", 0),
		module:     name,
		fileLogger: fileLogger,
		buffer:     make([]string, 0, 1000),
		maxBuffer:  1000,
	}

	l.mu.Lock()
	l.children = append(l.children, child)
	l.mu.Unlock()

	return child
}

func (l *Logger) log(level, format string, args ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf(format, args...)
	logLine := fmt.Sprintf("[%s] [%s] %s: %s", timestamp, l.module, level, message)

	l.mu.Lock()
	l.buffer = append(l.buffer, logLine)
	if len(l.buffer) > l.maxBuffer {
		l.buffer = l.buffer[len(l.buffer)-l.maxBuffer:]
	}
	l.mu.Unlock()

	l.Printf("%s", logLine)
}

func (l *Logger) Info(format string, args ...any) {
	l.log("INFO", format, args...)
}

func (l *Logger) Error(format string, args ...any) {
	l.log("ERROR", format, args...)
}

func (l *Logger) Warn(format string, args ...any) {
	l.log("WARN", format, args...)
}

func (l *Logger) Debug(format string, args ...any) {
	l.log("DEBUG", format, args...)
}

func (l *Logger) Fatal(format string, args ...any) {
	l.log("FATAL", format, args...)
	os.Exit(1)
}

func (l *Logger) GetRecentLogs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	logs := make([]string, len(l.buffer))
	copy(logs, l.buffer)
	return logs
}

// Close closes this logger's file writer and all child module loggers.
func (l *Logger) Close() error {
	l.mu.Lock()
	children := l.children
	l.mu.Unlock()

	for _, child := range children {
		child.Close()
	}

	if l.fileLogger != nil {
		return l.fileLogger.Close()
	}
	return nil
}

// Write implements io.Writer, allowing the Logger to be used as an output
func (l *Logger) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		l.Info("%s", msg)
	}
	return len(p), nil
}

// GetLogFilePath returns the path to this logger's log file.
func (l *Logger) GetLogFilePath() string {
	if l.fileLogger != nil {
		return l.fileLogger.Filename
	}
	return ""
}

func Logv(cfg *pkgcfg.MigrateConfig, format string, args ...any) {
	if cfg.Verbose {
		fmt.Printf(format+"\n", args...)
	}
}
