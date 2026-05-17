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
	"fmt"
	"net"
	"sync"

	"go.uber.org/zap"
)

// RTPManager handles the lifecycle of RTP ports for active calls.
type RTPManager struct {
	// --- Pointer-containing fields (GC scan prefix) ---

	// 8 bytes
	log   *zap.Logger
	ports map[uint16]bool // true if port is in use

	// --- Scalar / Non-pointer fields ---

	// 8 bytes
	mu sync.Mutex

	// 2 bytes
	portMin uint16
	portMax uint16
}

// NewRTPManager creates a manager for a given port range.
func NewRTPManager(log *zap.Logger, portMin, portMax uint16) (*RTPManager, error) {
	if portMin < 1024 || portMax < 1024 || portMax <= portMin {
		return nil, fmt.Errorf("invalid port range: %d-%d", portMin, portMax)
	}
	return &RTPManager{
		log:     log.Named("rtp_manager"),
		portMin: portMin,
		portMax: portMax,
		ports:   make(map[uint16]bool),
	}, nil
}

// CreateListener finds an available even-numbered UDP port and starts listening on it.
// RTP typically uses an even port number, with RTCP on the next odd port.
func (m *RTPManager) CreateListener(listenIP net.IP) (net.PacketConn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for port := m.portMin; port <= m.portMax; port += 2 {
		if _, inUse := m.ports[port]; inUse {
			continue
		}

		addr := &net.UDPAddr{IP: listenIP, Port: int(port)}
		conn, err := net.ListenPacket("udp", addr.String())
		if err != nil {
			m.log.Warn("Failed to bind to RTP port, trying next", zap.Int("port", int(port)), zap.Error(err))
			continue
		}

		if udpConn, ok := conn.(*net.UDPConn); ok {
			_ = udpConn.SetReadBuffer(1024 * 1024 * 2)  // 2MB receive buffer
			_ = udpConn.SetWriteBuffer(1024 * 1024 * 2) // 2MB send buffer
		}

		m.log.Debug("Successfully created RTP listener", zap.String("addr", conn.LocalAddr().String()))

		m.ports[port] = true
		return conn, nil
	}

	return nil, fmt.Errorf("no available RTP ports in range %d-%d", m.portMin, m.portMax)
}

// ReleaseListener closes the connection and marks the port as available.
func (m *RTPManager) ReleaseListener(conn net.PacketConn) {
	if conn == nil {
		return
	}

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		m.log.Error("Failed to get UDP address from packet connection on release")
		return
	}
	port := uint16(addr.Port)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := conn.Close(); err != nil {
		m.log.Error("Failed to close RTP listener", zap.Uint16("port", port), zap.Error(err))
	} else {
		m.log.Debug("Closed RTP listener", zap.Uint16("port", port))
	}

	delete(m.ports, port)
}
