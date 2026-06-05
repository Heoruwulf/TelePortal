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
package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/heoruwulf/teleportal/internal/call"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"go.uber.org/zap"
)

func TestApp_Drain(t *testing.T) {
	t.Parallel()

	log := zap.NewNop()
	cm := call.NewCallManager(log, metrics.NewNoOpProvider())

	a := &App{
		log:         log,
		callManager: cm,
	}

	t.Run("ImmediateReturnWhenNoCalls", func(t *testing.T) {
		a.SetReady(true)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := a.Drain(ctx)
		if err != nil {
			t.Errorf("Drain() returned error for empty app: %v", err)
		}
		if a.isReady.Load() {
			t.Error("isReady should be false after Drain")
		}
	})

	t.Run("BlocksUntilCallsFinished", func(t *testing.T) {
		a.SetReady(true)
		callID := "test-call-1"
		cm.Add(&call.ActiveCall{CallID: callID, ID: uuid.New()})

		drainErr := make(chan error, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go func() {
			drainErr <- a.Drain(ctx)
		}()

		// Give it a moment to start
		time.Sleep(50 * time.Millisecond)

		if a.isReady.Load() {
			t.Error("isReady should be false immediately after Drain starts")
		}

		// Should still be blocking
		select {
		case err := <-drainErr:
			t.Fatalf("Drain finished too early: %v", err)
		default:
			// Good
		}

		cm.Remove(callID)

		select {
		case err := <-drainErr:
			if err != nil {
				t.Errorf("Drain returned error: %v", err)
			}
		case <-time.After(1 * time.Second):
			t.Error("Drain timed out after call removal")
		}
	})

	t.Run("RespectsTimeout", func(t *testing.T) {
		a.SetReady(true)
		callID := "test-call-2"
		cm.Add(&call.ActiveCall{CallID: callID, ID: uuid.New()})
		defer cm.Remove(callID)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := a.Drain(ctx)
		if err == nil {
			t.Error("Drain should have timed out, but returned nil")
		} else if err != context.DeadlineExceeded {
			t.Errorf("Expected DeadlineExceeded, got: %v", err)
		}
	})
}
