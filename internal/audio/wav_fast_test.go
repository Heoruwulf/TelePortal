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
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

func TestPackIntsToBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		samples []int
		want    []byte
	}{
		{
			name:    "positive samples",
			samples: []int{0x1234, 0x5678},
			want:    []byte{0x34, 0x12, 0x78, 0x56},
		},
		{
			name:    "negative samples",
			samples: []int{-0x1234, -1},
			want:    []byte{0xcc, 0xed, 0xff, 0xff},
		},
		{
			name:    "zero sample",
			samples: []int{0},
			want:    []byte{0x00, 0x00},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := make([]byte, len(tt.samples)*2)
			packIntsToBytes(tt.samples, out)
			if !bytes.Equal(out, tt.want) {
				t.Errorf("packIntsToBytes() = %x, want %x", out, tt.want)
			}
		})
	}
}

func TestWriteWavHeader(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	sampleRate := 16000
	channels := 2
	dataSize := 1024
	err := writeWavHeader(&buf, sampleRate, channels, dataSize)
	if err != nil {
		t.Fatalf("writeWavHeader() error = %v", err)
	}

	got := buf.Bytes()
	if len(got) != 44 {
		t.Errorf("header length = %d, want 44", len(got))
	}

	// Verify key parts
	if string(got[0:4]) != "RIFF" {
		t.Errorf("RIFF tag = %s, want RIFF", got[0:4])
	}
	if binary.LittleEndian.Uint32(got[4:8]) != uint32(36+dataSize) {
		t.Errorf("ChunkSize = %d, want %d", binary.LittleEndian.Uint32(got[4:8]), 36+dataSize)
	}
	if string(got[8:12]) != "WAVE" {
		t.Errorf("WAVE tag = %s, want WAVE", got[8:12])
	}
	if string(got[12:16]) != "fmt " {
		t.Errorf("fmt tag = %s, want fmt ", got[12:16])
	}
	if binary.LittleEndian.Uint32(got[24:28]) != uint32(sampleRate) {
		t.Errorf("SampleRate = %d, want %d", binary.LittleEndian.Uint32(got[24:28]), sampleRate)
	}
	if binary.LittleEndian.Uint16(got[22:24]) != uint16(channels) {
		t.Errorf("Channels = %d, want %d", binary.LittleEndian.Uint16(got[22:24]), channels)
	}
	if string(got[36:40]) != "data" {
		t.Errorf("data tag = %s, want data", got[36:40])
	}
	if binary.LittleEndian.Uint32(got[40:44]) != uint32(dataSize) {
		t.Errorf("dataSize = %d, want %d", binary.LittleEndian.Uint32(got[40:44]), dataSize)
	}
}

func TestFastWavWriter(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.wav")
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	sampleRate := 16000
	w, err := NewFastWavWriter(f, sampleRate)
	if err != nil {
		t.Fatalf("NewFastWavWriter() error = %v", err)
	}

	// Write 10 samples (interleaved, 2 channels, 2 bytes each = 40 bytes)
	samples := make([]int, 20)
	for i := range samples {
		samples[i] = i
	}
	byteBuf := make([]byte, 40)
	packIntsToBytes(samples, byteBuf)

	if _, err := w.Write(byteBuf); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	f.Close()

	// Re-open and verify
	f2, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open file: %v", err)
	}
	defer f2.Close()

	header := make([]byte, 44)
	if _, err := io.ReadFull(f2, header); err != nil {
		t.Fatalf("failed to read header: %v", err)
	}

	if binary.LittleEndian.Uint32(header[4:8]) != 36+40 {
		t.Errorf("ChunkSize = %d, want %d", binary.LittleEndian.Uint32(header[4:8]), 36+40)
	}
	if binary.LittleEndian.Uint32(header[40:44]) != 40 {
		t.Errorf("dataSize = %d, want %d", binary.LittleEndian.Uint32(header[40:44]), 40)
	}

	// Verify data
	data := make([]byte, 40)
	if _, err := io.ReadFull(f2, data); err != nil {
		t.Fatalf("failed to read data: %v", err)
	}
	if !bytes.Equal(data, byteBuf) {
		t.Errorf("data mismatch")
	}
}

type mockWriteSeeker struct {
	io.Writer
}

func (m *mockWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}

func BenchmarkWAVEncoding_Legacy(b *testing.B) {
	sampleRate := 16000
	count := 320 // 20ms at 16kHz
	interleaved := make([]int, count*2)
	for i := range interleaved {
		interleaved[i] = i % 32768
	}

	buf := &audio.IntBuffer{
		Format: &audio.Format{
			NumChannels: 2,
			SampleRate:  sampleRate,
		},
		Data:           interleaved,
		SourceBitDepth: 16,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mws := &mockWriteSeeker{io.Discard}
		enc := wav.NewEncoder(mws, sampleRate, 16, 2, 1)
		if err := enc.Write(buf); err != nil {
			b.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWAVEncoding_Fast(b *testing.B) {
	sampleRate := 16000
	count := 320 // 20ms at 16kHz
	interleaved := make([]int, count*2)
	for i := range interleaved {
		interleaved[i] = i % 32768
	}

	byteLen := count * 4
	byteBuf := make([]byte, byteLen)

	mws := &mockWriteSeeker{io.Discard}
	w := &FastWavWriter{
		file:       nil,
		bw:         bufio.NewWriter(mws),
		sampleRate: sampleRate,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		packIntsToBytes(interleaved, byteBuf)
		if _, err := w.Write(byteBuf); err != nil {
			b.Fatal(err)
		}
		// In real usage we don't flush every 20ms, bufio handles it.
		// But to measure packing + write overhead:
	}
}
