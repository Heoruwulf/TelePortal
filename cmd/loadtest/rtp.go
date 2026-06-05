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
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/rtp"
	audiopool "github.com/heoruwulf/teleportal/pkg/audio"
	pionrtp "github.com/pion/rtp"
	"golang.org/x/sync/errgroup"
)

type RTPEngine struct {
	conn         *net.UDPConn
	dtmfSent     *int
	dtmfEchoed   *int
	targetIP     net.IP
	payload      []byte
	sampleRate   int
	chunkSize    int
	dtmfToSend   int
	dtmfDuration time.Duration
	localPort    int
	targetPort   int
	payloadType  uint8
}

func NewRTPEngine(localIP net.IP, targetIP net.IP, targetPort int, payloadType uint8, sampleRate int, payload []byte) (*RTPEngine, error) {
	addr := &net.UDPAddr{IP: localIP, Port: 0}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on udp: %w", err)
	}

	_ = conn.SetReadBuffer(1024 * 1024 * 2)  // 2MB receive buffer
	_ = conn.SetWriteBuffer(1024 * 1024 * 2) // 2MB send buffer

	localPort := conn.LocalAddr().(*net.UDPAddr).Port

	samplesPerChunk := (sampleRate * 20) / 1000
	bytesPerSample := 1
	if payloadType == audio.PayloadTypeL16 {
		bytesPerSample = 2
	}
	chunkSize := samplesPerChunk * bytesPerSample

	return &RTPEngine{
		conn:        conn,
		localPort:   localPort,
		targetIP:    targetIP,
		targetPort:  targetPort,
		payload:     payload,
		payloadType: payloadType,
		sampleRate:  sampleRate,
		chunkSize:   chunkSize,
	}, nil
}

func (e *RTPEngine) LocalPort() int {
	return e.localPort
}

func (e *RTPEngine) SetDTMF(count int, callDuration time.Duration, sentTracker, echoedTracker *int) {

	e.dtmfToSend = count
	e.dtmfDuration = callDuration
	e.dtmfSent = sentTracker
	e.dtmfEchoed = echoedTracker
}

// Start sending RTP and receiving echoed packets.
func (e *RTPEngine) Start(ctx context.Context) error {
	// Initialize audio package for G711 tables if needed
	audio.Initialize()

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return e.sendLoop(ctx)
	})

	g.Go(func() error {
		return e.receiveLoop(ctx)
	})

	return g.Wait()
}

func (e *RTPEngine) sendLoop(ctx context.Context) error {
	targetAddr := &net.UDPAddr{IP: e.targetIP, Port: e.targetPort}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	seq := uint16(0)
	timestamp := uint32(0)
	ssrc := uint32(12345) // Dummy SSRC

	chunkSize := e.chunkSize
	offset := 0

	samplesPerChunk := uint32((e.sampleRate * 20) / 1000)

	var dtmfTimes []time.Time
	if e.dtmfToSend > 0 && e.dtmfDuration > 0 {
		interval := e.dtmfDuration / time.Duration(e.dtmfToSend+1)
		now := time.Now()
		for i := 0; i < e.dtmfToSend; i++ {
			// Add a bit of jitter
			jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
			dtmfTimes = append(dtmfTimes, now.Add(interval*time.Duration(i+1)+jitter))
		}
	}

	dtmfGen := rtp.NewDTMFGenerator(e.sampleRate, 20)
	var pendingDTMF [][]byte
	var currentDTMFTimestamp uint32

	defer func() {
		for _, p := range pendingDTMF {
			audiopool.PutBuffer(p)
		}
	}()

	// Pre-fill static RTP header
	rtpHeader := make([]byte, 12)
	rtpHeader[0] = 0x80 // Version 2
	rtpHeader[1] = e.payloadType
	binary.BigEndian.PutUint32(rtpHeader[8:12], ssrc)

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			// Check if it's time to generate new DTMF sequence
			if len(pendingDTMF) == 0 && len(dtmfTimes) > 0 && now.After(dtmfTimes[0]) {
				dtmfTimes = dtmfTimes[1:]
				// Send a random digit
				digits := "0123456789*#"
				digit := string(digits[rand.Intn(len(digits))])

				// If we somehow had leftovers, clear them
				for _, p := range pendingDTMF {
					audiopool.PutBuffer(p)
				}
				pendingDTMF = dtmfGen.Generate(digit, 200) // 200ms DTMF

				// Set timestamp for the whole event
				timestamp += samplesPerChunk
				currentDTMFTimestamp = timestamp

				if e.dtmfSent != nil {
					*e.dtmfSent++
				}
			}

			// If we have DTMF to send, send one packet in place of audio
			if len(pendingDTMF) > 0 {
				p := pendingDTMF[0]
				pendingDTMF = pendingDTMF[1:]

				rtpHeader[1] = 101 // DTMF Payload Type
				binary.BigEndian.PutUint16(rtpHeader[2:4], seq)
				binary.BigEndian.PutUint32(rtpHeader[4:8], currentDTMFTimestamp)

				packet := audiopool.GetBuffer(12 + len(p))
				copy(packet[:12], rtpHeader)
				copy(packet[12:], p)

				_, _ = e.conn.WriteToUDP(packet, targetAddr)

				audiopool.PutBuffer(packet)
				audiopool.PutBuffer(p)

				seq++
				// Advance main timestamp for when audio resumes
				timestamp += samplesPerChunk

				// Restore audio payload type
				rtpHeader[1] = e.payloadType

				// Discard underlying audio chunk to suppress it
				end := offset + chunkSize
				if end > len(e.payload) {
					offset = 0
				} else {
					offset += chunkSize
				}
				continue
			}

			// Otherwise send normal audio
			end := offset + chunkSize
			if end > len(e.payload) {
				offset = 0
				end = chunkSize
			}

			if end > len(e.payload) {
				end = len(e.payload)
			}

			binary.BigEndian.PutUint16(rtpHeader[2:4], seq)
			binary.BigEndian.PutUint32(rtpHeader[4:8], timestamp)

			payloadSize := end - offset
			packet := audiopool.GetBuffer(12 + payloadSize)
			copy(packet[:12], rtpHeader)
			copy(packet[12:], e.payload[offset:end])

			_, _ = e.conn.WriteToUDP(packet, targetAddr)

			audiopool.PutBuffer(packet)

			seq++
			timestamp += samplesPerChunk
			offset += chunkSize
		}
	}
}

func (e *RTPEngine) receiveLoop(ctx context.Context) error {
	buf := make([]byte, 1500)
	var lastDTMFTimestamp uint32

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		e.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := e.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return nil // Connection closed or other error
		}

		packet := &pionrtp.Packet{}
		if err := packet.Unmarshal(buf[:n]); err != nil {
			continue // Parse error
		}

		// In a real test, we might track latency or verify the payload here.
		if packet.PayloadType == 101 { // DTMF
			if len(packet.Payload) >= 2 {
				// E bit is bit 7 (0x80) of byte 1.
				if (packet.Payload[1] & 0x80) != 0 {
					if packet.Timestamp != lastDTMFTimestamp {
						lastDTMFTimestamp = packet.Timestamp
						if e.dtmfEchoed != nil {
							*e.dtmfEchoed++
						}
					}
				}
			}
		}
	}
}

func (e *RTPEngine) Close() error {
	return e.conn.Close()
}
