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
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/heoruwulf/teleportal/internal/rtp/rtpdefs"
	audiopool "github.com/heoruwulf/teleportal/pkg/audio"
	"github.com/pion/interceptor/pkg/jitterbuffer"
	"go.uber.org/zap"
)

const rawBufferWinSize = 1024

type rawBufferEntry struct {
	buf []byte
	seq uint16
}

// PionJitterBuffer is a wrapper around pion/jitterbuffer that implements rtpdefs.JitterBuffer.
type PionJitterBuffer struct {
	metrics        metrics.Provider
	log            *zap.Logger
	jb             *jitterbuffer.JitterBuffer
	output         chan rtpdefs.RTPPacket
	stop           chan struct{}
	callID         string
	codec          CodecName
	silencePayload []byte
	rawBuffers     [rawBufferWinSize]rawBufferEntry
	wg             sync.WaitGroup
	silence        atomic.Uint64
	dropped        atomic.Uint64
	packetDur      time.Duration
	ptime          int
	sampleRate     int
	underflows     atomic.Uint64
	mu             sync.Mutex
	started        atomic.Bool
	isBuffering    atomic.Bool
	lastTS         uint32
	lastSeq        uint16
	hasLast        bool
}

// NewPionJitterBuffer creates a new jitter buffer using pion/jitterbuffer.
func NewPionJitterBuffer(ctx context.Context, log *zap.Logger, m metrics.Provider, callID string, ptime int, sampleRate int, codec CodecName, minPacketCount int) *PionJitterBuffer {
	if ptime <= 0 {
		ptime = 20 // Default to 20ms
	}

	jbMinPackets := uint16(50 / ptime) // ~50ms min buffer default
	if minPacketCount > 0 {
		jbMinPackets = uint16(minPacketCount)
	}

	pjb := &PionJitterBuffer{
		log:        log.Named("pion_jb"),
		jb:         jitterbuffer.New(jitterbuffer.WithMinimumPacketCount(jbMinPackets)),
		output:     make(chan rtpdefs.RTPPacket, 100),
		stop:       make(chan struct{}),
		packetDur:  time.Duration(ptime) * time.Millisecond,
		ptime:      ptime,
		sampleRate: sampleRate,
		codec:      codec,
		metrics:    m,
		callID:     callID,
	}
	pjb.isBuffering.Store(true)
	pjb.silencePayload = pjb.generateSilence()

	// Register listeners to track state
	pjb.jb.Listen(jitterbuffer.BeginPlayback, func(event jitterbuffer.Event, jb *jitterbuffer.JitterBuffer) {
		pjb.isBuffering.Store(false)
		pjb.log.Debug("Jitter buffer started emitting")
	})

	pjb.jb.Listen(jitterbuffer.BufferUnderflow, func(event jitterbuffer.Event, jb *jitterbuffer.JitterBuffer) {
		pjb.isBuffering.Store(true)
		pjb.log.Debug("Jitter buffer underflow, back to buffering")
	})

	pjb.wg.Add(1)
	go func() {
		defer pjb.wg.Done()
		<-ctx.Done()
		pjb.Stop()
	}()

	return pjb
}

// Push adds a packet to the jitter buffer.
func (pjb *PionJitterBuffer) Push(packet rtpdefs.RTPPacket) {
	if packet.Raw == nil {
		pjb.log.Warn("Received RTPPacket without Raw pion packet, skipping")
		if packet.RawBuffer != nil {
			audiopool.PutBuffer(packet.RawBuffer)
		} else if packet.Payload != nil {
			audiopool.PutBuffer(packet.Payload)
		}
		return
	}

	pjb.mu.Lock()
	idx := packet.Sequence % rawBufferWinSize
	entry := &pjb.rawBuffers[idx]
	// If we already have a buffer for this slot that isn't this sequence (collision/stale), return it to the pool
	if entry.buf != nil && entry.seq != packet.Sequence {
		audiopool.PutBuffer(entry.buf)
	}
	entry.buf = packet.RawBuffer
	entry.seq = packet.Sequence
	pjb.mu.Unlock()

	pjb.jb.Push(packet.Raw)
	if pjb.started.CompareAndSwap(false, true) {
		pjb.wg.Add(1)
		go pjb.run()
	}
}

// Pop returns a channel to consume ordered packets.
func (pjb *PionJitterBuffer) Pop() <-chan rtpdefs.RTPPacket {
	return pjb.output
}

// Stop signals the buffer to shutdown.
func (pjb *PionJitterBuffer) Stop() {
	select {
	case <-pjb.stop:
		return // Already stopped
	default:
		close(pjb.stop)
	}

	// Final metrics report
	pjb.reportMetrics()

	// Clean up any remaining raw buffers
	pjb.mu.Lock()
	for i := range pjb.rawBuffers {
		if pjb.rawBuffers[i].buf != nil {
			audiopool.PutBuffer(pjb.rawBuffers[i].buf)
			pjb.rawBuffers[i].buf = nil
		}
	}
	pjb.mu.Unlock()
}

// Wait waits for all background goroutines to exit.
func (pjb *PionJitterBuffer) Wait() {
	pjb.wg.Wait()
}

func (pjb *PionJitterBuffer) reportMetrics() {
	underflows := pjb.underflows.Swap(0)
	silence := pjb.silence.Swap(0)
	dropped := pjb.dropped.Swap(0)

	if underflows > 0 || silence > 0 || dropped > 0 {
		pjb.metrics.ReportJitterBufferStats(pjb.callID, underflows, silence, dropped)
	}
}

