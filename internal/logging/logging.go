package logging

import (
	"fmt"
	"os"

	"go.uber.org/zap"
)

type LogService struct {
	Logger *zap.Logger
} // CONSIDERING SUGAR LOGGING, TRYING STRUCTURED FIRST

func NewLogService() (*LogService, error) {
	var logger *zap.Logger
	var err error

	if os.Getenv("GO_ENV") != "production" {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}
	if err != nil {
		return nil, err
	}

	return &LogService{Logger: logger}, nil
}

func (l *LogService) Info(msg string, fields ...zap.Field) {
	l.Logger.Info(msg, fields...)
}

func (l *LogService) Debug(msg string, fields ...zap.Field) {
	l.Logger.Debug(msg, fields...)
}

func (l *LogService) Warn(msg string, fields ...zap.Field) {
	l.Logger.Warn(msg, fields...)
}

func (l *LogService) Error(msg string, err error, fields ...zap.Field) {
	fields = append(fields, zap.Error(err))
	l.Logger.Error(msg, fields...)
}

func (l *LogService) Errorf(msg string, err error, fields ...zap.Field) error {
	fields = append(fields, zap.Error(err))
	l.Logger.Error(msg, fields...)
	return fmt.Errorf("Error: %v", err)
}

func (l *LogService) Printf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.Logger.Info(msg)
}

func (l *LogService) With(fields ...zap.Field) *LogService {
	return &LogService{
		Logger: l.Logger.With(fields...),
	}
}

// CALL ON EXIT (PROBABLY PUTTING THIS IN DEFER)
func (l *LogService) Sync() error {
	return l.Logger.Sync()
}
