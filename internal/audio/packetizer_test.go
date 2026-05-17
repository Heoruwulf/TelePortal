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
	"context"
	"testing"
	"time"
)

func TestPacketizer(t *testing.T) {
	t.Parallel()

	ptime := 20
	sampleRate := 8000
	p := NewPacketizer(ptime, sampleRate, 2, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go p.Run(ctx)

	// bytesPerTick = (8000 * 20 / 1000) * 2 = 160 * 2 = 320 bytes
	expectedSize := 320

	// Push irregular chunks
	p.Input() <- make([]byte, 100)
	p.Input() <- make([]byte, 250) // Total 350, should emit one 320 byte chunk, 30 left

	select {
	case chunk := <-p.Output():
		if len(chunk) != expectedSize {
			t.Errorf("Expected chunk size %d, got %d", expectedSize, len(chunk))
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timed out waiting for first chunk")
	}

	p.Input() <- make([]byte, 300) // Total 30 + 300 = 330, should emit one 320 byte chunk, 10 left

	select {
	case chunk := <-p.Output():
		if len(chunk) != expectedSize {
			t.Errorf("Expected chunk size %d, got %d", expectedSize, len(chunk))
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timed out waiting for second chunk")
	}
}
