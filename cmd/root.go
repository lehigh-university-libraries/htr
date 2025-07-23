package cmd

import (
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var RootCmd = &cobra.Command{
	Use:   "htr",
	Short: "Handwritten Text Recognition",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		level := slog.LevelInfo
		ll, err := cmd.Flags().GetString("log-level")
		if err != nil {
			return err
		}

		switch strings.ToUpper(ll) {
		case "DEBUG":
			level = slog.LevelDebug
		case "WARN":
			level = slog.LevelWarn
		case "ERROR":
			level = slog.LevelError
		}

		opts := &slog.HandlerOptions{
			Level: level,
		}
		handler := slog.New(slog.NewTextHandler(os.Stdout, opts))
		slog.SetDefault(handler)

		return nil
	},
}

func Execute() {
	err := RootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	ll := os.Getenv("LOG_LEVEL")
	if ll == "" {
		ll = "INFO"
	}
	RootCmd.PersistentFlags().String("log-level", ll, "The logging level for the command")
}
