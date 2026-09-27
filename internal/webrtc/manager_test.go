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
package webrtc

import (
	"context"
	"testing"

	"github.com/heoruwulf/teleportal/internal/call"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	pkgapi "github.com/heoruwulf/teleportal/pkg/api"
	"go.uber.org/zap"
)

func TestCallManager_HandleInboundCall(t *testing.T) {
	var publishedMessage any
	var publishedChannel string
	mockPub := func(ctx context.Context, channel string, message any) error {
		publishedChannel = channel
		publishedMessage = message
		return nil
	}

	callManager := call.NewCallManager(zap.NewNop(), metrics.NewNoOpProvider())
	cm := NewCallManager(zap.NewNop(), mockPub, callManager)

	internalID, err := cm.HandleInboundCall(context.Background(), "test-call-id", "ws://test")
	if err != nil {
		t.Fatalf("HandleInboundCall failed: %v", err)
	}

	if internalID == "" {
		t.Error("expected internalID to be generated, got empty string")
	}

	if publishedChannel != pkgapi.RedisChannelWebRTCCallEvents {
		t.Errorf("expected channel %s, got %s", pkgapi.RedisChannelWebRTCCallEvents, publishedChannel)
	}

	if publishedMessage == nil {
		t.Error("expected a published message, got nil")
	}
}
