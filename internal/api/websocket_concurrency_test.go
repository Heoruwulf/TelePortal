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
package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/heoruwulf/teleportal/internal/call"
	"github.com/heoruwulf/teleportal/internal/platform/config"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type mockAudioBridge struct {
	reserved  bool
	hasClient bool
}

func (m *mockAudioBridge) Start()                                             {}
func (m *mockAudioBridge) Wait() error                                        { return nil }
func (m *mockAudioBridge) BroadcastCallEnded()                                {}
func (m *mockAudioBridge) BroadcastDTMF(digit string, duration int)           {}
func (m *mockAudioBridge) SetOnDTMF(handler func(digit string, duration int)) {}
func (m *mockAudioBridge) SetOnBye(handler func())                            {}
func (m *mockAudioBridge) AddClient(conn *websocket.Conn)                     { m.hasClient = true; m.reserved = false }
func (m *mockAudioBridge) ReadPump(conn *websocket.Conn)                      {}
func (m *mockAudioBridge) RemoveClient(conn *websocket.Conn)                  { m.hasClient = false; m.reserved = false }
func (m *mockAudioBridge) SetAudioOutput(ch chan<- []byte)                    {}
func (m *mockAudioBridge) WsCodec() string                                    { return "L16" }
func (m *mockAudioBridge) CloseAll()                                          { m.hasClient = false; m.reserved = false }
func (m *mockAudioBridge) TryLock() bool {
	if m.reserved || m.hasClient {
		return false
	}
	m.reserved = true
	return true
}
func (m *mockAudioBridge) Unlock() { m.reserved = false }

func TestHTTPHandler_HandleUpgrade_Concurrency(t *testing.T) {
	// We don't call t.Parallel() here because we are testing concurrency control which might be sensitive to timing in some mocks,
	// though it should be fine. Actually, t.Parallel() is fine.
	t.Parallel()

	log := zap.NewNop()
	m := metrics.NewNoOpProvider()
	cm := call.NewCallManager(log, m)
	cfg := &config.CoreConfig{}
	var isReady atomic.Bool
	isReady.Store(true)
	h := NewHTTPHandler(log, cm, m, cfg, &isReady)

	e := echo.New()
	h.RegisterHandlers(e)

	callID := "test-call"
	mb := &mockAudioBridge{}
	id := uuid.New()
	ac := &call.ActiveCall{
		ID:          id,
		CallID:      callID,
		AudioBridge: mb,
	}
	cm.Add(ac)

	// First request - should be allowed (will fail at Upgrade because it's not a real WS request, but we want to check the status code before upgrade)
	// Actually, the upgrader will fail if it's not a websocket request.
	// But HandleUpgrade calls TryLock BEFORE Upgrade.

	// Let's manually trigger TryLock to simulate a connection
	if !mb.TryLock() {
		t.Fatal("Expected TryLock to succeed for the first time")
	}

	// Second request - should return 409 Conflict
	req := httptest.NewRequest(http.MethodGet, "/v1/listen/"+id.String()+"/"+callID, nil)
	rec := httptest.NewRecorder()

	// We need to use the handler directly or via Echo.
	// If we use Echo, it will call HandleUpgrade.
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("Expected status 409 Conflict, got %d", rec.Code)
	}

	// Release lock
	mb.Unlock()

	// Third request - should get past TryLock and fail at Upgrade (since it's not a real WS request) with 400 or something from upgrader
	// Wait, gorilla/websocket upgrader returns 400 if it's not a websocket request.
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req)

	if rec2.Code == http.StatusConflict {
		t.Errorf("Expected status NOT to be 409 Conflict after Unlock, got %d", rec2.Code)
	}
}
