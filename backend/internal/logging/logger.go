package logging

import (
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/yourusername/hytale-server-manager/internal/config"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	logger    *slog.Logger
	initOnce  sync.Once
	logCloser io.Closer
)

// Init configures the global logger singleton.
func Init(cfg config.LoggingConfig) (*slog.Logger, error) {
	var initErr error

	initOnce.Do(func() {
		level := parseLevel(cfg.Level)
		output, closer := buildOutput(cfg)
		if closer != nil {
			logCloser = closer
		}

		options := &slog.HandlerOptions{Level: level, AddSource: true}
		var handler slog.Handler
		if strings.EqualFold(cfg.Format, "text") {
			handler = slog.NewTextHandler(output, options)
		} else {
			handler = slog.NewJSONHandler(output, options)
		}

		logger = slog.New(handler)
		slog.SetDefault(logger)
		log.SetFlags(0)
		log.SetOutput(slogWriter{logger: logger})
	})

	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	return logger, initErr
}

// L returns the configured logger, or a no-op logger if not initialized.
func L() *slog.Logger {
	if logger == nil {
		return slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return logger
}

// Close flushes and closes any logger resources.
func Close() error {
	if logCloser != nil {
		return logCloser.Close()
	}
	return nil
}

type slogWriter struct {
	logger *slog.Logger
}

func (w slogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return len(p), nil
	}
	w.logger.Info(msg)
	return len(p), nil
}

func buildOutput(cfg config.LoggingConfig) (io.Writer, io.Closer) {
	if strings.TrimSpace(cfg.File) == "" {
		return os.Stdout, nil
	}

	fileLogger := &lumberjack.Logger{
		Filename:   cfg.File,
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   true,
	}

	return io.MultiWriter(os.Stdout, fileLogger), fileLogger
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}
