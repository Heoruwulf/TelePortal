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
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/rtp/rtpdefs"
	audiopool "github.com/heoruwulf/teleportal/pkg/audio"
	"go.uber.org/zap"
)

const (
	rtpHeaderSize = 12
	rtpVersion    = 2
)

// DTMFRequest represents a request to send a DTMF digit.
type DTMFRequest struct {
	Digit    string
	Duration int // in milliseconds
}

func initRTPHeader(info audio.Stream, log *zap.Logger) ([]byte, uint32, uint16) {
	var ssrc, timestamp uint32
	var seqNum uint16

	var b [10]byte
	if _, err := crand.Read(b[:]); err != nil {
		log.Warn("Failed to read crypto/rand, falling back to math/rand", zap.Error(err))
		ssrc = rand.Uint32()
		seqNum = uint16(rand.Uint32())
		timestamp = rand.Uint32()
	} else {
		ssrc = binary.BigEndian.Uint32(b[0:4])
		seqNum = binary.BigEndian.Uint16(b[4:6])
		timestamp = binary.BigEndian.Uint32(b[6:10])
	}

	rtpHeader := make([]byte, rtpHeaderSize)
	rtpHeader[0] = (rtpVersion << 6)
	rtpHeader[1] = info.Codec.PayloadType
	binary.BigEndian.PutUint32(rtpHeader[8:12], ssrc)
	return rtpHeader, timestamp, seqNum
}

func drainAudioSource(audioSource <-chan []byte) {
	if audioSource == nil {
		return
	}
	for {
		select {
		case buf, ok := <-audioSource:
			if !ok {
				return
			}
			audiopool.PutBuffer(buf)
		default:
			return
		}
	}
}

func encodeOutboundPayload(l16Payload []byte, actualWsCodec, wsCodec string, info audio.Stream) ([]byte, error) {
	if actualWsCodec == string(info.Codec.Name) {
		if info.Codec.Name == audio.CodecL16 {
			switch {
			case wsCodec == string(audio.CodecPass):
				return l16Payload, nil
			case info.Codec.IsBigEndian:
				return audio.DecodeL16LEToL16BE(l16Payload)
			default:
				return l16Payload, nil
			}
		}
		return l16Payload, nil
	}

	switch info.Codec.Name {
	case audio.CodecPCMU:
		return audio.EncodeL16ToPCMU(l16Payload)
	case audio.CodecPCMA:
		return audio.EncodeL16ToPCMA(l16Payload)
	case audio.CodecL16:
		if info.Codec.IsBigEndian {
			return audio.DecodeL16LEToL16BE(l16Payload)
		}
		return l16Payload, nil
	case audio.CodecOpus:
		return nil, fmt.Errorf("opus encoding not implemented")
	default:
		return nil, fmt.Errorf("unsupported codec for outbound: %s", info.Codec.Name)
	}
}

// WriterConfig contains parameters for StartWriter.
type WriterConfig struct {
	Stream      net.PacketConn
	RemoteAddr  net.Addr
	AudioSource <-chan []byte
	DTMFSource  <-chan DTMFRequest
	WsCodec     string
	Info        audio.Stream
}

