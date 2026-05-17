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

import "time"

// Provider defines the interface for collecting application metrics.
// It allows for different implementations (e.g., Prometheus, NoOp for tests).
type Provider interface {
	IncSIPRequests(method string, status string)
	UpdateActiveCalls(count int)
	IncWSConnections()
	DecWSConnections()
	ObserveTranscodingDuration(codec string, duration time.Duration)
	ReportJitterBufferStats(callID string, underflows, silence, dropped uint64)
}

// NoOpProvider is a silent implementation of the Provider interface.
// It is used when metrics are disabled or during unit testing.
type NoOpProvider struct{}

func (n *NoOpProvider) IncSIPRequests(method string, status string)                     {}
func (n *NoOpProvider) UpdateActiveCalls(count int)                                     {}
func (n *NoOpProvider) IncWSConnections()                                               {}
func (n *NoOpProvider) DecWSConnections()                                               {}
func (n *NoOpProvider) ObserveTranscodingDuration(codec string, duration time.Duration) {}
func (n *NoOpProvider) ReportJitterBufferStats(callID string, underflows, silence, dropped uint64) {
}

// NewNoOpProvider returns a new NoOpProvider.
func NewNoOpProvider() *NoOpProvider {
	return &NoOpProvider{}
}
