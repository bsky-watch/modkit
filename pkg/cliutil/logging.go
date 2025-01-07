package cliutil

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/rs/zerolog"
	slogzerolog "github.com/samber/slog-zerolog"
)

type LoggingConfig struct {
	LogFile   string
	LogFormat string `default:"text"`
	LogLevel  int64  `default:"1"`
}

func RegisterLoggingFlags(config *LoggingConfig) {
	flag.StringVar(&config.LogFile, "log", "", "Path to the log file. If empty, will log to stderr")
	flag.StringVar(&config.LogFormat, "log-format", "text", "Logging format. 'text' or 'json'")
	flag.Int64Var(&config.LogLevel, "log-level", 1, "Log level. -1 - trace, 0 - debug, 1 - info, 5 - panic")
}

func SetupLogging(ctx context.Context, config *LoggingConfig) context.Context {
	logFile := os.Stderr

	if config.LogFile != "" {
		f, err := os.OpenFile(config.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("Failed to open the specified log file %q: %s", config.LogFile, err)
		}
		logFile = f
	}

	var output io.Writer

	switch config.LogFormat {
	case "json":
		output = logFile
	case "text":
		prefixList := []string{}
		info, ok := debug.ReadBuildInfo()
		if ok {
			prefixList = append(prefixList, info.Path+"/")
		}

		basedir := ""
		_, sourceFile, _, ok := runtime.Caller(0)
		if ok {
			basedir = filepath.Dir(sourceFile)
		}

		if basedir != "" {
			prefixList = append(prefixList, basedir+"/")
			head, _ := filepath.Split(basedir)
			for head != "/" && head != "" {
				prefixList = append(prefixList, head)
				head, _ = filepath.Split(strings.TrimSuffix(head, "/"))
			}
		}

		output = zerolog.ConsoleWriter{
			Out:        logFile,
			NoColor:    true,
			TimeFormat: time.RFC3339,
			PartsOrder: []string{
				zerolog.LevelFieldName,
				zerolog.TimestampFieldName,
				zerolog.CallerFieldName,
				zerolog.MessageFieldName,
			},
			FormatFieldName:  func(i interface{}) string { return fmt.Sprintf("%s:", i) },
			FormatFieldValue: func(i interface{}) string { return fmt.Sprintf("%s", i) },
			FormatCaller: func(i interface{}) string {
				s := i.(string)
				for _, p := range prefixList {
					s = strings.TrimPrefix(s, p)
				}
				return s
			},
		}
	default:
		log.Fatalf("Invalid log format specified: %q", config.LogFormat)
	}

	logger := zerolog.New(output).Level(zerolog.Level(config.LogLevel)).With().Caller().Timestamp().Logger()

	ctx = logger.WithContext(ctx)

	zerolog.DefaultContextLogger = &logger

	slogLevel := slog.LevelDebug
	switch zerolog.Level(config.LogLevel) {
	case zerolog.DebugLevel, zerolog.TraceLevel:
		slogLevel = slog.LevelDebug
	case zerolog.InfoLevel:
		slogLevel = slog.LevelInfo
	case zerolog.WarnLevel:
		slogLevel = slog.LevelWarn
	case zerolog.ErrorLevel, zerolog.FatalLevel, zerolog.PanicLevel:
		slogLevel = slog.LevelError
	}

	slogger := slog.New(slogzerolog.Option{Level: slogLevel, Logger: &logger}.NewZerologHandler())
	slog.SetDefault(slogger)

	return ctx
}