// StartWriter sends RTP packets to the remote endpoint.
// It consumes payloads from the provided channel, encodes them if necessary, and sends them.
func StartWriter(ctx context.Context, log *zap.Logger, cfg WriterConfig) {
	log = log.Named("rtp_writer")
	if cfg.Stream == nil {
		log.Error("RTP stream is nil, writer cannot start")
		return
	}
	log.Info("Starting RTP writer",
		zap.String("codec", string(cfg.Info.Codec.Name)),
		zap.Int("ptime", cfg.Info.PTime),
	)
	defer log.Info("RTP writer stopped")

	rtpHeader, timestamp, seqNum := initRTPHeader(cfg.Info, log)
	tsIncrement := cfg.Info.SamplesPerPacket()

	dtmfGenerator := NewDTMFGenerator(cfg.Info.Codec.SampleRate, cfg.Info.PTime)
	var pendingDTMF [][]byte
	var currentDTMFTimestamp uint32

	defer func() {
		for _, p := range pendingDTMF {
			audiopool.PutBuffer(p)
		}
		drainAudioSource(cfg.AudioSource)
	}()

	actualWsCodec := cfg.WsCodec
	if cfg.WsCodec == string(audio.CodecPass) {
		actualWsCodec = string(cfg.Info.Codec.Name)
	}

	bytesPerSample := 2
	if actualWsCodec == string(audio.CodecPCMU) || actualWsCodec == string(audio.CodecPCMA) {
		bytesPerSample = 1
	}

	channels := cfg.Info.Codec.Channels
	if channels == 0 {
		channels = 1
	}

	ptime := cfg.Info.PTime
	if ptime == 0 {
		ptime = 20
	}
	sampleRate := cfg.Info.Codec.SampleRate
	if sampleRate == 0 {
		sampleRate = 8000
	}

	bytesPerTick := (sampleRate * ptime / 1000) * bytesPerSample * channels
	if bytesPerTick == 0 {
		bytesPerTick = int(tsIncrement) * bytesPerSample
	}

	capacity := bytesPerTick * 10
	ringBuf := make([]byte, capacity)
	head, tail := 0, 0
	count := 0

	ticker := time.NewTicker(time.Duration(ptime) * time.Millisecond)
	defer ticker.Stop()

	dtmfSource := cfg.DTMFSource
	audioSource := cfg.AudioSource

	for {
		select {
		case <-ctx.Done():
			return

		case req, ok := <-dtmfSource:
			if !ok {
				dtmfSource = nil
				continue
			}
			if cfg.Info.DTMFPayloadType == 0 {
				log.Warn("DTMF request received but DTMF is not negotiated", zap.String("digit", req.Digit))
				continue
			}

			log.Debug("Injecting DTMF", zap.String("digit", req.Digit), zap.Int("duration", req.Duration))

			for _, p := range pendingDTMF {
				audiopool.PutBuffer(p)
			}
			pendingDTMF = dtmfGenerator.Generate(req.Digit, req.Duration)
			timestamp += tsIncrement
			currentDTMFTimestamp = timestamp

		case data, ok := <-audioSource:
			if !ok {
				audioSource = nil
				continue
			}
			n := len(data)
			if n > capacity-count {
				n = max(capacity-count, 0)
			}
			if n > 0 {
				firstPart := min(capacity-tail, n)
				copy(ringBuf[tail:tail+firstPart], data[:firstPart])
				if n > firstPart {
					copy(ringBuf[0:n-firstPart], data[firstPart:n])
				}
				tail = (tail + n) % capacity
				count += n
			}
			audiopool.PutBuffer(data)

		case <-ticker.C:
			if len(pendingDTMF) > 0 {
				p := pendingDTMF[0]
				pendingDTMF = pendingDTMF[1:]

				seqNum++
				rtpHeader[1] = cfg.Info.DTMFPayloadType
				binary.BigEndian.PutUint16(rtpHeader[2:4], seqNum)
				binary.BigEndian.PutUint32(rtpHeader[4:8], currentDTMFTimestamp)

				packet := audiopool.GetBuffer(rtpHeaderSize + len(p))
				copy(packet[:rtpHeaderSize], rtpHeader)
				copy(packet[rtpHeaderSize:], p)

				if _, err := cfg.Stream.WriteTo(packet, cfg.RemoteAddr); err != nil {
					log.Warn("Failed to write DTMF RTP packet", zap.Error(err))
				}
				audiopool.PutBuffer(packet)
				audiopool.PutBuffer(p)

				timestamp += tsIncrement
				rtpHeader[1] = cfg.Info.Codec.PayloadType

				if count >= bytesPerTick {
					head = (head + bytesPerTick) % capacity
					count -= bytesPerTick
				}
				continue
			}

			if count == 0 {
				continue
			}

			l16Payload := audiopool.GetBuffer(bytesPerTick)
			firstPart := min(capacity-head, min(bytesPerTick, count))
			copy(l16Payload[:firstPart], ringBuf[head:head+firstPart])
			if bytesPerTick > firstPart && count > firstPart {
				secondPart := min(bytesPerTick-firstPart, count-firstPart)
				copy(l16Payload[firstPart:firstPart+secondPart], ringBuf[0:secondPart])
			}
			if count < bytesPerTick {
				clear(l16Payload[count:])
				head = (head + count) % capacity
				count = 0
			} else {
				head = (head + bytesPerTick) % capacity
				count -= bytesPerTick
			}

			// Encode if necessary
			payload, err := encodeOutboundPayload(l16Payload, actualWsCodec, cfg.WsCodec, cfg.Info)
			if err != nil {
				log.Warn("Failed to encode audio for RTP", zap.Error(err))
				audiopool.PutBuffer(l16Payload)
				continue
			}

			seqNum++
			timestamp += tsIncrement

			binary.BigEndian.PutUint16(rtpHeader[2:4], seqNum)
			binary.BigEndian.PutUint32(rtpHeader[4:8], timestamp)

			packet := audiopool.GetBuffer(rtpHeaderSize + len(payload))
			copy(packet[:rtpHeaderSize], rtpHeader)
			copy(packet[rtpHeaderSize:], payload)

			if _, err := cfg.Stream.WriteTo(packet, cfg.RemoteAddr); err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// continue
				} else {
					log.Error("Failed to write RTP packet", zap.Error(err))
				}
			}

			audiopool.PutBuffer(packet)
			if len(payload) > 0 && &payload[0] != &l16Payload[0] {
				audiopool.PutBuffer(payload)
			}
			audiopool.PutBuffer(l16Payload)
		}
	}
}

