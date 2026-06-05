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
	"encoding/binary"
	"strings"

	audiopool "github.com/heoruwulf/teleportal/pkg/audio"
)

// DTMFGenerator creates RFC 2833 (RFC 4733) RTP payloads for DTMF events.
type DTMFGenerator struct {
	sampleRate int
	ptime      int
}

// NewDTMFGenerator creates a new DTMF generator.
func NewDTMFGenerator(sampleRate int, ptime int) *DTMFGenerator {
	if ptime <= 0 {
		ptime = 20
	}
	return &DTMFGenerator{sampleRate: sampleRate, ptime: ptime}
}

// Generate creates a sequence of RFC 2833 DTMF payloads for a given digit and duration.
// The caller is responsible for returning the returned buffers to the audio pool.
func (g *DTMFGenerator) Generate(digit string, durationMs int) [][]byte {
	event := digitToEvent(digit)
	// Calculate total duration in samples
	totalSamples := uint16((g.sampleRate * durationMs) / 1000)

	intervalMs := g.ptime
	if durationMs < intervalMs {
		intervalMs = durationMs
	}
	ptimeSamples := uint16((g.sampleRate * intervalMs) / 1000)

	var payloads [][]byte
	var currentDuration uint16

	// 1. Send incrementing duration packets
	for currentDuration < totalSamples {
		currentDuration += ptimeSamples
		if currentDuration > totalSamples {
			currentDuration = totalSamples
		}

		payload := audiopool.GetBuffer(4)
		payload[0] = event
		payload[1] = 0x0A // Volume 10 (standard-ish)
		binary.BigEndian.PutUint16(payload[2:4], currentDuration)
		payloads = append(payloads, payload)

		if currentDuration == totalSamples {
			break
		}
	}

	// 2. Send 3 end packets (E bit set) as per RFC 2833 retransmission requirements
	for i := 0; i < 3; i++ {
		payload := audiopool.GetBuffer(4)
		payload[0] = event
		payload[1] = 0x8A // E=1 (bit 0), Volume 10
		binary.BigEndian.PutUint16(payload[2:4], totalSamples)
		payloads = append(payloads, payload)
	}

	return payloads
}

func digitToEvent(digit string) byte {
	digit = strings.ToUpper(digit)
	switch {
	case len(digit) == 0:
		return 0
	case digit[0] >= '0' && digit[0] <= '9':
		return digit[0] - '0'
	case digit[0] == '*':
		return 10
	case digit[0] == '#':
		return 11
	case digit[0] >= 'A' && digit[0] <= 'D':
		return 12 + (digit[0] - 'A')
	default:
		return 0
	}
}
