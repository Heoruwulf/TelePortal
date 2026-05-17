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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	audiopool "github.com/Heoruwulf/TelePortal/pkg/audio"
	"go.uber.org/zap"
)

// StereoRecorder handles recording of bi-directional audio into a stereo WAV file.
type StereoRecorder struct {
	// --- Pointer-containing fields (GC scan prefix) ---

	// 16 bytes
	ctx      context.Context
	filePath string
	callID   string

	// 8 bytes
	log      *zap.Logger
	leftCh   chan []int
	rightCh  chan []int
	cancel   context.CancelFunc
	leftBuf  []int
	rightBuf []int

	// --- Scalar / Non-pointer fields ---

	// 16 bytes
	wg sync.WaitGroup

	// 8 bytes
	sampleRate int
}

// NewStereoRecorder creates a new StereoRecorder.
func NewStereoRecorder(ctx context.Context, log *zap.Logger, recordingPath, callID string, sampleRate int) (*StereoRecorder, error) {
	if recordingPath == "" {
		return nil, nil // Feature disabled
	}

	// Ensure directory exists
	if err := os.MkdirAll(recordingPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create recording directory: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	fileName := fmt.Sprintf("%s_%s.wav", timestamp, callID)
	fullPath := filepath.Join(recordingPath, fileName)

	ctx, cancel := context.WithCancel(ctx)
	r := &StereoRecorder{
		log:        log,
		sampleRate: sampleRate,
		filePath:   fullPath,
		callID:     callID,
		leftCh:     make(chan []int, 100), // Buffer ~2 seconds of audio at 20ms ptime
		rightCh:    make(chan []int, 100),
		ctx:        ctx,
		cancel:     cancel,
		leftBuf:    make([]int, 0, sampleRate/10), // Pre-allocate 100ms to reduce baseline memory footprint
		rightBuf:   make([]int, 0, sampleRate/10),
	}

	r.wg.Add(1)
	go r.run()

	return r, nil
}

// PushLeft adds mono samples to the left channel (Rx).
func (r *StereoRecorder) PushLeft(data []byte) {
	if r == nil {
		return
	}
	samples := bToI(data)
	select {
	case r.leftCh <- samples:
	case <-r.ctx.Done():
		audiopool.PutIntBuffer(samples)
	default:
		// Drop samples if the recorder is falling behind
		audiopool.PutIntBuffer(samples)
	}
}

// PushRight adds mono samples to the right channel (Tx).
func (r *StereoRecorder) PushRight(data []byte) {
	if r == nil {
		return
	}
	samples := bToI(data)
	select {
	case r.rightCh <- samples:
	case <-r.ctx.Done():
		audiopool.PutIntBuffer(samples)
	default:
		// Drop samples if the recorder is falling behind
		audiopool.PutIntBuffer(samples)
	}
}

// Close stops the recorder and finalizes the WAV file.
func (r *StereoRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.cancel()
	r.wg.Wait()
	return nil
}

func (r *StereoRecorder) run() {
	defer r.wg.Done()

	f, err := os.Create(r.filePath)
	if err != nil {
		r.log.Error("Failed to create recording file", zap.Error(err), zap.String("path", r.filePath))
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			r.log.Error("Failed to close recording file", zap.Error(err))
		}
	}()

	// 16-bit PCM, 2 channels (Stereo)
	w, err := NewFastWavWriter(f, r.sampleRate)
	if err != nil {
		r.log.Error("Failed to create WAV writer", zap.Error(err))
		return
	}
	defer func() {
		if err := w.Close(); err != nil {
			r.log.Error("Failed to close WAV writer", zap.Error(err))
		}
	}()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			// Drain remaining samples in channels
		drainLoop:
			for {
				select {
				case samples := <-r.leftCh:
					r.leftBuf = append(r.leftBuf, samples...)
					audiopool.PutIntBuffer(samples)
					r.process(w)
				case samples := <-r.rightCh:
					r.rightBuf = append(r.rightBuf, samples...)
					audiopool.PutIntBuffer(samples)
					r.process(w)
				default:
					break drainLoop
				}
			}
			// Final flush
			r.flush(w)
			return
		case samples := <-r.leftCh:
			r.leftBuf = append(r.leftBuf, samples...)
			audiopool.PutIntBuffer(samples)
			r.process(w)
		case samples := <-r.rightCh:
			r.rightBuf = append(r.rightBuf, samples...)
			audiopool.PutIntBuffer(samples)
			r.process(w)
		case <-ticker.C:
			// Handle drift/silence if one side hasn't sent audio for a while
			r.handleSilence(w)
		}
	}
}

