package gpiodriver

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/warthog618/go-gpiocdev"
	"periph.io/x/conn/v3"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/physic"
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

		if info.Name == "" {
			logger.Debug("skipping line without name")
			continue
		}

		logger.Debug("found line")
		pins = append(pins, newPinAdapter(chip, info, logger))
	}

	if err := pinreg.Register(strings.ToUpper(name), [][]pin.Pin{pins}); err != nil {
		return fmt.Errorf("failed to register gpiochip %q: %w", name, err)
	}

	for _, pin := range pins {
		adapter := pin.(*pinAdapter)
		if err := gpioreg.Register(adapter); err != nil {
			return fmt.Errorf("failed to register pin %q: %w", adapter.Name(), err)
		}
	}

	return nil
}

type pinAdapter struct {
	logger *slog.Logger                      // const
	chip   *gpiocdev.Chip                    // const
	edge   chan struct{}                     // const
	line   atomic.Pointer[gpiocdev.Line]     // const, nil if not opened
	info   atomic.Pointer[gpiocdev.LineInfo] // const
}

var (
	_ conn.Resource = (*pinAdapter)(nil)
	_ pin.Pin       = (*pinAdapter)(nil)
	_ pin.PinFunc   = (*pinAdapter)(nil)
	_ gpio.PinIn    = (*pinAdapter)(nil)
	_ gpio.PinOut   = (*pinAdapter)(nil)
)

func newPinAdapter(chip *gpiocdev.Chip, info gpiocdev.LineInfo, logger *slog.Logger) *pinAdapter {
	p := &pinAdapter{
		logger: logger,
		chip:   chip,
		edge:   make(chan struct{}),
	}
	p.info.Store(&info)
	return p
}

// initPin initializes the pin if it hasn't been initialized yet.
// The given options are only used IF THE PIN IS NOT ALREADY OPENED.
// True is returned if the pin is opened by this call, false if it was already
// opened.
func (p *pinAdapter) initPin(options ...gpiocdev.LineReqOption) (bool, error) {
	if line := p.line.Load(); line != nil {
		return false, nil
	}

	offset := p.info.Load().Offset
	options = append(options, gpiocdev.WithEventHandler(p.handleEvent))

	line, err := p.chip.RequestLine(offset, options...)
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
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to request line %d: %w", offset, err)
	}

	info, err := p.chip.WatchLineInfo(offset, p.handleInfoChange)
	if err != nil {
		return false, fmt.Errorf("failed to watch line %d info: %w", offset, err)
	}
	p.info.Store(&info)

	p.logger.Debug("line requested")
	return true, nil
}

func (p *pinAdapter) handleInfoChange(event gpiocdev.LineInfoChangeEvent) {
	p.info.Store(&event.Info)
}

func (p *pinAdapter) handleEvent(event gpiocdev.LineEvent) {
	switch event.Type {
	case gpiocdev.LineEventRisingEdge, gpiocdev.LineEventFallingEdge:
		select {
		case p.edge <- struct{}{}:
		default:
		}
	}
}

// String returns a human readable identifier representing this resource in a
// descriptive way for the user. It is the same signature as fmt.Stringer.
func (p *pinAdapter) String() string {
	info := p.info.Load()
	return fmt.Sprintf("%s/%s", p.chip.Name, info.Name)
}

// Halt stops the resource.
//
// Unlike a Conn, a Resource may not be closable, On the other hand, a
// resource can be halted. What halting entails depends on the resource
// device but it should stop motion, sensing loop, light emission or PWM
// output and go back into an inert state.
func (p *pinAdapter) Halt() error {
	line := p.line.Load()
	if line == nil {
		return nil
	}

	var errs []error

	if err := line.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close line: %w", err))
	}

	offset := p.info.Load().Offset
	if err := p.chip.UnwatchLineInfo(offset); err != nil {
		errs = append(errs, fmt.Errorf("failed to unwatch line %d info: %w", offset, err))
	}

	return errors.Join(errs...)
}

// Name returns the name of the pin.
func (p *pinAdapter) Name() string {
	info := p.info.Load()
	return info.Name
}

