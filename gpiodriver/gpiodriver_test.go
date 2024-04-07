package gpiodriver_test

import (
	"os"
	"testing"

	"libdb.so/periph-gpioc/gpiodriver"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
)

func BenchmarkPinAdapter(b *testing.B) {
	testPin := os.Getenv("PIN")
	if testPin == "" {
		b.Skip("$PIN environment variable not set")
	}

	if err := gpiodriver.Register(); err != nil {
		b.Fatalf("failed to register driver: %v", err)
	}

	pin := gpioreg.ByName(testPin)
	if pin == nil {
		b.Fatalf("failed to find pin %q", testPin)
	}

	b.Run("In", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := pin.In(gpio.PullUp, gpio.NoEdge); err != nil {
				b.Fatalf("failed to set pin to input: %v", err)
			}
		}
	})

	b.Run("In/Read", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pin.Read()
		}
	})

	b.Run("Out", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := pin.Out(gpio.Low); err != nil {
				b.Fatalf("failed to set pin to output: %v", err)
			}
		}
	})
}
