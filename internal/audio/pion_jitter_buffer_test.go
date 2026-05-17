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

	"github.com/Heoruwulf/TelePortal/internal/platform/metrics"
	"github.com/Heoruwulf/TelePortal/internal/rtp/rtpdefs"
	"github.com/pion/rtp"
	"go.uber.org/zap"
)

func TestPionJitterBuffer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := zap.NewNop()
	mm := metrics.NewNoOpProvider()
	ptime := 20
	// Use a larger buffer to ensure it doesn't pop immediately
	jb := NewPionJitterBuffer(ctx, log, mm, "test-call", ptime, 8000, CodecPCMU, 0)

	// Push packets out of order.
	// We push 100 first to establish the head, then others out of order.
	packets := []rtpdefs.RTPPacket{
		{
			Sequence:  100,
			Timestamp: 0,
			Raw: &rtp.Packet{
				Header:  rtp.Header{SequenceNumber: 100, Timestamp: 0},
				Payload: []byte{0x00},
			},
		},
		{
			Sequence:  102,
			Timestamp: 320,
			Raw: &rtp.Packet{
				Header:  rtp.Header{SequenceNumber: 102, Timestamp: 320},
				Payload: []byte{0x02},
			},
		},
		{
			Sequence:  101,
			Timestamp: 160,
			Raw: &rtp.Packet{
				Header:  rtp.Header{SequenceNumber: 101, Timestamp: 160},
				Payload: []byte{0x01},
			},
		},
		{
			Sequence:  103,
			Timestamp: 480,
			Raw: &rtp.Packet{
				Header:  rtp.Header{SequenceNumber: 103, Timestamp: 480},
				Payload: []byte{0x03},
			},
		},
	}

	for _, p := range packets {
		jb.Push(p)
	}

	// Pop and verify order
	output := jb.Pop()

	expectedSeqs := []uint16{100, 101, 102, 103}
	for _, expectedSeq := range expectedSeqs {
		select {
		case p := <-output:
			if p.Sequence != expectedSeq {
				t.Errorf("Expected sequence %d, got %d", expectedSeq, p.Sequence)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Timed out waiting for packet %d", expectedSeq)
		}
	}
}

func TestPionJitterBufferSilence(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := zap.NewNop()
	mm := metrics.NewNoOpProvider()
	ptime := 20
	sampleRate := 8000
	// Min packet count 1 to start emitting quickly
	jb := NewPionJitterBuffer(ctx, log, mm, "test-call", ptime, sampleRate, CodecPCMU, 1)

	// Push first packet
	jb.Push(rtpdefs.RTPPacket{
		Sequence:  100,
		Timestamp: 0,
		Raw: &rtp.Packet{
			Header:  rtp.Header{SequenceNumber: 100, Timestamp: 0},
			Payload: []byte{0x00},
		},
	})

	output := jb.Pop()

	// Wait for first packet
	select {
	case p := <-output:
		if p.Sequence != 100 {
			t.Errorf("Expected sequence 100, got %d", p.Sequence)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for first packet")
	}

	// Now wait for silence packet due to "underrun" (we don't push 101)
	select {
	case p := <-output:
		if p.Sequence != 101 {
			t.Errorf("Expected silence sequence 101, got %d", p.Sequence)
		}
		// PCMU silence is 0xFF
		for _, b := range p.Payload {
			if b != 0xFF {
				t.Errorf("Expected silence byte 0xFF, got 0x%02X", b)
				break
			}
		}
		expectedTS := uint32(sampleRate * ptime / 1000)
		if p.Timestamp != expectedTS {
			t.Errorf("Expected silence timestamp %d, got %d", expectedTS, p.Timestamp)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for silence packet")
	}
}
