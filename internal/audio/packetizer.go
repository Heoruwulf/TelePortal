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
	"time"

	audiopool "github.com/Heoruwulf/TelePortal/pkg/audio"
)

// Packetizer accumulates audio data and emits it in fixed-size chunks.
type Packetizer struct {
	// 24 bytes (Pointers clustered for GC scan efficiency)
	input  chan []byte
	output chan []byte
	buffer []byte

	// 24 bytes
	ptime        int
	sampleRate   int
	bytesPerTick int
}

// NewPacketizer creates a new packetizer.
func NewPacketizer(ptime int, sampleRate int, bytesPerSample int, channels int) *Packetizer {
	bytesPerTick := (sampleRate * ptime / 1000) * bytesPerSample * channels
	return &Packetizer{
		ptime:        ptime,
		sampleRate:   sampleRate,
		bytesPerTick: bytesPerTick,
		input:        make(chan []byte, 100),
		output:       make(chan []byte, 100),
		buffer:       make([]byte, 0, bytesPerTick*2),
	}
}

// Input returns the input channel.
func (p *Packetizer) Input() chan<- []byte {
	return p.input
}

// Output returns the output channel.
func (p *Packetizer) Output() <-chan []byte {
	return p.output
}

// Run starts the packetization loop.
func (p *Packetizer) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(p.ptime) * time.Millisecond)
	defer ticker.Stop()
	defer close(p.output)

	// Pre-allocate a large circular buffer to avoid append allocations.
	// We make it large enough to hold several ticks of data.
	capacity := p.bytesPerTick * 10
	ringBuf := make([]byte, capacity)
	head, tail := 0, 0
	count := 0

	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-p.input:
			if !ok {
				return
			}
			// Copy data into the ring buffer
			n := len(data)
			if n > capacity-count {
				// We don't have enough room, drop what doesn't fit or handle overflow.
				// For real-time audio, dropping the oldest would be better, but simpler is to drop the new if we are full.
				// However, our ringBuf is capacity*10, so this should be rare.
				if capacity-count > 0 {
					n = capacity - count
				} else {
					n = 0
				}
			}

			if n > 0 {
				// Copy data to tail, handling wrap around
				firstPart := capacity - tail
				if firstPart > n {
					firstPart = n
				}
				copy(ringBuf[tail:tail+firstPart], data[:firstPart])
				if n > firstPart {
					copy(ringBuf[0:n-firstPart], data[firstPart:n])
				}
				tail = (tail + n) % capacity
				count += n
			}
			audiopool.PutBuffer(data)
		case <-ticker.C:
			chunk := audiopool.GetBuffer(p.bytesPerTick)
			if count >= p.bytesPerTick {
				// Read from ring buffer, handling wrap around
				firstPart := capacity - head
				if firstPart > p.bytesPerTick {
					firstPart = p.bytesPerTick
				}
				copy(chunk[:firstPart], ringBuf[head:head+firstPart])
				if p.bytesPerTick > firstPart {
					copy(chunk[firstPart:p.bytesPerTick], ringBuf[0:p.bytesPerTick-firstPart])
				}
				head = (head + p.bytesPerTick) % capacity
				count -= p.bytesPerTick
			} else {
				// Not enough data, emit silence.
				// The Packetizer operates on L16 LE, where silence is simply zeros.
				for i := range chunk {
					chunk[i] = 0
				}
			}

			select {
			case p.output <- chunk:
			default:
				// Output full, drop chunk
				audiopool.PutBuffer(chunk)
			}
		}
	}
}