// Number returns the logical pin number or a negative number if the pin is
// not a GPIO, e.g. GROUND, V3_3, etc.
func (p *pinAdapter) Number() int {
	info := p.info.Load()
	return info.Offset
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
	return pin.Func(p.Name()).Generalize()
}

// SupportedFuncs returns the possible functions this pin support.
//
// Do not mutate the returned slice.
func (p *pinAdapter) SupportedFuncs() []pin.Func {
	return []pin.Func{
		gpio.IN,
		gpio.IN_HIGH,
		gpio.IN_LOW,
		gpio.OUT,
		gpio.OUT_OC,
		gpio.OUT_HIGH,
		gpio.OUT_LOW,
		gpio.FLOAT,
	}

	// funcs := []pin.Func{p.Func()}
	// info := p.info.Load()
	//
	// switch info.Config.Direction {
	// case gpiocdev.LineDirectionInput:
	// 	funcs = append(funcs, gpio.IN)
	//
	// 	switch info.Config.Bias {
	// 	case gpiocdev.LineBiasPullUp:
	// 		// Pull up means default high.
	// 		funcs = append(funcs, gpio.IN_HIGH)
	// 	case gpiocdev.LineBiasPullDown:
	// 		// Pull down means default low.
	// 		funcs = append(funcs, gpio.IN_LOW)
	// 	}
	//
	// case gpiocdev.LineDirectionOutput:
	// 	switch info.Config.Drive {
	// 	case gpiocdev.LineDrivePushPull:
	// 		// Drive aka push-pull.
	// 		funcs = append(funcs, gpio.OUT)
	// 	case gpiocdev.LineDriveOpenDrain:
	// 		// Open collector/drain aka open-drain.
	// 		funcs = append(funcs, gpio.OUT_OC)
	// 	}
	//
	// 	switch info.Config.Bias {
	// 	case gpiocdev.LineBiasPullUp:
	// 		funcs = append(funcs, gpio.OUT_HIGH)
	// 	case gpiocdev.LineBiasPullDown:
	// 		funcs = append(funcs, gpio.OUT_LOW)
	// 	case gpiocdev.LineBiasDisabled:
	// 		funcs = append(funcs, gpio.FLOAT)
	// 	}
	// }
	//
	// p.logger.Debug(
	// 	"pin supported functions",
	// 	"funcs", funcs)
	//
	// return []pin.Func{p.Func()}
}

// SetFunc sets the pin function.
//
// Example use is to reallocate a RPi3's GPIO14 active function between
// UART0_TX and UART1_TX.
func (p *pinAdapter) SetFunc(f pin.Func) error {
	_, err := p.initPin()
	if err != nil {
		return err
	}

	p.logger.Debug(
		"set pin function",
		"func", f)

	pin := p.line.Load()

	// https://github.com/periph/host/blob/522a3cb6e99e9649daf291bfb7b097219409a813/bcm283x/gpio.go#L319
	switch f {
	case gpio.IN:
		return p.In(gpio.PullNoChange, gpio.NoEdge)
	case gpio.IN_LOW:
		return p.In(gpio.PullDown, gpio.NoEdge)
	case gpio.IN_HIGH:
		return p.In(gpio.PullUp, gpio.NoEdge)
	case gpio.OUT:
		err = pin.Reconfigure(gpiocdev.AsOutput(), gpiocdev.AsPushPull)
	case gpio.OUT_OC:
		err = pin.Reconfigure(gpiocdev.AsOutput(), gpiocdev.AsOpenDrain)
	case gpio.OUT_HIGH:
		return p.Out(gpio.High)
	case gpio.OUT_LOW:
		return p.Out(gpio.Low)
	default:
		err = fmt.Errorf("unsupported function %q", f)
	}

	return err
}

