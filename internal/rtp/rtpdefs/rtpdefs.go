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
package rtpdefs

import (
	"sync"

	"github.com/pion/rtp"
)

// RTPPacket holds the payload and metadata of an RTP packet.
// It can optionally wrap a full pion rtp.Packet.
type RTPPacket struct {
	// --- Pointer-containing fields (GC scan prefix) ---

	// 48 bytes
	Raw       *rtp.Packet
	Payload   []byte
	RawBuffer []byte

	// --- Scalar / Non-pointer fields ---

	// 4 bytes
	Timestamp uint32

	// 2 bytes
	Sequence uint16
}

var packetPool = sync.Pool{
	New: func() any {
		return &rtp.Packet{}
	},
}

// GetPacket retrieves an empty rtp.Packet from the pool.
func GetPacket() *rtp.Packet {
	return packetPool.Get().(*rtp.Packet)
}

// PutPacket returns an rtp.Packet to the pool for reuse.
func PutPacket(p *rtp.Packet) {
	if p == nil {
		return
	}
	packetPool.Put(p)
}

// JitterBuffer defines the contract for handling incoming RTP packets.
type JitterBuffer interface {
	Push(packet RTPPacket)
	Pop() <-chan RTPPacket
	Stop()
	Wait()
}
