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
package api

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-audio/wav"
	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/heoruwulf/teleportal/internal/rtp/rtpdefs"
	"go.uber.org/zap"
)

type mockMetrics struct {
	metrics.Provider
}

func (m *mockMetrics) IncWSConnections()                                    {}
func (m *mockMetrics) DecWSConnections()                                    {}
func (m *mockMetrics) ObserveTranscodingDuration(s string, d time.Duration) {}

func TestAudioBridge_RecordingIntegration(t *testing.T) {
	log := zap.NewNop()
	tempDir := t.TempDir()
	callID := "bridge-integration-test"

	codec, _ := audio.NewCodecL16(96, 8000, 1, false, true)
	stream := audio.Stream{Codec: codec, PTime: 20}

	audioInput := make(chan rtpdefs.RTPPacket, 100)

	bridge := NewAudioBridge(context.Background(), log, &mockMetrics{}, audioInput, callID, stream, tempDir, string(audio.CodecL16))
	bridge.Start()

	// Send 50 packets to Rx (Left) - 1 second at 20ms ptime
	for i := 0; i < 50; i++ {
		payload := make([]byte, 320)
		for j := 0; j < 160; j++ {
			binary.BigEndian.PutUint16(payload[j*2:], uint16(100))
		}
		audioInput <- rtpdefs.RTPPacket{Payload: payload}
	}

	// Send 50 packets to Tx (Right)
	for i := 0; i < 50; i++ {
		payload := make([]byte, 320)
		for j := 0; j < 160; j++ {
			binary.LittleEndian.PutUint16(payload[j*2:], uint16(200))
		}
		bridge.recorder.PushRight(payload)
	}

	// Give it some time to process and write
	time.Sleep(1 * time.Second)

	bridge.CloseAll()
	close(audioInput)

	// Allow final flush
	time.Sleep(200 * time.Millisecond)

	// Verify file content
	files, _ := os.ReadDir(tempDir)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	filePath := filepath.Join(tempDir, files[0].Name())
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open recording: %v", err)
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		t.Fatalf("failed to read PCM buffer: %v", err)
	}

	// Should have at least 1 second (8000 samples)
	if len(buf.Data) < 16000 { // 8000 * 2 channels
		t.Errorf("expected at least 16000 samples, got %d", len(buf.Data))
	}

	// Sample check
	if buf.Data[0] != 100 {
		t.Errorf("expected Left sample 100, got %d", buf.Data[0])
	}
	if buf.Data[1] != 200 {
		t.Errorf("expected Right sample 200, got %d", buf.Data[1])
	}
}
