// Package log 提供基于 zap 的日志构造（支持 console/json、stdout/file、lumberjack 滚动、error.log 独立）。
package log

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Config 日志配置。
type Config struct {
	Level      string // debug / info / warn / error
	Format     string // console / json
	Output     string // stdout / file
	File       string // 主日志文件（output=file 时）
	ErrorFile  string // error.log（Error+ 独立汇总）
	MaxSizeMB  int    // 单文件最大 MB（lumberjack 滚动）
	MaxBackups int    // 保留旧文件数
	MaxAgeDays int    // 保留天数
}

// New 按 Config 构造 zap.Logger 并设全局（zap.L() 可用）。
//   Core A：全级别 → stdout 或 File（lumberjack 滚动）
//   Core B（若 ErrorFile 非空）：Error+ → ErrorFile 独立，便于告警/快查
func New(c Config) *zap.Logger {
	level := parseLevel(c.Level)
	encoder := newEncoder(c.Format)

	// Core A：全级
	var syncer zapcore.WriteSyncer
	if c.Output == "file" && c.File != "" {
		syncer = zapcore.AddSync(newRoller(c.File, c.MaxSizeMB, c.MaxBackups, c.MaxAgeDays))
	} else {
		syncer = zapcore.Lock(os.Stderr) // 默认 stderr（容器 stdout 收集）
	}
	cores := []zapcore.Core{zapcore.NewCore(encoder, syncer, level)}

	// Core B：Error+ → error.log 独立
	if c.ErrorFile != "" {
		errSyncer := zapcore.AddSync(newRoller(c.ErrorFile, c.MaxSizeMB, c.MaxBackups, c.MaxAgeDays))
		cores = append(cores, zapcore.NewCore(encoder, errSyncer, zapcore.ErrorLevel))
	}

	logger := zap.New(zapcore.NewTee(cores...))
	zap.ReplaceGlobals(logger)
	return logger
}

// newRoller 构造 lumberjack 滚动写入器；MaxSize<=0 时用默认 100。
func newRoller(path string, maxMB, maxBackups, maxAge int) *lumberjack.Logger {
	if maxMB <= 0 {
		maxMB = 100
	}
	return &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxMB,
		MaxBackups: maxBackups,
		MaxAge:     maxAge,
		LocalTime:  true,
	}
}

// parseLevel 字符串 → zapcore.Level（默认 info）。
func parseLevel(s string) zapcore.Level {
	switch s {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// newEncoder 按 format 构造 encoder（json 或 console，默认 console）。
func newEncoder(format string) zapcore.Encoder {
	cfg := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	if format == "json" {
		return zapcore.NewJSONEncoder(cfg)
	}
	return zapcore.NewConsoleEncoder(cfg)
}
