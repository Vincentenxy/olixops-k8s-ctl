package logger

import (
	"context"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type contextKey string

const TraceIDKey contextKey = "traceId"

var base *zap.Logger
var sugared *zap.Logger

func Init(mode string) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey: "time", LevelKey: "level", NameKey: "logger", CallerKey: "caller",
		MessageKey: "msg", StacktraceKey: "stacktrace", LineEnding: zapcore.DefaultLineEnding,
		EncodeTime: func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02T15:04:05.000-07:00"))
		},
		EncodeCaller: zapcore.ShortCallerEncoder,
	}
	var core zapcore.Core
	if mode == "debug" || mode == "test" {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		core = zapcore.NewCore(zapcore.NewConsoleEncoder(encoderConfig), zapcore.AddSync(os.Stdout), zap.NewAtomicLevelAt(zap.DebugLevel))
	} else {
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
		core = zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(os.Stdout), zap.NewAtomicLevelAt(zap.InfoLevel))
	}
	base = zap.New(core, zap.AddCaller())
	sugared = base.WithOptions(zap.AddCallerSkip(2))
}

func L() *zap.Logger {
	if base == nil {
		base = zap.NewNop()
	}
	return base
}

func traceField(ctx context.Context) *zap.Field {
	if ctx == nil {
		return nil
	}
	var traceID string
	if gc, ok := ctx.(*gin.Context); ok {
		traceID = gc.GetString(string(TraceIDKey))
	} else {
		traceID, _ = ctx.Value(TraceIDKey).(string)
	}
	if traceID == "" {
		return nil
	}
	f := zap.String("traceId", traceID)
	return &f
}

func Info(ctx context.Context, msg string, fields ...zap.Field) {
	if sugared == nil {
		return
	}
	if f := traceField(ctx); f != nil {
		fields = append([]zap.Field{*f}, fields...)
	}
	sugared.Info(msg, fields...)
}

func Error(ctx context.Context, msg string, fields ...zap.Field) {
	if sugared == nil {
		return
	}
	if f := traceField(ctx); f != nil {
		fields = append([]zap.Field{*f}, fields...)
	}
	sugared.Error(msg, fields...)
}

func Sync() {
	if base != nil {
		_ = base.Sync()
	}
}
