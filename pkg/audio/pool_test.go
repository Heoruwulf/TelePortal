/*
TelePortal: High-performance, zero-allocation bi-directional audio bridge.
Copyright (C) 2026 Mark Horila

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/
package audio

import (
	"testing"
)

func TestGetBuffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		size        int
		minCap      int
		expectedLen int
	}{
		{"Small 160", Size160, Size160, Size160},
		{"Medium 320", Size320, Size320, Size320},
		{"Large 640", Size640, Size640, Size640},
		{"XLarge 960", Size960, Size960, Size960},
		{"Default 4096", SizeDefault, SizeDefault, SizeDefault},
		{"Odd size 500", 500, Size640, 500},
		{"Very large", 8192, 8192, 8192},
		{"Zero size", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := GetBuffer(tt.size)
			if len(b) != tt.expectedLen {
				t.Errorf("GetBuffer(%d) len = %d; want %d", tt.size, len(b), tt.expectedLen)
			}
			if cap(b) < tt.minCap {
				t.Errorf("GetBuffer(%d) cap = %d; want at least %d", tt.size, cap(b), tt.minCap)
			}

			// Sanity check: Ensure we can write to the buffer
			if len(b) > 0 {
				b[0] = 0xFF
				b[len(b)-1] = 0xEE
			}

			// Return it
			PutBuffer(b)
		})
	}
}

func TestPutBuffer(t *testing.T) {
	t.Run("NilBuffer", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("PutBuffer(nil) panicked: %v", r)
			}
		}()
		PutBuffer(nil)
	})

	t.Run("EmptyBuffer", func(t *testing.T) {
		t.Parallel()
		PutBuffer([]byte{})
	})

	t.Run("SlicedBuffer", func(t *testing.T) {
		t.Parallel()
		// Create a buffer that looks like it had a header removed
		orig := make([]byte, SizeDefault)
		sliced := orig[12:]
		PutBuffer(sliced) // Should still go to poolDefault due to cap

		// Try to get it back
		b := GetBuffer(SizeDefault)
		if cap(b) < SizeDefault {
			t.Errorf("Expected buffer with cap %d, got %d", SizeDefault, cap(b))
		}
	})

	t.Run("RTPSlicingAllocation", func(t *testing.T) {
		// This test specifically reproduces the issue where slicing from the front
		// reduces capacity and causes subsequent allocations if not handled.

		// 1. Warm up the pool to ensure we are testing reuse, not initial allocation
		for range 10 {
			PutBuffer(make([]byte, SizeDefault))
		}

		allocs := testing.AllocsPerRun(100, func() {
			buf := GetBuffer(1500)
			// Simulate RTP slicing (pion/rtp style)
			sliced := buf[12:]
			PutBuffer(sliced)
		})

		if allocs > 1.0 {
			t.Errorf("RTPSlicingAllocation: expected at most 1 allocation (boxing), got %f", allocs)
		}
	})
}

func TestBufferConcurrency(t *testing.T) {
	t.Parallel()

	const workers = 10
	const iterations = 1000
	done := make(chan bool, workers)

	for range workers {
		go func() {
			for range iterations {
				b := GetBuffer(Size320)
				if len(b) != Size320 {
					panic("invalid len")
				}
				PutBuffer(b)

				b2 := GetBuffer(Size640)
				if len(b2) != Size640 {
					panic("invalid len")
				}
				PutBuffer(b2)
			}
			done <- true
		}()
	}

	for range workers {
		<-done
	}
}
