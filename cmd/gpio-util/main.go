package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"libdb.so/periph-gpioc/gpiodriver"
	"libdb.so/periph-gpioc/internal/xcli"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
)

var verbose bool

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usages:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags] get <pin>\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  %s [flags] set <pin> <value>\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.BoolVar(&verbose, "v", false, "enable verbose logging")
	flag.Parse()

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	logger := xcli.NewColoredLogger(os.Stderr, level)
	slog.SetDefault(logger)

	os.Exit(run())
}

func run() int {
	switch flag.Arg(0) {
	case "dump", "get", "set":
		if err := gpiodriver.Register(); err != nil {
			slog.Error("failed to register gpiochips", "err", err)
			return 1
		}

		if flag.Arg(0) == "dump" {
			return 0
		}

		pin := gpioreg.ByName(flag.Arg(1))
		if pin == nil {
			slog.Error("invalid pin", "pin", flag.Arg(1))
			return 1
		}

		switch flag.Arg(0) {
		case "get":
			if err := pin.In(gpio.PullNoChange, gpio.NoEdge); err != nil {
				slog.Error("failed to set pin as input", "err", err)
				return 1
			}

			switch pin.Read() {
			case gpio.High:
				fmt.Println("1")
			case gpio.Low:
				fmt.Println("0")
			}

		case "set":
			value, err := strconv.ParseBool(flag.Arg(2))
			if err != nil {
				slog.Error("failed to parse value", "err", err)
				return 1
			}

			if err := pin.Out(gpio.Level(value)); err != nil {
				slog.Error("failed to set pin as output", "err", err)
				return 1
			}
		}

		return 0
	}

	flag.Usage()
	slog.Error("invalid command", "cmd", flag.Arg(0))
	return 1
}
