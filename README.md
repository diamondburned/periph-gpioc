# periph-gpiod

An adapter implementing [periph.io](https://periph.io) interfaces using
[gpiocdev](https://github.com/warthog618/go-gpiocdev). This adapter allows
periph.io libraries to be used on any Linux systems that support GPIO without
requiring vendor-specific kernels such as Raspberry Pi's or Broadcom's.

## Usage

```go
import (
    "libdb.so/periph-gpioc/gpiodriver"
    "periph.io/x/conn/v3/gpio"
    "periph.io/x/conn/v3/gpioreg"
)

func main() {
    if err := gpiodriver.Register(); err != nil {
        log.Fatalf("failed to initialize gpiodriver: %v", err)
    }

    pin := gpioreg.ByName("GPIO27")
    if pin == nil {
        log.Fatalf("failed to find pin")
    }

    // Set the pin as output to low.
    if err := pin.Out(gpio.Low); err != nil {
        log.Fatalf("failed to set pin as output: %v", err)
    }
}
```

> [!NOTE]
> Here, we skipped using `host.Init()` from
> [periph.io/x/host](https://periph.io/x/host) and instead call our
> `gpiodriver.Register()`. Calling both may cause conflicts.
