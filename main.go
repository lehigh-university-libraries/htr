package main

import (
	"context"
	"os"

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

	if err := fang.Execute(context.Background(), cmd.RootCmd); err != nil {
		os.Exit(1)
	}
}
