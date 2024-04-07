package xcli

import (
	"io"
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
)

type WritableFd interface {
	io.Writer
	Fd() uintptr
}

// NewColoredLogger sets the global slog.Logger to use colored logging.
// If $NO_COLOR is set, it will disable colored logging.
func NewColoredLogger(o WritableFd, l slog.Leveler) *slog.Logger {
	handler := tint.NewHandler(o, &tint.Options{
		Level:   l,
		NoColor: os.Getenv("NO_COLOR") != "" && isatty.IsTerminal(o.Fd()),
	})
	logger := slog.New(handler)
	return logger
}
