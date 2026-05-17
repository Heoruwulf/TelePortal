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
package call

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/Heoruwulf/TelePortal/internal/platform/metrics"
	"go.uber.org/zap"
)

// CallManagerStats holds statistics about the call manager.
type CallManagerStats struct {
	ActiveCalls int64 `json:"active_calls"`
	TotalCalls  int64 `json:"total_calls"`
}

// CallManager provides a thread-safe store for all active calls.
type CallManager struct {
	log          *zap.Logger
	metrics      metrics.Provider
	calls        *DualIndexedArray[ActiveCall]
	emptyWaiters []chan struct{}
	activeCalls  atomic.Int64
	totalCalls   atomic.Int64
	mu           sync.Mutex
}

// NewCallManager creates a new call manager.
func NewCallManager(log *zap.Logger, m metrics.Provider) *CallManager {
	return &CallManager{
		log:     log.Named("call_manager"),
		metrics: m,
		calls: NewDualIndexedArray(1024,
			func(c *ActiveCall) string { return c.CallID },
			func(c *ActiveCall) string { return c.ID.String() },
		),
	}
}

// Stats returns current statistics.
func (m *CallManager) Stats() CallManagerStats {
	return CallManagerStats{
		ActiveCalls: m.activeCalls.Load(),
		TotalCalls:  m.totalCalls.Load(),
	}
}

// Add stores a new active call.
func (m *CallManager) Add(call *ActiveCall) {
	m.calls.Add(call)
	newCount := m.activeCalls.Add(1)
	m.totalCalls.Add(1)
	m.metrics.UpdateActiveCalls(int(newCount))
	m.log.Info("New call added to manager", zap.String("sip_call_id", call.CallID), zap.String("internal_id", call.ID.String()))
}

// Get retrieves an active call by its SIP Call-ID.
func (m *CallManager) Get(callID string) (*ActiveCall, bool) {
	return m.calls.GetByKey1(callID)
}

// GetByInternalID retrieves an active call by its internal UUID string.
func (m *CallManager) GetByInternalID(internalID string) (*ActiveCall, bool) {
	return m.calls.GetByKey2(internalID)
}

// Remove deletes a call from the manager.
func (m *CallManager) Remove(callID string) {
	// Need to check if it exists before decrementing the count
	if _, ok := m.calls.GetByKey1(callID); ok {
		m.calls.RemoveByKey1(callID)
		newCount := m.activeCalls.Add(-1)
		m.metrics.UpdateActiveCalls(int(newCount))
		m.log.Info("Call removed from manager", zap.String("sip_call_id", callID))

		if newCount == 0 {
			m.mu.Lock()
			for _, w := range m.emptyWaiters {
				close(w)
			}
			m.emptyWaiters = nil
			m.mu.Unlock()
		}
	}
}

// WaitEmpty blocks until the active call count drops to zero or the context is canceled.
func (m *CallManager) WaitEmpty(ctx context.Context) error {
	if m.activeCalls.Load() == 0 {
		return nil
	}

	ch := make(chan struct{})
	m.mu.Lock()
	// Re-check after locking to avoid race
	if m.activeCalls.Load() == 0 {
		m.mu.Unlock()
		return nil
	}
	m.emptyWaiters = append(m.emptyWaiters, ch)
	m.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *CallManager) ListCallIDs() []string {
	activeCalls := m.calls.Values()
	ids := make([]string, 0, len(activeCalls))
	for _, call := range activeCalls {
		ids = append(ids, call.CallID)
	}
	return ids
}

// ListActiveCalls returns a slice of all currently active calls.
func (m *CallManager) ListActiveCalls() []*ActiveCall {
	return m.calls.Values()
}

// HangupCall initiates a graceful teardown of a call by sending a BYE request.
func (m *CallManager) HangupCall(ctx context.Context, callID string) error {
	call, ok := m.Get(callID)
	if !ok {
		m.log.Warn("Attempted to hang up a non-existent call", zap.String("sip_call_id", callID))
		return nil // Not an error if the call is already gone
	}

	m.log.Info("Programmatically hanging up call", zap.String("sip_call_id", callID))
	return call.Dialog.Bye(ctx)
}

// StopAll terminates all active calls, e.g., during a graceful shutdown.
func (m *CallManager) StopAll(ctx context.Context) {
	activeCalls := m.calls.Values()

	m.log.Info("Stopping all active calls", zap.Int("count", len(activeCalls)))
	for _, call := range activeCalls {
		// Use a background context for hangup as the app shutdown context might be too short.
		if err := m.HangupCall(context.Background(), call.CallID); err != nil {
			m.log.Error("Error hanging up call during shutdown", zap.String("sip_call_id", call.CallID), zap.Error(err))
		}
	}
}