// ReaderConfig contains parameters for StartReader.
type ReaderConfig struct {
	Stream       net.PacketConn
	JitterBuffer rtpdefs.JitterBuffer
	OnTimeout    func()
	OnDTMF       func(digit string, duration uint16, end bool)
	Info         audio.Stream
}

// StartReader reads incoming RTP packets from the stream.
// It detects media timeouts (stale connections) and triggers onTimeout if no packets are received for 10s.
func StartReader(ctx context.Context, log *zap.Logger, cfg ReaderConfig) {
	log = log.Named("rtp_reader")
	if cfg.Stream == nil {
		log.Error("RTP stream is nil, reader cannot start")
		return
	}
	payloadType := cfg.Info.Codec.PayloadType

	log.Info("Starting RTP reader",
		zap.String("codec", string(cfg.Info.Codec.Name)),
		zap.Int("payload_type", int(payloadType)),
		zap.Int("dtmf_payload_type", int(cfg.Info.DTMFPayloadType)),
	)
	defer log.Info("RTP reader stopped")

	lastPacketTime := time.Now()
	timeoutDuration := 10 * time.Second
	var lastDTMFTimestamp uint32

	// Initial deadline
	_ = cfg.Stream.SetReadDeadline(time.Now().Add(1 * time.Second))

	for {
		// Allocate a buffer from the pool for reading the packet.
		// Standardize on 1500 bytes for RTP reads, which fits standard MTU.
		// We use a buffer from the default pool (2048 bytes).
		buffer := audiopool.GetBuffer(1500)

		n, _, err := cfg.Stream.ReadFrom(buffer)
		readTime := time.Now()

		if ctx.Err() != nil {
			audiopool.PutBuffer(buffer)
			return
		}

		if err != nil {
			audiopool.PutBuffer(buffer)
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Check for overall media timeout
				if time.Since(lastPacketTime) > timeoutDuration {
					log.Warn("RTP media timeout detected (no packets received), ending call")
					if cfg.OnTimeout != nil {
						cfg.OnTimeout()
					}
					return
				}
				// Reset deadline and continue
				_ = cfg.Stream.SetReadDeadline(time.Now().Add(1 * time.Second))
				continue // Expected error for checking context, continue loop
			}
			log.Error("Failed to read from RTP stream", zap.Error(err))
			return
		}

		if n < 1 {
			audiopool.PutBuffer(buffer)
			continue // Ignore empty packets
		}

		// Update activity timer
		lastPacketTime = readTime

		// Minimal RTP header validation and extraction
		packet, err := parsePacket(buffer[:n], buffer)
		if err != nil {
			// Log mostly debug, as malformed packets might happen
			audiopool.PutBuffer(buffer)
			continue
		}
		// Check for DTMF
		if cfg.Info.DTMFPayloadType != 0 && packet.Raw.PayloadType == cfg.Info.DTMFPayloadType {
			event, err := ParseDTMFPayload(packet.Payload)
			if err == nil {
				if cfg.OnDTMF != nil {
					// Deduplicate end packets based on RTP timestamp
					if event.End {
						if packet.Raw.Timestamp != lastDTMFTimestamp {
							lastDTMFTimestamp = packet.Raw.Timestamp
							cfg.OnDTMF(event.Digit, event.Duration, event.End)
						}
					} else {
						cfg.OnDTMF(event.Digit, event.Duration, event.End)
					}
				}
			} else {
				log.Warn("Failed to parse DTMF payload", zap.Error(err))
			}
			// Fall through to push the packet to the jitter buffer.
			// This is CRITICAL to maintain sequence numbers and prevent the jitter buffer
			// from stalling and growing its internal queue unbounded.
		}

		// Push packet object to jitter buffer
		cfg.JitterBuffer.Push(packet)
	}
}

// parsePacket extracts the payload and metadata from a raw RTP packet.
// The provided buffer `buf` MUST be allocated from the audio pool.
// The returned RTPPacket will contain a slice that points directly to the original pooled buffer.
func parsePacket(buf []byte, originalBuf []byte) (rtpdefs.RTPPacket, error) {
	packet := rtpdefs.GetPacket()
	if err := packet.Unmarshal(buf); err != nil {
		rtpdefs.PutPacket(packet)
		return rtpdefs.RTPPacket{}, fmt.Errorf("failed to unmarshal RTP packet: %w", err)
	}

	// We no longer need to copy the payload.
	// The `buf` was allocated freshly from the pool in StartReader.
	// packet.Payload is already a sub-slice of `buf` thanks to pion/rtp unmarshal.

	return rtpdefs.RTPPacket{
		Payload:   packet.Payload,
		Timestamp: packet.Timestamp,
		Sequence:  packet.SequenceNumber,
		Raw:       packet,
		RawBuffer: originalBuf,
	}, nil
}
