package main

import (
	"flag"
	"log/slog"
	"os"

	"libdb.so/periph-gpioc/gpiodriver"
	"libdb.so/periph-gpioc/internal/xcli"
)

var jsonOutput bool

func main() {
	flag.BoolVar(&jsonOutput, "json", false, "output logs in JSON format")
	flag.Parse()

	if jsonOutput {
		slog.SetDefault(slog.New(slog.NewJSONHandler(
			os.Stdout,
			&slog.HandlerOptions{Level: slog.LevelDebug})))
	} else {
		slog.SetDefault(xcli.NewColoredLogger(
			os.Stdout,
			slog.LevelDebug))
	}

	if err := gpiodriver.Register(); err != nil {
		slog.Error("failed to register gpiochips", "err", err)
		os.Exit(1)
	}
}
