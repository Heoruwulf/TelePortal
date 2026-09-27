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
	"net"
	"testing"

	"go.uber.org/zap"
)

func TestRTPManager_PortAllocation(t *testing.T) {
	log := zap.NewNop()

	t.Run("InvalidRange", func(t *testing.T) {
		if _, err := NewRTPManager(log, 500, 1000); err == nil {
			t.Errorf("expected error for portMin < 1024")
		}
		if _, err := NewRTPManager(log, 2000, 1000); err == nil {
			t.Errorf("expected error for portMax <= portMin")
		}
	})

	t.Run("SequentialAllocationAndWrap", func(t *testing.T) {
		// Use a local test range: 40000 to 40004 (even ports: 40000, 40002, 40004)
		rm, err := NewRTPManager(log, 40000, 40004)
		if err != nil {
			t.Fatalf("failed to create RTPManager: %v", err)
		}

		loopback := net.IPv4(127, 0, 0, 1)

		conn1, err := rm.CreateListener(loopback)
		if err != nil {
			t.Fatalf("failed to create listener 1: %v", err)
		}
		defer rm.ReleaseListener(conn1)

		conn2, err := rm.CreateListener(loopback)
		if err != nil {
			t.Fatalf("failed to create listener 2: %v", err)
		}
		defer rm.ReleaseListener(conn2)

		conn3, err := rm.CreateListener(loopback)
		if err != nil {
			t.Fatalf("failed to create listener 3: %v", err)
		}
		defer rm.ReleaseListener(conn3)

		// 4th listener should fail because range is exhausted
		conn4, err := rm.CreateListener(loopback)
		if err == nil {
			rm.ReleaseListener(conn4)
			t.Fatalf("expected exhaustion error, but succeeded")
		}

		// Release conn2 and verify it can be reallocated
		p2 := conn2.LocalAddr().(*net.UDPAddr).Port
		rm.ReleaseListener(conn2)

		reallocated, err := rm.CreateListener(loopback)
		if err != nil {
			t.Fatalf("failed to reallocate released port: %v", err)
		}
		defer rm.ReleaseListener(reallocated)

		if gotPort := reallocated.LocalAddr().(*net.UDPAddr).Port; gotPort != p2 {
			t.Errorf("expected reallocated port %d, got %d", p2, gotPort)
		}
	})
}