func (r *StereoRecorder) process(w *FastWavWriter) {
	// Interleave samples as long as we have data for both channels
	minLen := len(r.leftBuf)
	if len(r.rightBuf) < minLen {
		minLen = len(r.rightBuf)
	}

	if minLen == 0 {
		return
	}

	r.writeInterleaved(w, minLen)
}

func (r *StereoRecorder) handleSilence(w *FastWavWriter) {
	// If one buffer is much larger than the other, it means the other side is silent or lagging.
	// We fill with zeros to keep them aligned.
	// A threshold of 500ms seems reasonable.
	threshold := r.sampleRate / 2

	if len(r.leftBuf)-len(r.rightBuf) > threshold {
		count := len(r.leftBuf) - len(r.rightBuf)
		padding := audiopool.GetIntBuffer(count)
		for i := 0; i < count; i++ {
			padding[i] = 0
		}
		r.rightBuf = append(r.rightBuf, padding...)
		audiopool.PutIntBuffer(padding)
		r.process(w)
	} else if len(r.rightBuf)-len(r.leftBuf) > threshold {
		count := len(r.rightBuf) - len(r.leftBuf)
		padding := audiopool.GetIntBuffer(count)
		for i := 0; i < count; i++ {
			padding[i] = 0
		}
		r.leftBuf = append(r.leftBuf, padding...)
		audiopool.PutIntBuffer(padding)
		r.process(w)
	}
}

func (r *StereoRecorder) flush(w *FastWavWriter) {
	// Flush remaining samples by padding the shorter buffer with zeros
	maxLen := len(r.leftBuf)
	if len(r.rightBuf) > maxLen {
		maxLen = len(r.rightBuf)
	}

	if maxLen == 0 {
		return
	}

	if len(r.leftBuf) < maxLen {
		count := maxLen - len(r.leftBuf)
		padding := audiopool.GetIntBuffer(count)
		for i := 0; i < count; i++ {
			padding[i] = 0
		}
		r.leftBuf = append(r.leftBuf, padding...)
		audiopool.PutIntBuffer(padding)
	}
	if len(r.rightBuf) < maxLen {
		count := maxLen - len(r.rightBuf)
		padding := audiopool.GetIntBuffer(count)
		for i := 0; i < count; i++ {
			padding[i] = 0
		}
		r.rightBuf = append(r.rightBuf, padding...)
		audiopool.PutIntBuffer(padding)
	}

	r.writeInterleaved(w, maxLen)
}

func (r *StereoRecorder) writeInterleaved(w *FastWavWriter, count int) {
	interleaved := audiopool.GetIntBuffer(count * 2)
	defer audiopool.PutIntBuffer(interleaved)

	for i := 0; i < count; i++ {
		interleaved[i*2] = r.leftBuf[i]
		interleaved[i*2+1] = r.rightBuf[i]
	}

	// Allocate bytes from pool (count * 2 channels * 2 bytes/sample)
	byteLen := count * 4
	byteBuf := audiopool.GetBuffer(byteLen)
	defer audiopool.PutBuffer(byteBuf)

	packIntsToBytes(interleaved, byteBuf)

	if _, err := w.Write(byteBuf); err != nil {
		r.log.Error("Failed to write to WAV writer", zap.Error(err))
	}

	// Remove processed samples by shifting remaining data to the front
	// This avoids allocating new underlying arrays over time
	nLeft := copy(r.leftBuf, r.leftBuf[count:])
	r.leftBuf = r.leftBuf[:nLeft]

	nRight := copy(r.rightBuf, r.rightBuf[count:])
	r.rightBuf = r.rightBuf[:nRight]
}

// bToI converts L16 LE bytes to int samples.
func bToI(data []byte) []int {
	samples := audiopool.GetIntBuffer(len(data) / 2)
	for i := 0; i < len(samples); i++ {
		samples[i] = int(int16(binary.LittleEndian.Uint16(data[i*2:])))
	}
	return samples
}