// run is the release loop that pops packets from the jitter buffer at a steady pace.
func (pjb *PionJitterBuffer) run() {
	defer pjb.wg.Done()
	pjb.log.Info("Pion Jitter buffer started", zap.Duration("ptime", pjb.packetDur))
	defer pjb.log.Info("Pion Jitter buffer stopped")

	ticker := time.NewTicker(pjb.packetDur)
	defer ticker.Stop()

	metricsTicker := time.NewTicker(1 * time.Minute)
	defer metricsTicker.Stop()

	for {
		select {
		case <-pjb.stop:
			close(pjb.output)
			return
		case <-metricsTicker.C:
			pjb.reportMetrics()
		case <-ticker.C:
			isBuffering := pjb.isBuffering.Load()
			packet, err := pjb.jb.Pop()

			if err != nil {
				if errors.Is(err, jitterbuffer.ErrPopWhileBuffering) {
					// Still buffering, just wait for next tick
					continue
				}
				if errors.Is(err, jitterbuffer.ErrInvalidOperation) {
					// This happens if the buffer is empty.
					// Suppress if we are in buffering state.
					if isBuffering {
						continue
					}
					// If we were supposed to be emitting, this might be a real issue or just a transient empty buffer.
					// For VoIP bridging, we'll emit silence to maintain continuity.
					pjb.underflows.Add(1)
					if pjb.codec != CodecOpus {
						pjb.emitSilence()
					}
					continue
				}
				if errors.Is(err, jitterbuffer.ErrBufferUnderrun) {
					// Gap in the stream
					pjb.underflows.Add(1)
					if pjb.codec != CodecOpus {
						pjb.emitSilence()
					}
					continue
				}
				pjb.log.Warn("Jitter buffer pop error", zap.Error(err))
				continue
			}

			pjb.lastSeq = packet.SequenceNumber
			pjb.lastTS = packet.Timestamp
			pjb.hasLast = true

			// Retrieve the original raw buffer
			pjb.mu.Lock()
			idx := packet.SequenceNumber % rawBufferWinSize
			var rawBuf []byte
			if pjb.rawBuffers[idx].seq == packet.SequenceNumber {
				rawBuf = pjb.rawBuffers[idx].buf
				pjb.rawBuffers[idx].buf = nil
			}

			// Periodically clean up any old buffers that were dropped by the jitter buffer
			// (e.g. too late or overflow). We do this by checking for sequence numbers
			// that are significantly older than the current one.
			if packet.SequenceNumber%32 == 0 { // Every 32 packets to reduce lock contention
				for i := range pjb.rawBuffers {
					if pjb.rawBuffers[i].buf != nil && isEarlier(pjb.rawBuffers[i].seq, packet.SequenceNumber) {
						pjb.dropped.Add(1)
						audiopool.PutBuffer(pjb.rawBuffers[i].buf)
						pjb.rawBuffers[i].buf = nil
					}
				}
			}
			pjb.mu.Unlock()

			pjb.output <- rtpdefs.RTPPacket{
				Payload:   packet.Payload,
				Timestamp: packet.Timestamp,
				Sequence:  packet.SequenceNumber,
				Raw:       packet,
				RawBuffer: rawBuf,
			}
		}
	}
}

// isEarlier returns true if a is earlier than b in sequence space.
func isEarlier(a, b uint16) bool {
	return b-a < 32768 && a != b
}

// emitSilence generates and sends a silence packet to maintain the stream.
func (pjb *PionJitterBuffer) emitSilence() {
	if !pjb.hasLast || pjb.silencePayload == nil {
		return
	}

	pjb.silence.Add(1)
	samplesPerPacket := uint32((pjb.sampleRate * pjb.ptime) / 1000)
	pjb.lastSeq++
	pjb.lastTS += samplesPerPacket

	pjb.log.Debug("Emitting silence packet", zap.Uint16("seq", pjb.lastSeq), zap.Uint32("ts", pjb.lastTS))

	// Copy silence into a pooled buffer because the consumer will return it to the pool.
	payload := audiopool.GetBuffer(len(pjb.silencePayload))
	copy(payload, pjb.silencePayload)

	pjb.output <- rtpdefs.RTPPacket{
		Payload:   payload,
		Timestamp: pjb.lastTS,
		Sequence:  pjb.lastSeq,
	}
}

// generateSilence pre-allocates a silence payload for the configured codec.
func (pjb *PionJitterBuffer) generateSilence() []byte {
	samplesPerPacket := (pjb.sampleRate * pjb.ptime) / 1000
	if samplesPerPacket <= 0 {
		return nil
	}

	switch pjb.codec {
	case CodecPCMU:
		b := make([]byte, samplesPerPacket)
		for i := range b {
			b[i] = 0xFF // Standard silence for mu-law
		}
		return b
	case CodecPCMA:
		b := make([]byte, samplesPerPacket)
		for i := range b {
			b[i] = 0x55 // Standard silence for A-law
		}
		return b
	case CodecL16:
		// L16 is 16-bit signed PCM, 2 bytes per sample.
		// All zeros is silence for linear PCM.
		return make([]byte, samplesPerPacket*2)
	default:
		return nil
	}
}
