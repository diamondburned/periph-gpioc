package driver

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/warthog618/go-gpiocdev"
	"periph.io/x/conn/v3"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/pin"
	"periph.io/x/conn/v3/pin/pinreg"
)

// Register initializes all available gpiochip devices.
func Register(options ...gpiocdev.ChipOption) error {
	var errs []error
	for _, name := range gpiocdev.Chips() {
		if err := RegisterChip(name); err != nil {
			errs = append(errs, fmt.Errorf("failed to initialize gpiochip %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// RegisterChip initializes the gpiochip device with the given name.
func RegisterChip(name string, options ...gpiocdev.ChipOption) error {
	chip, err := gpiocdev.NewChip(name, options...)
	if err != nil {
		return fmt.Errorf("failed to open gpiochip %q: %w", name, err)
	}

	logger := slog.Default().With(
		"driver", "gpiocdev",
		"chip", chip.Name)

	var pins []pin.Pin
	for line := range chip.Lines() {
		info, err := chip.LineInfo(line)
		if err != nil {
			return fmt.Errorf("failed to get line %d info: %w", line, err)
		}
		// time=2024-04-06T19:04:20.584-07:00 level=DEBUG msg="found line" chip=gpiochip0 driver=gpiocdev line=0 line.active_low=false line.direction=1 line.drive=0 line.bias=0 line.edge=0
		// time=2024-04-06T19:04:20.584-07:00 level=DEBUG msg="found line" chip=gpiochip0 driver=gpiocdev line=1 line.active_low=false line.direction=1 line.drive=0 line.bias=0 line.edge=0

		logger := logger.With(
			"line", line,
			"line.name", info.Name,
			"line.used", info.Used,
			"line.active_low", info.Config.ActiveLow,
			"line.direction", info.Config.Direction,
			"line.drive", info.Config.Drive,
			"line.bias", info.Config.Bias,
			"line.edge", info.Config.EdgeDetection)
		logger.Debug("found line")

		pins = append(pins, &pinAdapter{
			logger: logger,
			chip:   chip,
			info:   info,
		})
	}

	if err := pinreg.Register(strings.ToUpper(name), [][]pin.Pin{pins}); err != nil {
		return fmt.Errorf("failed to register gpiochip %q: %w", name, err)
	}

	return nil
}

type pinAdapter struct {
	logger *slog.Logger
	chip   *gpiocdev.Chip
	line   atomic.Pointer[gpiocdev.Line] // nil if not opened
	info   gpiocdev.LineInfo
}

var (
	_ conn.Resource = (*pinAdapter)(nil)
	_ pin.Pin       = (*pinAdapter)(nil)
	_ pin.PinFunc   = (*pinAdapter)(nil)
)

// String returns a human readable identifier representing this resource in a
// descriptive way for the user. It is the same signature as fmt.Stringer.
func (p *pinAdapter) String() string {
	return fmt.Sprintf("%s/%s", p.chip.Name, p.info.Name)
}

// Halt stops the resource.
//
// Unlike a Conn, a Resource may not be closable, On the other hand, a
// resource can be halted. What halting entails depends on the resource
// device but it should stop motion, sensing loop, light emission or PWM
// output and go back into an inert state.
func (p *pinAdapter) Halt() error {
	line := p.line.Load()
	if line != nil {
		return line.Close()
	}
	return nil
}

// Name returns the name of the pin.
func (p *pinAdapter) Name() string {
	return p.info.Name
}

// Number returns the logical pin number or a negative number if the pin is
// not a GPIO, e.g. GROUND, V3_3, etc.
func (p *pinAdapter) Number() int {
	return p.info.Offset
}

// Function returns a user readable string representation of what the pin is
// configured to do. Common case is In and Out but it can be bus specific pin
// name.
//
// Deprecated: Use PinFunc.Func. Will be removed in v4.
func (p *pinAdapter) Function() string {
	return string(p.Func())
}

// Func returns the pin's current function.
//
// The returned value may be specialized or generalized, depending on the
// actual port. For example it will likely be generalized for ports served
// over USB (like a FT232H with D0 set as SPI_MOSI) but specialized for
// ports on the base board (like a RPi3 with GPIO10 set as SPI0_MOSI).
func (p *pinAdapter) Func() pin.Func {
	return pin.Func(p.info.Name).Generalize()
}

// SupportedFuncs returns the possible functions this pin support.
//
// Do not mutate the returned slice.
func (p *pinAdapter) SupportedFuncs() []pin.Func {
	funcs := []pin.Func{p.Func()}

	switch p.info.Config.Direction {
	case gpiocdev.LineDirectionInput:
		funcs = append(funcs, gpio.IN)

		switch p.info.Config.Bias {
		case gpiocdev.LineBiasPullUp:
			// Pull up means default high.
			funcs = append(funcs, gpio.IN_HIGH)
		case gpiocdev.LineBiasPullDown:
			// Pull down means default low.
			funcs = append(funcs, gpio.IN_LOW)
		}

	case gpiocdev.LineDirectionOutput:
		switch p.info.Config.Drive {
		case gpiocdev.LineDrivePushPull:
			// Drive aka push-pull.
			funcs = append(funcs, gpio.OUT)
		case gpiocdev.LineDriveOpenDrain:
			// Open collector/drain aka open-drain.
			funcs = append(funcs, gpio.OUT_OC)
		}

		switch p.info.Config.Bias {
		case gpiocdev.LineBiasPullUp:
			funcs = append(funcs, gpio.OUT_HIGH)
		case gpiocdev.LineBiasPullDown:
			funcs = append(funcs, gpio.OUT_LOW)
		}
	}

	p.logger.Debug(
		"pin supported functions",
		"funcs", funcs)

	return []pin.Func{p.Func()}
}

// SetFunc sets the pin function.
//
// Example use is to reallocate a RPi3's GPIO14 active function between
// UART0_TX and UART1_TX.
func (p *pinAdapter) SetFunc(f pin.Func) error {
	if err := p.initPin(); err != nil {
		return err
	}

	p.logger.Debug(
		"set pin function",
		"func", f)

	pin := p.line.Load()
	var err error

	switch f {
	case gpio.IN:
		err = pin.Reconfigure(gpiocdev.AsInput)
	case gpio.IN_HIGH:
		err = pin.Reconfigure(gpiocdev.AsInput, gpiocdev.AsActiveHigh)
	case gpio.IN_LOW:
		err = pin.Reconfigure(gpiocdev.AsInput, gpiocdev.AsActiveLow)
	case gpio.OUT:
		err = pin.Reconfigure(gpiocdev.AsOutput(), gpiocdev.AsPushPull)
	case gpio.OUT_OC:
		err = pin.Reconfigure(gpiocdev.AsOutput(), gpiocdev.AsOpenDrain)
	case gpio.OUT_HIGH:
		err = pin.Reconfigure(gpiocdev.AsOutput(), gpiocdev.AsActiveHigh)
	case gpio.OUT_LOW:
		err = pin.Reconfigure(gpiocdev.AsOutput(), gpiocdev.AsActiveLow)
	default:
		err = fmt.Errorf("unsupported function %q", f)
	}

	return err
}

func (p *pinAdapter) initPin() error {
	if p.line.Load() != nil {
		return nil
	}

	line, err := p.chip.RequestLine(p.info.Offset)

	if err != nil {
		p.logger.Error(
			"failed to request line",
			"err", err)
	}

	if !p.line.CompareAndSwap(nil, line) {
		// Another goroutine beat us to it.
		// If we opened a line anyway, close it. Return nil so we can use the
		// actual line.
		if line != nil {
			p.logger.Debug(
				"line already opened, closing new one",
				"close_err", line.Close())
		}
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to request line %d: %w", p.info.Offset, err)
	}

	p.logger.Debug("line requested")
	return nil
}
