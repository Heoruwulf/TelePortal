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

	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"go.uber.org/zap"
)

// CallManagerStats holds statistics about the call manager.
type CallManagerStats struct {
	ActiveCalls int64 `json:"active_calls"`
	TotalCalls  int64 `json:"total_calls"`
}

// CallManager provides a thread-safe store for all active calls.
type CallManager struct {
	metrics      metrics.Provider
	log          *zap.Logger
	callsBySIP   map[string]*ActiveCall
	callsByUUID  map[string]*ActiveCall
	emptyWaiters []chan struct{}
	activeCalls  atomic.Int64
	totalCalls   atomic.Int64
	mu           sync.RWMutex
}

// NewCallManager creates a new call manager.
func NewCallManager(log *zap.Logger, m metrics.Provider) *CallManager {
	return &CallManager{
		log:         log.Named("call_manager"),
		metrics:     m,
		callsBySIP:  make(map[string]*ActiveCall),
		callsByUUID: make(map[string]*ActiveCall),
	}
}

// Stats returns current statistics.
func (m *CallManager) Stats() CallManagerStats {
	return CallManagerStats{
		ActiveCalls: m.activeCalls.Load(),
		TotalCalls:  m.totalCalls.Load(),
	}
}

// Metrics returns the configured metrics provider.
func (m *CallManager) Metrics() metrics.Provider {
	return m.metrics
}

// Add stores a new active call.
func (m *CallManager) Add(call *ActiveCall) {
	m.mu.Lock()
	m.callsBySIP[call.CallID] = call
	m.callsByUUID[call.ID.String()] = call
	m.mu.Unlock()

	newCount := m.activeCalls.Add(1)
	m.totalCalls.Add(1)
	m.metrics.UpdateActiveCalls(int(newCount))
	m.log.Info("New call added to manager", zap.String("sip_call_id", call.CallID), zap.String("internal_id", call.ID.String()))
}

// Get retrieves an active call by its SIP Call-ID.
func (m *CallManager) Get(callID string) (*ActiveCall, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.callsBySIP[callID]
	if !ok || c == nil {
		return nil, false
	}
	return c, true
}

// GetByInternalID retrieves an active call by its internal UUID string.
func (m *CallManager) GetByInternalID(internalID string) (*ActiveCall, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.callsByUUID[internalID]
	if !ok || c == nil {
		return nil, false
	}
	return c, true
}

// Remove deletes a call from the manager.
func (m *CallManager) Remove(callID string) {
	m.mu.Lock()
	call, ok := m.callsBySIP[callID]
	var waiters []chan struct{}
	if ok {
		delete(m.callsBySIP, callID)
		delete(m.callsByUUID, call.ID.String())
		newCount := m.activeCalls.Add(-1)
		m.metrics.UpdateActiveCalls(int(newCount))
		m.log.Info("Call removed from manager", zap.String("sip_call_id", callID))

		if newCount == 0 {
			waiters = m.emptyWaiters
			m.emptyWaiters = nil
		}
	}
	m.mu.Unlock()

	if !ok {
		return
	}

	for _, w := range waiters {
		close(w)
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.callsBySIP))
	for id := range m.callsBySIP {
		ids = append(ids, id)
	}
	return ids
}

// ListActiveCalls returns a slice of all currently active calls.
func (m *CallManager) ListActiveCalls() []*ActiveCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	calls := make([]*ActiveCall, 0, len(m.callsBySIP))
	for _, call := range m.callsBySIP {
		calls = append(calls, call)
	}
	return calls
}

// HangupCall initiates a graceful teardown of a call by sending a BYE request.
func (m *CallManager) HangupCall(ctx context.Context, callID string) error {
	call, ok := m.Get(callID)
	if !ok {
		m.log.Warn("Attempted to hang up a non-existent call", zap.String("sip_call_id", callID))
		return nil // Not an error if the call is already gone
	}

	m.log.Info("Programmatically hanging up call", zap.String("sip_call_id", callID))

	if call.Dialog != nil {
		return call.Dialog.Bye(ctx)
	}

	if call.AudioBridge != nil {
		call.AudioBridge.CloseAll()
	}
	m.Remove(callID)

	return nil
}

// StopAll terminates all active calls, e.g., during a graceful shutdown.
func (m *CallManager) StopAll(ctx context.Context) {
	activeCalls := m.ListActiveCalls()

	m.log.Info("Stopping all active calls", zap.Int("count", len(activeCalls)))
	for _, call := range activeCalls {
		// Use a background context for hangup as the app shutdown context might be too short.
		if err := m.HangupCall(context.Background(), call.CallID); err != nil {
			m.log.Error("Error hanging up call during shutdown", zap.String("sip_call_id", call.CallID), zap.Error(err))
		}
	}
}
