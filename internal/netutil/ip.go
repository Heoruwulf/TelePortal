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
package netutil

import (
	"fmt"
	"net"
	"os"
)

// GetOutboundIP determines the preferred outbound IP address of this machine.
// It attempts to find a non-loopback IP address that can be used for communication
// on the local network. This is useful for advertising an address in protocols like SDP.
//
// Note: This method relies on dialing a public address (8.8.8.8) to determine
// the local interface used for outbound traffic. It does not actually send any data.
// It includes a fallback to hostname resolution for environments without internet access.
func GetOutboundIP() (net.IP, error) {
	// This is a common trick to find the local IP used for outbound connections.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// Fallback for environments without internet access (e.g., isolated containers).
		hostname, hostErr := os.Hostname()
		if hostErr != nil {
			return nil, fmt.Errorf("failed to dial and failed to get hostname: %w", hostErr)
		}
		addrs, lookupErr := net.LookupIP(hostname)
		if lookupErr != nil {
			return nil, fmt.Errorf("failed to dial and failed to look up hostname IP: %w", lookupErr)
		}
		for _, addr := range addrs {
			// Find the first non-loopback IPv4 address.
			if ipv4 := addr.To4(); ipv4 != nil && !ipv4.IsLoopback() {
				return ipv4, nil
			}
		}
		return nil, fmt.Errorf("failed to find any non-loopback IP address")
	}
	defer conn.Close()

	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		// This should theoretically not happen with a UDP dial.
		return nil, fmt.Errorf("could not assert local address to *net.UDPAddr")
	}

	return localAddr.IP, nil
}
