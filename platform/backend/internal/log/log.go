// Package log 提供基于 zap 的日志构造。
package log

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New 按级别构造 zap.Logger；失败时返回 no-op logger。
func New(level string) *zap.Logger {
	cfg := zap.NewDevelopmentConfig()
	cfg.EncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	switch level {
	case "debug":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case "info":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	case "warn":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case "error":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	}
	l, err := cfg.Build()
	if err != nil {
		nop := zap.NewNop()
		zap.ReplaceGlobals(nop) // 失败也设全局，保证 zap.L() 永不 nil
		return nop
	}
	zap.ReplaceGlobals(l) // 业务代码用 zap.L() 即可
	return l
}
