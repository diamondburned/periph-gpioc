package main

import (
	"flag"
	"log/slog"
	"os"

	"libdb.so/periph-gpioc/driver"
)

var jsonOutput bool

func main() {
	flag.BoolVar(&jsonOutput, "json", false, "output logs in JSON format")
	flag.Parse()

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}

	var logHandler slog.Handler
	if jsonOutput {
		logHandler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		logHandler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	if err := driver.Register(); err != nil {
		logger.Error("failed to register gpiochips", "err", err)
	}
}
