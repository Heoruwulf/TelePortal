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
package rtp

import (
	"testing"

	audiopool "github.com/heoruwulf/teleportal/pkg/audio"
)

func TestDTMFGenerator(t *testing.T) {
	t.Parallel()

	g := NewDTMFGenerator(8000, 20)

	// Generate for digit '1', duration 160ms
	// 160ms at 8000Hz = 1280 samples
	packets := g.Generate("1", 160)

	if len(packets) < 3 {
		t.Errorf("expected at least 3 packets, got %d", len(packets))
	}

	// Verify first packet
	digit, end, _, err := ParseDTMFPayload(packets[0])
	if err != nil {
		t.Fatal(err)
	}
	if digit != "1" {
		t.Errorf("expected digit 1, got %s", digit)
	}
	if end {
		t.Error("expected first packet NOT to have end bit set")
	}

	// Verify last packet
	lastIdx := len(packets) - 1
	_, end, duration, err := ParseDTMFPayload(packets[lastIdx])
	if err != nil {
		t.Fatal(err)
	}
	if !end {
		t.Error("expected last packet to have end bit set")
	}
	if duration != 1280 {
		t.Errorf("expected duration 1280, got %d", duration)
	}

	// Verify second to last packet also has end bit set (retransmission)
	_, end, _, err = ParseDTMFPayload(packets[lastIdx-1])
	if err == nil && !end {
		t.Error("expected second to last packet to have end bit set")
	}
}

func BenchmarkDTMFGeneration(b *testing.B) {
	g := NewDTMFGenerator(8000, 20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		payloads := g.Generate("5", 100)
		// We should normally return these to the pool, but for benchmarking the generation itself:
		// We'll return them to see the net effect.
		for _, p := range payloads {
			audiopool.PutBuffer(p)
		}
	}
}
