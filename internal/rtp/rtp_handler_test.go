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
	"context"
	"net"
	"testing"
	"time"

	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/rtp/rtpdefs"
	"github.com/pion/rtp"
	"go.uber.org/zap"
)

type mockJitterBuffer struct {
	packets chan rtpdefs.RTPPacket
}

func (m *mockJitterBuffer) Push(p rtpdefs.RTPPacket) {
	select {
	case m.packets <- p:
	default:
	}
}
func (m *mockJitterBuffer) Pop() <-chan rtpdefs.RTPPacket { return m.packets }
func (m *mockJitterBuffer) Stop()                         {}
func (m *mockJitterBuffer) Wait()                         {}

func TestStartReader_DTMF(t *testing.T) {
	log := zap.NewNop()
	jb := &mockJitterBuffer{packets: make(chan rtpdefs.RTPPacket, 10)}

	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	addr := l.LocalAddr()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	info := audio.Stream{
		Codec: audio.Codec{
			Name:        audio.CodecPCMU,
			PayloadType: 0,
		},
		DTMFPayloadType: 101,
	}

	dtmfReceived := make(chan string, 1)
	onDTMF := func(digit string, duration uint16, end bool) {
		if end {
			dtmfReceived <- digit
		}
	}

	go StartReader(ctx, log, l, jb, info, nil, onDTMF)

	// Send DTMF packet
	conn, err := net.Dial("udp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// RFC 2833 packet for digit '5'
	dtmfPayload := []byte{0x05, 0x80, 0x00, 0xA0} // digit 5, end bit set, duration 160
	p := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    101,
			SequenceNumber: 1,
			Timestamp:      160,
			SSRC:           12345,
		},
		Payload: dtmfPayload,
	}
	buf, _ := p.Marshal()
	_, _ = conn.Write(buf)

	select {
	case digit := <-dtmfReceived:
		if digit != "5" {
			t.Errorf("expected digit 5, got %s", digit)
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for DTMF")
	}
}

func TestStartWriter_DTMFInjection(t *testing.T) {
	log := zap.NewNop()

	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	addr := l.LocalAddr()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	info := audio.Stream{
		Codec: audio.Codec{
			Name:        audio.CodecPCMU,
			PayloadType: 0,
			SampleRate:  8000,
		},
		DTMFPayloadType: 101,
		PTime:           20,
	}

	audioSource := make(chan []byte)
	dtmfSource := make(chan DTMFRequest, 1)

	go StartWriter(ctx, log, l, addr, info, audioSource, dtmfSource, string(audio.CodecL16))

	// Inject DTMF
	dtmfSource <- DTMFRequest{Digit: "1", Duration: 100}
	time.Sleep(10 * time.Millisecond) // Give the writer time to process the DTMF request

	// Trigger emission by sending an audio payload (DTMF will be sent instead)
	audioSource <- make([]byte, 160)

	// Read from the same listener (it's sending to itself basically)
	buf := make([]byte, 1500)
	l.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, _, err := l.ReadFrom(buf)
	if err != nil {
		t.Fatalf("failed to read injected DTMF packet: %v", err)
	}

	p := &rtp.Packet{}
	if err := p.Unmarshal(buf[:n]); err != nil {
		t.Fatal(err)
	}

	if p.PayloadType != 101 {
		t.Errorf("expected payload type 101, got %d", p.PayloadType)
	}
}
