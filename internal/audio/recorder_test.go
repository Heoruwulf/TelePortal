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
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-audio/wav"
	"go.uber.org/zap"
)

func TestStereoRecorder_Interleaving(t *testing.T) {
	t.Parallel()
	log := zap.NewNop()
	tempDir := t.TempDir()
	callID := "test-call-1"
	sampleRate := 8000

	r, err := NewStereoRecorder(context.Background(), log, tempDir, callID, sampleRate)
	if err != nil {
		t.Fatalf("failed to create recorder: %v", err)
	}

	// 10 samples for each side
	left := make([]byte, 20)  // 10 samples * 2 bytes
	right := make([]byte, 20) // 10 samples * 2 bytes

	for i := 0; i < 10; i++ {
		binary.LittleEndian.PutUint16(left[i*2:], uint16(i+1))
		binary.LittleEndian.PutUint16(right[i*2:], uint16((i+1)*10))
	}

	r.PushLeft(left)
	r.PushRight(right)

	// Give it a moment to process
	time.Sleep(200 * time.Millisecond)

	if err := r.Close(); err != nil {
		t.Fatalf("failed to close recorder: %v", err)
	}

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
	if !dec.IsValidFile() {
		t.Fatal("invalid WAV file")
	}

	buf, err := dec.FullPCMBuffer()
	if err != nil {
		t.Fatalf("failed to read PCM buffer: %v", err)
	}

	if buf.Format.NumChannels != 2 {
		t.Errorf("expected 2 channels, got %d", buf.Format.NumChannels)
	}

	// Interleaved data: [L1, R1, L2, R2, ...]
	// Should be: [1, 10, 2, 20, 3, 30, ...]
	expectedLen := 20 // 10 samples * 2 channels
	if len(buf.Data) != expectedLen {
		t.Errorf("expected %d samples, got %d", expectedLen, len(buf.Data))
	}

	for i := 0; i < 10; i++ {
		if buf.Data[i*2] != i+1 {
			t.Errorf("sample %d Left: expected %d, got %d", i, i+1, buf.Data[i*2])
		}
		if buf.Data[i*2+1] != (i+1)*10 {
			t.Errorf("sample %d Right: expected %d, got %d", i, (i+1)*10, buf.Data[i*2+1])
		}
	}
}

func TestStereoRecorder_InitialCapacity(t *testing.T) {
	t.Parallel()
	log := zap.NewNop()
	sampleRate := 8000

	r, err := NewStereoRecorder(context.Background(), log, t.TempDir(), "test-cap", sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	expectedCap := sampleRate / 10
	if cap(r.leftBuf) > expectedCap {
		t.Errorf("leftBuf initial capacity too large: got %d, want <= %d", cap(r.leftBuf), expectedCap)
	}
	if cap(r.rightBuf) > expectedCap {
		t.Errorf("rightBuf initial capacity too large: got %d, want <= %d", cap(r.rightBuf), expectedCap)
	}
}

func TestStereoRecorder_SilenceHandling(t *testing.T) {
	t.Parallel()
	log := zap.NewNop()
	tempDir := t.TempDir()
	callID := "test-call-silence"
	sampleRate := 8000

	r, err := NewStereoRecorder(context.Background(), log, tempDir, callID, sampleRate)
	if err != nil {
		t.Fatalf("failed to create recorder: %v", err)
	}

	// Send 1 second of Left audio only
	left := make([]byte, sampleRate*2) // 1 second * 2 bytes
	for i := 0; i < sampleRate; i++ {
		binary.LittleEndian.PutUint16(left[i*2:], uint16(1))
	}

	r.PushLeft(left)

	// Wait for silence handler to trigger (ticker is 100ms, threshold is 500ms)
	time.Sleep(1 * time.Second)

	if err := r.Close(); err != nil {
		t.Fatalf("failed to close recorder: %v", err)
	}

	// Verify file content
	files, _ := os.ReadDir(tempDir)
	filePath := filepath.Join(tempDir, files[0].Name())
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open recording: %v", err)
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	buf, _ := dec.FullPCMBuffer()

	// Should have roughly 1 second of stereo audio
	// Left should be 1, Right should be 0
	if len(buf.Data) < sampleRate*2 {
		t.Errorf("expected at least %d samples, got %d", sampleRate*2, len(buf.Data))
	}

	for i := 0; i < sampleRate; i++ {
		if buf.Data[i*2] != 1 {
			t.Errorf("sample %d Left: expected 1, got %d", i, buf.Data[i*2])
		}
		if buf.Data[i*2+1] != 0 {
			t.Errorf("sample %d Right: expected 0, got %d", i, buf.Data[i*2+1])
		}
	}
}

func TestBToI(t *testing.T) {
	t.Parallel()
	data := []byte{0x01, 0x00, 0xff, 0xff} // 1, -1 in Little Endian
	expected := []int{1, -1}
	got := bToI(data)
	if len(got) != len(expected) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(expected))
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Errorf("at %d: got %d, want %d", i, got[i], expected[i])
		}
	}
}
