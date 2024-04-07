package main

import (
	"log/slog"
	"os"

	"libdb.so/periph-gpioc/driver"
)

func main() {
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	if err := driver.Register(); err != nil {
		logger.Error("failed to register gpiochips", "err", err)
	}
}
