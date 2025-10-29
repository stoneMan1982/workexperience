package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/stoneMan1982/workexperience/practice/golang/pkg/config"
	"github.com/stoneMan1982/workexperience/practice/golang/pkg/logx"
)

func main() {

	var permission uint32 = 234749951
	const bitCreateGroupPerm = 5
	mask := uint32(1) << bitCreateGroupPerm

	hasCreateGroupPerm := (permission & mask) != 0
	fmt.Println("CreateGroupPerm:", hasCreateGroupPerm) // true
	fmt.Println("Bit value:", (permission>>bitCreateGroupPerm)&1)

	cfg, err := config.LoadConfig("./config.yaml")
	if err != nil {
		logx.Setup("info", "json", false)
		slog.Error("load config failed", "err", err)
		return
	}

	logx.Setup(cfg.Logging.Level, cfg.Logging.Format, cfg.Logging.AddSource)

	slog.Info("config loaded",
		"dialect", cfg.Database.Dialect,
		"host", cfg.Database.Host,
		"db", cfg.Database.DBName,
		"log_level", cfg.Logging.Level,
		"log_format", cfg.Logging.Format,
	)

	// Demo: different levels; only error/fatal should include source (when add_source=true).
	slog.Debug("debug message")
	slog.Info("info message")
	slog.Warn("warn message")
	slog.Error("error message with source")

	// Demo: fatal (opt-in via env to avoid always exiting during development)
	if os.Getenv("DEMO_FATAL") == "1" {
		logx.Fatal("fatal demo: exiting process now", "hint", "unset DEMO_FATAL to skip")
	}
}
