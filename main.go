package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/charmbracelet/fang"
	"github.com/joho/godotenv"
	"github.com/lehigh-university-libraries/htr/cmd"
	"github.com/lehigh-university-libraries/htr/internal/utils"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		utils.ExitOnError("Error loading .env file", err)
	}

	level := getLogLevel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(logger)

	if err := fang.Execute(context.Background(), cmd.RootCmd); err != nil {
		os.Exit(1)
	}
}

func getLogLevel() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
