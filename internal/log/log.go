package log

import (
	"fmt"
	"io"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger struct {
	zap *zap.Logger
}

func New(w io.Writer) *Logger {
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(w),
		zapcore.InfoLevel,
	)

	return &Logger{zap: zap.New(core)}
}

func NewNop() *Logger {
	return &Logger{zap: zap.NewNop()}
}

func (l *Logger) Component(name string) *Logger {
	return l.With("component", name)
}

func (l *Logger) With(args ...any) *Logger {
	return &Logger{zap: l.zap.With(fields(args...)...)}
}

func (l *Logger) Info(msg string, args ...any) {
	l.zap.Info(msg, fields(args...)...)
}

func (l *Logger) Warn(msg string, args ...any) {
	l.zap.Warn(msg, fields(args...)...)
}

func (l *Logger) Error(msg string, args ...any) {
	l.zap.Error(msg, fields(args...)...)
}

func (l *Logger) Debug(msg string, args ...any) {
	l.zap.Debug(msg, fields(args...)...)
}

func (l *Logger) Sync() error {
	return l.zap.Sync()
}

func (l *Logger) Zap() *zap.Logger {
	return l.zap
}

func fields(args ...any) []zap.Field {
	if len(args) == 0 {
		return nil
	}

	result := make([]zap.Field, 0, (len(args)+1)/2)
	for i := 0; i < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			result = append(result, zap.Any("invalid_key", args[i]))
			if i+1 < len(args) {
				result = append(result, zap.Any("invalid_value", args[i+1]))
			}
			continue
		}

		if i+1 >= len(args) {
			result = append(result, zap.String(key, "<missing>"))
			continue
		}

		result = append(result, zap.Any(key, normalize(args[i+1])))
	}

	return result
}

func normalize(value any) any {
	switch v := value.(type) {
	case error:
		return v.Error()
	case fmt.Stringer:
		return v.String()
	default:
		return value
	}
}
