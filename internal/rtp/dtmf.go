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
	"fmt"
)

var eventToDigit = []string{
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	"*", "#", "A", "B", "C", "D",
}

// ParseDTMFPayload parses an RFC 2833 (RFC 4733) DTMF payload.
// RFC 2833 payload is 4 bytes:
// 0: event (digit)
// 1: E (1 bit), R (1 bit), volume (6 bits)
// 2-3: duration (16 bits, big-endian)
func ParseDTMFPayload(payload []byte) (digit string, end bool, duration uint16, err error) {
	if len(payload) < 4 {
		return "", false, 0, fmt.Errorf("invalid DTMF payload length: %d", len(payload))
	}

	event := payload[0]
	end = (payload[1] & 0x80) != 0
	duration = binary.BigEndian.Uint16(payload[2:4])

	if event < uint8(len(eventToDigit)) {
		digit = eventToDigit[event]
	} else {
		digit = fmt.Sprintf("%d", event)
	}

	return digit, end, duration, nil
}
