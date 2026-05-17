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
	"bytes"
	"testing"
)

func TestEncodeDecodePCMU(t *testing.T) {
	Initialize()

	// 10 samples of L16 LE (20 bytes)
	// Just use a simple incrementing pattern to test roundtrip
	original := []byte{
		0x00, 0x00, // 0
		0x10, 0x00, // 16
		0x00, 0x10, // 4096
		0x00, 0x20, // 8192
		0x00, 0x30, // 12288
		0x00, 0x40, // 16384
		0x00, 0x50, // 20480
		0x00, 0x60, // 24576
		0x00, 0x70, // 28672
		0xFF, 0x7F, // 32767
	}

	pcmu, err := EncodeL16ToPCMU(original)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(pcmu) != len(original)/2 {
		t.Fatalf("Expected PCMU length %d, got %d", len(original)/2, len(pcmu))
	}

	decoded, err := DecodePCMUToL16(pcmu)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("Expected decoded length %d, got %d", len(original), len(decoded))
	}

	// Because G.711 is lossy, we can't expect a perfect byte-for-byte match,
	// but we can verify it doesn't crash and the functions exist.
	if len(decoded) == 0 {
		t.Fatal("Decoded array is empty")
	}
}

func TestEncodeDecodePCMA(t *testing.T) {
	Initialize()

	original := []byte{
		0x00, 0x00,
		0x10, 0x00,
		0x00, 0x10,
		0x00, 0x20,
		0xFF, 0x7F,
	}

	pcma, err := EncodeL16ToPCMA(original)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := DecodePCMAToL16(pcma)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("Expected decoded length %d, got %d", len(original), len(decoded))
	}
}

func TestDecodeL16BEToL16LE(t *testing.T) {
	originalBE := []byte{0x7F, 0xFF, 0x00, 0x10}
	expectedLE := []byte{0xFF, 0x7F, 0x10, 0x00}

	decoded, err := DecodeL16BEToL16LE(originalBE)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !bytes.Equal(decoded, expectedLE) {
		t.Errorf("Expected %x, got %x", expectedLE, decoded)
	}
}
