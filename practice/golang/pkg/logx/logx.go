package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Options controls logger initialization.
type Options struct {
	// Level controls log level. If zero, defaults to Info.
	Level slog.Level
	// JSON selects JSON or Text handler.
	JSON bool
	// AddSource toggles source locations in output.
	AddSource bool
	// SetDefault also sets slog.SetDefault to this logger when true.
	SetDefault bool
}

// LevelFatal is a custom level above ERROR. Handlers will print it as "FATAL".
const LevelFatal slog.Level = slog.LevelError + 4

// Init builds a slog logger with the given options. If opts.SetDefault is true,
// the created logger is set as the global default.
func Init(opts Options) *slog.Logger {
	h := handler(opts)
	l := slog.New(h)
	if opts.SetDefault {
		slog.SetDefault(l)
	}
	return l
}

// SetupFromEnv initializes a default logger from environment variables and sets it as default.
//
// Env vars:
//   - LOG_LEVEL: debug|info|warn|error (default: info)
//   - LOG_FORMAT: json|text (default: json)
//   - LOG_ADD_SOURCE: 1/true to enable AddSource (default: disabled)
func SetupFromEnv() *slog.Logger {
	lvl := parseLevel(envOr("LOG_LEVEL", "info"))
	json := strings.EqualFold(envOr("LOG_FORMAT", "json"), "json")
	addSrc := parseBool(envOr("LOG_ADD_SOURCE", ""))
	return Init(Options{Level: lvl, JSON: json, AddSource: addSrc, SetDefault: true})
}

// Setup initializes the global logger from string options (typically from config file).
// level: debug|info|warn|error; format: json|text; addSource: include source location.
func Setup(level, format string, addSource bool) *slog.Logger {
	lvl := parseLevel(level)
	json := strings.EqualFold(strings.TrimSpace(format), "json")
	return Init(Options{Level: lvl, JSON: json, AddSource: addSource, SetDefault: true})
}

func handler(opts Options) slog.Handler {
	optsLevel := &slog.LevelVar{}
	if opts.Level == 0 {
		optsLevel.Set(slog.LevelInfo)
	} else {
		optsLevel.Set(opts.Level)
	}

	// Base handler does NOT add source automatically; we'll add it conditionally.
	hOpts := &slog.HandlerOptions{
		Level:     optsLevel,
		AddSource: false,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Map our custom fatal level to a readable string label.
			if a.Key == slog.LevelKey {
				if lv, ok := a.Value.Any().(slog.Level); ok {
					if lv == LevelFatal {
						a.Value = slog.StringValue("FATAL")
					}
				}
			}
			return a
		},
	}
	var base slog.Handler
	if opts.JSON {
		base = slog.NewJSONHandler(os.Stdout, hOpts)
	} else {
		base = slog.NewTextHandler(os.Stdout, hOpts)
	}
	if opts.AddSource {
		return &conditionalSourceHandler{h: base}
	}
	return base
}

func parseLevel(s string) slog.Level {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	case "fatal":
		return LevelFatal
	default:
		return slog.LevelInfo
	}
}

func parseBool(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "1" || s == "true" || s == "yes" || s == "y"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Fatal logs with FATAL level and then calls os.Exit(1).
func Fatal(msg string, args ...any) {
	slog.Log(context.Background(), LevelFatal, msg, args...)
	os.Exit(1)
}

// FatalContext logs with FATAL level with a context and then calls os.Exit(1).
func FatalContext(ctx context.Context, msg string, args ...any) {
	slog.Log(ctx, LevelFatal, msg, args...)
	os.Exit(1)
}

// conditionalSourceHandler wraps a handler and injects a "source" attribute
// (file:line) only for records with level >= Error.
type conditionalSourceHandler struct{ h slog.Handler }

func (c *conditionalSourceHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return c.h.Enabled(ctx, l)
}

func (c *conditionalSourceHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		file, line := callerFileLine()
		if file != "" {
			r.AddAttrs(slog.String("source", fmt.Sprintf("%s:%d", file, line)))
		}
	}
	return c.h.Handle(ctx, r)
}

func (c *conditionalSourceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &conditionalSourceHandler{h: c.h.WithAttrs(attrs)}
}

func (c *conditionalSourceHandler) WithGroup(name string) slog.Handler {
	return &conditionalSourceHandler{h: c.h.WithGroup(name)}
}

// callerFileLine tries to find the first caller outside slog and this package.
func callerFileLine() (string, int) {
	const maxDepth = 16
	pcs := make([]uintptr, maxDepth)
	n := runtime.Callers(3, pcs) // skip [Callers, callerFileLine, Handle]
	frames := runtime.CallersFrames(pcs[:n])
	for {
		fr, more := frames.Next()
		// skip runtime and slog internals and this package
		if !strings.Contains(fr.Function, "log/slog") && !strings.Contains(fr.Function, "/pkg/logx.") && !strings.Contains(fr.Function, "runtime.") {
			return filepath.Base(fr.File), fr.Line
		}
		if !more {
			break
		}
	}
	return "", 0
}