func (p *pinAdapter) In(pull gpio.Pull, edge gpio.Edge) error {
	var cBias gpiocdev.LineBias
	switch pull {
	case gpio.PullNoChange:
		cBias = gpiocdev.WithBiasAsIs
	case gpio.PullUp:
		cBias = gpiocdev.WithPullUp
	case gpio.PullDown:
		cBias = gpiocdev.WithPullDown
	case gpio.Float:
		cBias = gpiocdev.WithBiasDisabled
	default:
		return fmt.Errorf("unsupported pull %q", pull)
	}

	var cEdge gpiocdev.LineEdge
	switch edge {
	case gpio.NoEdge:
		cEdge = gpiocdev.WithoutEdges
	case gpio.RisingEdge:
		cEdge = gpiocdev.WithRisingEdge
	case gpio.FallingEdge:
		cEdge = gpiocdev.WithFallingEdge
	case gpio.BothEdges:
		cEdge = gpiocdev.WithBothEdges
	default:
		return fmt.Errorf("unsupported edge %q", edge)
	}

	initialized, err := p.initPin(gpiocdev.AsInput, cBias, cEdge)
	if err != nil {
		return fmt.Errorf("failed to initialize pin: %w", err)
	}
	if initialized {
		return nil
	}

	pin := p.line.Load()
	if err := pin.Reconfigure(gpiocdev.AsInput, cBias, cEdge); err != nil {
		return fmt.Errorf("failed to configure pin as input: %w", err)
	}

	return nil
}

func (p *pinAdapter) Read() gpio.Level {
	pin := p.line.Load()
	// The GPIO package just returns Low if the pin is not opened yet.
	// https://github.com/periph/host/blob/522a3cb6e99e9649daf291bfb7b097219409a813/bcm283x/gpio.go#L478
	if pin == nil {
		p.logger.Error("reading from unopened pin")
		return gpio.Low
	}

	v, err := pin.Value()
	if err != nil {
		p.logger.Error(
			"failed to read pin",
			"err", err)
		return gpio.Low
	}

	// Check if any other bits are set. If they are, then we're not handling
	// them and should raise a warning about it.
	b := v & 0b1
	if v != b {
		p.logger.Warn(
			"kernel returned invalid non-boolean value",
			"value", v)
	}

	return gpio.Level(itob(b))
}

func (p *pinAdapter) ReadFast() gpio.Level {
	pin := p.line.Load()
	if pin == nil {
		return gpio.Low
	}
	v, _ := pin.Value()
	return gpio.Level(itob(v & 0b1))
}

func (p *pinAdapter) WaitForEdge(timeout time.Duration) bool {
	if timeout < 0 {
		<-p.edge
		return true
	}

	// If we're waiting for less than 100µs, just busy wait.
	if timeout < 1*time.Microsecond {
		// Busy wait.
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			select {
			case <-p.edge:
				return true
			default:
			}
		}
		return false
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-p.edge:
		return true
	case <-timer.C:
		return false
	}
}

func (p *pinAdapter) Pull() gpio.Pull {
	info := p.info.Load()
	switch info.Config.Bias {
	case gpiocdev.LineBiasPullUp:
		return gpio.PullUp
	case gpiocdev.LineBiasPullDown:
		return gpio.PullDown
	case gpiocdev.LineBiasDisabled:
		return gpio.Float
	default:
		return gpio.PullNoChange
	}
}

func (p *pinAdapter) DefaultPull() gpio.Pull {
	// TODO: Not sure what to put here.
	return gpio.PullNoChange
}

func (p *pinAdapter) Out(level gpio.Level) error {
	initialized, err := p.initPin(gpiocdev.AsOutput(btoi(bool(level))))
	if err != nil {
		return fmt.Errorf("failed to initialize pin: %w", err)
	}
	if initialized {
		// Already set value by initialization.
		return nil
	}

	pin := p.line.Load()

	info := p.info.Load()
	if info.Config.Direction != gpiocdev.LineDirectionOutput {
		if err := pin.Reconfigure(gpiocdev.AsOutput(btoi(bool(level)))); err != nil {
			return fmt.Errorf("failed to reconfigure pin as output: %w", err)
		}
		// Already set value by reconfiguration.
		return nil
	}

	// The pin is already an output, just set the value.
	if err := pin.SetValue(btoi(bool(level))); err != nil {
		return fmt.Errorf("failed to set pin value: %w", err)
	}

	return nil
}

func (p *pinAdapter) PWM(duty gpio.Duty, f physic.Frequency) error {
	return errors.New("not implemented")
}

func itob(i int) bool {
	// Use one of the optimization patterns.
	// See https://github.com/golang/go/issues/6011.
	if i == 1 {
		return true
	}
	return false
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
