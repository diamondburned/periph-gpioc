package xtime

import (
	"testing"
	"time"
)

func BenchmarkNow(b *testing.B) {
	for i := 0; i < b.N; i++ {
		time.Now()
	}
}

func BenchmarkTimer(b *testing.B) {
	for i := 0; i < b.N; i++ {
		time.NewTimer(0).Stop()
	}
}
