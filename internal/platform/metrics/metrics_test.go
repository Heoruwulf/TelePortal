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
package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPrometheusProvider(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	provider := NewPrometheusProvider(registry)

	if provider == nil {
		t.Fatal("expected provider to be non-nil")
	}

	// Verify that we can call methods without panic
	provider.IncSIPRequests("INVITE", "200")
	provider.UpdateActiveCalls(1)
	provider.IncWSConnections()
	provider.DecWSConnections()

	// Since we are using a private registry, we could potentially verify the values,
	// but the main goal here is to ensure no panics and basic functionality.
}

func TestNoOpProvider(t *testing.T) {
	t.Parallel()

	provider := NewNoOpProvider()
	if provider == nil {
		t.Fatal("expected provider to be non-nil")
	}

	// Verify that we can call methods without panic
	provider.IncSIPRequests("INVITE", "200")
	provider.UpdateActiveCalls(1)
	provider.IncWSConnections()
	provider.DecWSConnections()
}
