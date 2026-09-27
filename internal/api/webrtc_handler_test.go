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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/heoruwulf/teleportal/internal/call"
	"github.com/heoruwulf/teleportal/internal/platform/config"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

func TestHandleWebRTCConnect_NoToken(t *testing.T) {
	e := echo.New()
	cfg := &config.CoreConfig{
		Auth: config.AuthConfig{
			JWTSecret: "secret",
		},
	}
	h := NewHTTPHandler(HTTPHandlerConfig{
		Log:         zap.NewNop(),
		CallManager: call.NewCallManager(zap.NewNop(), metrics.NewNoOpProvider()),
		Metrics:     metrics.NewNoOpProvider(),
		Config:      cfg,
		IsReady:     &atomic.Bool{},
	})
	h.RegisterHandlers(e)

	req := httptest.NewRequest(http.MethodGet, "/v1/webrtc/connect", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}
}

func TestHandleWebRTCConnect_WithToken(t *testing.T) {
	// Simple test for WebSocket Upgrade (we mock the token)
	e := echo.New()
	cfg := &config.CoreConfig{}
	h := NewHTTPHandler(HTTPHandlerConfig{
		Log:         zap.NewNop(),
		CallManager: call.NewCallManager(zap.NewNop(), metrics.NewNoOpProvider()),
		Metrics:     metrics.NewNoOpProvider(),
		Config:      cfg,
		IsReady:     &atomic.Bool{},
	})
	h.RegisterHandlers(e)

	server := httptest.NewServer(e)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "/v1/webrtc/connect"
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial WebSocket: %v", err)
	}
	defer ws.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("Expected 101 Switching Protocols, got %d", resp.StatusCode)
	}
}
