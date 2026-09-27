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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"go.uber.org/zap"
)

func TestCallManager_WaitEmpty(t *testing.T) {
	t.Parallel()

	log := zap.NewNop()
	m := metrics.NewNoOpProvider()

	t.Run("ReturnsImmediatelyIfEmpty", func(t *testing.T) {
		t.Parallel()
		cm := NewCallManager(log, m)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := cm.WaitEmpty(ctx)
		if err != nil {
			t.Errorf("WaitEmpty() returned error for empty manager: %v", err)
		}
	})

	t.Run("BlocksUntilEmpty", func(t *testing.T) {
		t.Parallel()
		cm := NewCallManager(log, m)
		call := &ActiveCall{CallID: "call-1", ID: uuid.New()}
		cm.Add(call)

		waitDone := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			waitDone <- cm.WaitEmpty(ctx)
		}()

		// Ensure it's blocking
		select {
		case err := <-waitDone:
			t.Fatalf("WaitEmpty() returned too early: %v", err)
		case <-time.After(100 * time.Millisecond):
			// Good, it's blocking
		}

		cm.Remove(call.CallID)

		select {
		case err := <-waitDone:
			if err != nil {
				t.Errorf("WaitEmpty() returned error after removal: %v", err)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("WaitEmpty() timed out after removal")
		}
	})

	t.Run("RespectsContextTimeout", func(t *testing.T) {
		t.Parallel()
		cm := NewCallManager(log, m)
		call := &ActiveCall{CallID: "call-1", ID: uuid.New()}
		cm.Add(call)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := cm.WaitEmpty(ctx)
		if err == nil {
			t.Error("WaitEmpty() should have timed out, but returned nil")
		} else if err != context.DeadlineExceeded {
			t.Errorf("Expected DeadlineExceeded, got: %v", err)
		}
	})

	t.Run("MultipleWaiters", func(t *testing.T) {
		t.Parallel()
		cm := NewCallManager(log, m)
		call := &ActiveCall{CallID: "call-1", ID: uuid.New()}
		cm.Add(call)

		const waiterCount = 5
		errCh := make(chan error, waiterCount)

		for range waiterCount {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				errCh <- cm.WaitEmpty(ctx)
			}()
		}

		time.Sleep(100 * time.Millisecond)
		cm.Remove(call.CallID)

		for i := range waiterCount {
			select {
			case err := <-errCh:
				if err != nil {
					t.Errorf("Waiter %d returned error: %v", i, err)
				}
			case <-time.After(1 * time.Second):
				t.Errorf("Waiter %d timed out", i)
			}
		}
	})
}

func TestCallManager_CRUD(t *testing.T) {
	t.Parallel()

	log := zap.NewNop()
	m := metrics.NewNoOpProvider()
	cm := NewCallManager(log, m)

	c1ID := uuid.New()
	c1 := &ActiveCall{CallID: "sip-call-1", ID: c1ID}
	c2ID := uuid.New()
	c2 := &ActiveCall{CallID: "sip-call-2", ID: c2ID}

	cm.Add(c1)
	cm.Add(c2)

	stats := cm.Stats()
	if stats.ActiveCalls != 2 || stats.TotalCalls != 2 {
		t.Errorf("expected 2 active and total calls, got active=%d total=%d", stats.ActiveCalls, stats.TotalCalls)
	}

	// Lookup by SIP
	got, ok := cm.Get("sip-call-1")
	if !ok || got.CallID != "sip-call-1" {
		t.Fatalf("expected to find c1 by SIP CallID, got ok=%v", ok)
	}

	// Lookup by UUID
	gotUUID, ok := cm.GetByInternalID(c2ID.String())
	if !ok || gotUUID.CallID != "sip-call-2" {
		t.Fatalf("expected to find c2 by UUID, got ok=%v", ok)
	}

	// List active
	active := cm.ListActiveCalls()
	if len(active) != 2 {
		t.Errorf("expected 2 active calls, got %d", len(active))
	}
	ids := cm.ListCallIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 call IDs, got %d", len(ids))
	}

	// Remove c1
	cm.Remove("sip-call-1")
	if _, ok := cm.Get("sip-call-1"); ok {
		t.Errorf("c1 still found after removal")
	}
	if _, ok := cm.GetByInternalID(c1ID.String()); ok {
		t.Errorf("c1 still found by UUID after removal")
	}

	stats = cm.Stats()
	if stats.ActiveCalls != 1 || stats.TotalCalls != 2 {
		t.Errorf("expected 1 active and 2 total calls, got active=%d total=%d", stats.ActiveCalls, stats.TotalCalls)
	}

	// Remove non-existent
	cm.Remove("non-existent")
	stats = cm.Stats()
	if stats.ActiveCalls != 1 {
		t.Errorf("active calls changed on non-existent removal")
	}
}
