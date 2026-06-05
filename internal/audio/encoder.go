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
	"fmt"

	audiopool "github.com/heoruwulf/teleportal/pkg/audio"
)

// EncodeL16ToPCMU converts L16 LE to PCMU using a lookup table.
func EncodeL16ToPCMU(l16 []byte) ([]byte, error) {
	if len(l16)%2 != 0 {
		return nil, fmt.Errorf("invalid L16 payload length: %d", len(l16))
	}

	pcmu := audiopool.GetBuffer(len(l16) / 2)
	for i := 0; i < len(pcmu); i++ {
		sample := int16(l16[i*2]) | int16(l16[i*2+1])<<8
		// Cast to uint16 to use as an index for the 65536-element array
		pcmu[i] = pcmToMuLawTable[uint16(sample)]
	}
	return pcmu, nil
}

// EncodeL16ToPCMA converts L16 LE to PCMA using a lookup table.
func EncodeL16ToPCMA(l16 []byte) ([]byte, error) {
	if len(l16)%2 != 0 {
		return nil, fmt.Errorf("invalid L16 payload length: %d", len(l16))
	}

	pcma := audiopool.GetBuffer(len(l16) / 2)
	for i := 0; i < len(pcma); i++ {
		sample := int16(l16[i*2]) | int16(l16[i*2+1])<<8
		// Cast to uint16 to use as an index for the 65536-element array
		pcma[i] = pcmToAlawTable[uint16(sample)]
	}
	return pcma, nil
}
