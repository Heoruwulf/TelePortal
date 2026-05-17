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

import "sync"

// muLawToPcmTable translates mu-law 8-bit to 16-bit PCM.
var muLawToPcmTable = [256]int16{}

// aLawToPcmTable translates a-law 8-bit to 16-bit PCM.
var aLawToPcmTable = [256]int16{}

// pcmToMuLawTable translates 16-bit PCM to mu-law 8-bit.
var pcmToMuLawTable = [65536]byte{}

// pcmToAlawTable translates 16-bit PCM to a-law 8-bit.
var pcmToAlawTable = [65536]byte{}

var initOnce sync.Once

// Initialize pre-computes the G.711 lookup tables.
// It must be called during the application startup sequence.
// It is thread-safe and guaranteed to execute exactly once.
func Initialize() {
	initOnce.Do(func() {
		// Initialize mu-law table
		for i := 0; i < 256; i++ {
			muLawToPcmTable[i] = muLawToLinear(byte(i))
		}
		// Initialize a-law table
		for i := 0; i < 256; i++ {
			aLawToPcmTable[i] = aLawToLinear(byte(i))
		}

		// Initialize reverse tables for encoding
		for i := 0; i < 65536; i++ {
			sample := int16(uint16(i))
			pcmToMuLawTable[i] = linearToMuLaw(sample)
			pcmToAlawTable[i] = linearToALaw(sample)
		}
	})
}

func muLawToLinear(mu byte) int16 {
	mu = ^mu
	sign := (mu & 0x80) != 0
	exponent := (mu >> 4) & 0x07
	mantissa := mu & 0x0F
	sample := int16((mantissa << 3) + 132)
	sample <<= exponent
	sample -= 132
	if sign {
		return -sample
	}
	return sample
}

func linearToMuLaw(pcm int16) byte {
	p := int32(pcm)
	sign := (p >> 8) & 0x80
	if p < 0 {
		p = -p
	}
	p += 132
	if p > 32767 {
		p = 32767
	}
	exponent := uint8(7)
	for i := uint8(0); i < 7; i++ {
		if (p & (0x4000 >> i)) != 0 {
			exponent = 7 - i
			break
		}
		if i == 6 {
			exponent = 0
		}
	}
	mantissa := (p >> (exponent + 3)) & 0x0F
	return ^(byte(sign) | (exponent << 4) | byte(mantissa))
}

func aLawToLinear(a byte) int16 {
	a ^= 0x55
	sign := (a & 0x80) != 0
	exponent := (a >> 4) & 0x07
	mantissa := a & 0x0F
	var sample int16
	if exponent == 0 {
		sample = int16((mantissa << 4) + 8)
	} else {
		sample = int16(mantissa)<<4 + 264
		sample <<= (exponent - 1)
	}
	if sign {
		return -sample
	}
	return sample
}

func linearToALaw(pcm int16) byte {
	p := int32(pcm)
	sign := (p >> 8) & 0x80
	if p < 0 {
		p = -p
	}
	if p > 32767 {
		p = 32767
	}
	exponent := uint8(7)
	for i := uint8(0); i < 7; i++ {
		if (p & (0x4000 >> i)) != 0 {
			exponent = 7 - i
			break
		}
		if i == 6 {
			exponent = 0
		}
	}
	var mantissa uint8
	if exponent == 0 {
		mantissa = uint8(p>>4) & 0x0F
	} else {
		mantissa = uint8(p>>(exponent+3)) & 0x0F
	}
	return (byte(sign) | (exponent << 4) | mantissa) ^ 0x55
}
