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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	audiopool "github.com/heoruwulf/teleportal/pkg/audio"
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
	leftCh   chan []byte
	rightCh  chan []byte
	cancel   context.CancelFunc
	leftBuf  []byte
	rightBuf []byte

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
	if err := os.MkdirAll(recordingPath, 0o755); err != nil {
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
		leftCh:     make(chan []byte, 100), // Buffer ~2 seconds of audio at 20ms ptime
		rightCh:    make(chan []byte, 100),
		ctx:        ctx,
		cancel:     cancel,
		leftBuf:    make([]byte, 0, (sampleRate*2)/10), // Pre-allocate 100ms 16-bit PCM bytes
		rightBuf:   make([]byte, 0, (sampleRate*2)/10),
	}

	r.wg.Add(1)
	go r.run()

	return r, nil
}

// PushLeft adds mono 16-bit PCM samples to the left channel (Rx).
func (r *StereoRecorder) PushLeft(data []byte) {
	if r == nil || len(data) == 0 {
		return
	}
	buf := audiopool.GetBuffer(len(data))
	copy(buf, data)
	select {
	case r.leftCh <- buf:
		r.log.Debug("PushLeft: queued samples", zap.Int("bytes", len(buf)))
	case <-r.ctx.Done():
		audiopool.PutBuffer(buf)
	default:
		// Drop samples if the recorder is falling behind
		r.log.Warn("PushLeft: dropping samples, queue full")
		audiopool.PutBuffer(buf)
	}
}

// PushRight adds mono 16-bit PCM samples to the right channel (Tx).
func (r *StereoRecorder) PushRight(data []byte) {
	if r == nil || len(data) == 0 {
		return
	}
	buf := audiopool.GetBuffer(len(data))
	copy(buf, data)
	select {
	case r.rightCh <- buf:
		r.log.Debug("PushRight: queued samples", zap.Int("bytes", len(buf)))
	case <-r.ctx.Done():
		audiopool.PutBuffer(buf)
	default:
		// Drop samples if the recorder is falling behind
		r.log.Warn("PushRight: dropping samples, queue full")
		audiopool.PutBuffer(buf)
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
				case buf := <-r.leftCh:
					r.leftBuf = append(r.leftBuf, buf...)
					audiopool.PutBuffer(buf)
					r.process(w)
				case buf := <-r.rightCh:
					r.rightBuf = append(r.rightBuf, buf...)
					audiopool.PutBuffer(buf)
					r.process(w)
				default:
					break drainLoop
				}
			}
			// Final flush
			r.flush(w)
			return
		case buf := <-r.leftCh:
			r.leftBuf = append(r.leftBuf, buf...)
			audiopool.PutBuffer(buf)
			r.process(w)
		case buf := <-r.rightCh:
			r.rightBuf = append(r.rightBuf, buf...)
			audiopool.PutBuffer(buf)
			r.process(w)
		case <-ticker.C:
			// Handle drift/silence if one side hasn't sent audio for a while
			r.handleSilence(w)
		}
	}
}

func (r *StereoRecorder) process(w *FastWavWriter) {
	// Interleave samples as long as we have data for both channels
	minBytes := min(len(r.rightBuf), len(r.leftBuf))
	minBytes -= minBytes % 2 // Align to 16-bit sample boundary

	if minBytes == 0 {
		return
	}

	r.log.Debug("process: writing interleaved samples", zap.Int("bytes", minBytes))
	r.writeInterleaved(w, minBytes)
}

func (r *StereoRecorder) handleSilence(w *FastWavWriter) {
	// If one buffer is much larger than the other, it means the other side is silent or lagging.
	// We fill with zeros to keep them aligned.
	// A threshold of 500ms (sampleRate samples * 2 bytes/sample)
	threshold := r.sampleRate

	if len(r.leftBuf)-len(r.rightBuf) > threshold {
		count := len(r.leftBuf) - len(r.rightBuf)
		count -= count % 2
		padding := audiopool.GetBuffer(count)
		clear(padding)
		r.rightBuf = append(r.rightBuf, padding...)
		audiopool.PutBuffer(padding)
		r.process(w)
	} else if len(r.rightBuf)-len(r.leftBuf) > threshold {
		count := len(r.rightBuf) - len(r.leftBuf)
		count -= count % 2
		padding := audiopool.GetBuffer(count)
		clear(padding)
		r.leftBuf = append(r.leftBuf, padding...)
		audiopool.PutBuffer(padding)
		r.process(w)
	}
}

func (r *StereoRecorder) flush(w *FastWavWriter) {
	// Flush remaining samples by padding the shorter buffer with zeros
	maxBytes := max(len(r.rightBuf), len(r.leftBuf))
	maxBytes -= maxBytes % 2

	if maxBytes == 0 {
		r.log.Debug("flush: no remaining samples to flush")
		return
	}

	r.log.Debug("flush: padding and writing remaining samples", zap.Int("bytes", maxBytes))

	if len(r.leftBuf) < maxBytes {
		count := maxBytes - len(r.leftBuf)
		padding := audiopool.GetBuffer(count)
		clear(padding)
		r.leftBuf = append(r.leftBuf, padding...)
		audiopool.PutBuffer(padding)
	}
	if len(r.rightBuf) < maxBytes {
		count := maxBytes - len(r.rightBuf)
		padding := audiopool.GetBuffer(count)
		clear(padding)
		r.rightBuf = append(r.rightBuf, padding...)
		audiopool.PutBuffer(padding)
	}

	r.writeInterleaved(w, maxBytes)
}

func (r *StereoRecorder) writeInterleaved(w *FastWavWriter, byteCount int) {
	sampleCount := byteCount / 2
	byteLen := sampleCount * 4 // 2 channels * 2 bytes/sample
	byteBuf := audiopool.GetBuffer(byteLen)
	defer audiopool.PutBuffer(byteBuf)

	// Direct zero-conversion 2-byte sample interleaving
	for i := range sampleCount {
		// Left channel 16-bit sample (2 bytes)
		byteBuf[i*4] = r.leftBuf[i*2]
		byteBuf[i*4+1] = r.leftBuf[i*2+1]
		// Right channel 16-bit sample (2 bytes)
		byteBuf[i*4+2] = r.rightBuf[i*2]
		byteBuf[i*4+3] = r.rightBuf[i*2+1]
	}

	if _, err := w.Write(byteBuf); err != nil {
		r.log.Error("Failed to write to WAV writer", zap.Error(err))
	}

	// Remove processed samples by shifting remaining data to the front
	nLeft := copy(r.leftBuf, r.leftBuf[byteCount:])
	r.leftBuf = r.leftBuf[:nLeft]

	nRight := copy(r.rightBuf, r.rightBuf[byteCount:])
	r.rightBuf = r.rightBuf[:nRight]
}
