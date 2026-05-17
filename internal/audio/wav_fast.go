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
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// FastWavWriter is a high-performance WAV writer that avoids reflection.
type FastWavWriter struct {
	file       *os.File
	bw         *bufio.Writer
	sampleRate int
	dataBytes  uint32
}

// NewFastWavWriter creates a new FastWavWriter and writes the initial header.
func NewFastWavWriter(f *os.File, sampleRate int) (*FastWavWriter, error) {
	// Write initial header with 0 data size
	if err := writeWavHeader(f, sampleRate, 2, 0); err != nil {
		return nil, err
	}

	return &FastWavWriter{
		file:       f,
		bw:         bufio.NewWriter(f),
		sampleRate: sampleRate,
	}, nil
}

// Write writes raw PCM bytes to the buffer.
func (w *FastWavWriter) Write(p []byte) (int, error) {
	n, err := w.bw.Write(p)
	w.dataBytes += uint32(n)
	return n, err
}

// Close flushes the buffer and updates the WAV header with the final size.
func (w *FastWavWriter) Close() error {
	if err := w.bw.Flush(); err != nil {
		return fmt.Errorf("flushing wav buffer: %w", err)
	}

	// Seek back to update ChunkSize (offset 4) and Subchunk2Size (offset 40)
	// ChunkSize = 36 + dataBytes
	if _, err := w.file.Seek(4, io.SeekStart); err != nil {
		return fmt.Errorf("seeking to chunk size: %w", err)
	}
	chunkSizeBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(chunkSizeBuf, 36+w.dataBytes)
	if _, err := w.file.Write(chunkSizeBuf); err != nil {
		return fmt.Errorf("updating chunk size: %w", err)
	}

	if _, err := w.file.Seek(40, io.SeekStart); err != nil {
		return fmt.Errorf("seeking to data size: %w", err)
	}
	binary.LittleEndian.PutUint32(chunkSizeBuf, w.dataBytes)
	if _, err := w.file.Write(chunkSizeBuf); err != nil {
		return fmt.Errorf("updating data size: %w", err)
	}

	return nil
}

// writeWavHeader writes a standard 44-byte RIFF/WAVE header.
func writeWavHeader(w io.Writer, sampleRate, channels, totalDataBytes int) error {
	header := make([]byte, 44)

	// RIFF header
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+totalDataBytes))
	copy(header[8:12], "WAVE")

	// fmt subchunk
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // Subchunk1Size for PCM
	binary.LittleEndian.PutUint16(header[20:22], 1)  // AudioFormat (1 = PCM)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	byteRate := sampleRate * channels * 2 // 16-bit = 2 bytes
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	blockAlign := channels * 2
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], 16) // BitsPerSample

	// data subchunk
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(totalDataBytes))

	_, err := w.Write(header)
	if err != nil {
		return fmt.Errorf("writing wav header: %w", err)
	}
	return nil
}

// packIntsToBytes packs interleaved 16-bit PCM ints into a little-endian byte slice.
func packIntsToBytes(samples []int, out []byte) {
	for i, sample := range samples {
		out[i*2] = byte(sample)
		out[i*2+1] = byte(sample >> 8)
	}
}
