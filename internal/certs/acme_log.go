package certs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nickheyer/distroface/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Forwards acme client entries into the app log
type logBridge struct {
	log    *logger.Logger
	fields []zapcore.Field
}

func acmeLogger(log *logger.Logger) *zap.Logger {
	if log == nil {
		return zap.NewNop()
	}
	return zap.New(logBridge{log: log})
}

func (b logBridge) Enabled(level zapcore.Level) bool {
	return level >= zapcore.InfoLevel
}

func (b logBridge) With(fields []zapcore.Field) zapcore.Core {
	return logBridge{log: b.log, fields: append(b.fields[:len(b.fields):len(b.fields)], fields...)}
}

func (b logBridge) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if b.Enabled(entry.Level) {
		return checked.AddCore(entry, b)
	}
	return checked
}

func (b logBridge) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range b.fields {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}
	keys := make([]string, 0, len(enc.Fields))
	for k := range enc.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var line strings.Builder
	if entry.LoggerName != "" {
		line.WriteString(entry.LoggerName + " ")
	}
	line.WriteString(entry.Message)
	for _, k := range keys {
		fmt.Fprintf(&line, " %s=%v", k, enc.Fields[k])
	}
	switch {
	case entry.Level >= zapcore.ErrorLevel:
		b.log.Error("%s", line.String())
	case entry.Level == zapcore.WarnLevel:
		b.log.Warn("%s", line.String())
	default:
		b.log.Info("%s", line.String())
	}
	return nil
}

func (b logBridge) Sync() error {
	return nil
}
