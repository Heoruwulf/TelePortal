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
package sip

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/call"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/heoruwulf/teleportal/pkg/api"
	"go.uber.org/zap"
)

func TestSIPNotification(t *testing.T) {
	log := zap.NewNop()
	var publishedMessages []string
	var pubMu sync.Mutex
	mockPub := func(ctx context.Context, channel string, message any) error {
		pubMu.Lock()
		defer pubMu.Unlock()
		publishedMessages = append(publishedMessages, string(message.([]byte)))
		return nil
	}
	m := metrics.NewNoOpProvider()
	instanceURL := "http://localhost:8080"

	h := &SIPHandler{
		log:         log,
		publisher:   mockPub,
		metrics:     m,
		instanceURL: instanceURL,
	}

	req := sip.NewRequest(sip.INVITE, sip.Uri{User: "user", Host: "localhost"})
	req.AppendHeader(sip.NewHeader("Call-ID", "test-call-id"))

	codec, _ := audio.NewCodecG711MuLaw()
	streamInfo := audio.Stream{Codec: codec, PTime: 20}

	activeCall := call.NewActiveCall(
		log,
		m,
		call.CallConfig{
			CallID:        "test-call-id",
			Headers:       call.ExtractHeaders(req),
			RemoteRTPAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234},
			StreamInfo:    streamInfo,
		},
	)

	// In handleInvite, the hooks are set. Let's simulate that manually as we are testing the hook logic.
	// We'll use the same logic as in sip_handler.go

	callID := "test-call-id"
	wsURL := instanceURL + "/v1/listen/" + activeCall.ID.String() + "/" + callID

	activeCall.OnConnected = func() {
		event := api.CallEvent{
			Event:        "connected",
			CallID:       activeCall.CallID,
			InternalID:   activeCall.ID.String(),
			WebSocketURL: wsURL,
			Timestamp:    time.Now(),
		}
		data, _ := json.Marshal(event)
		_ = h.publisher(context.Background(), api.RedisChannelCallEvents, data)
	}

	activeCall.OnDisconnected = func() {
		event := api.CallEvent{
			Event:        "disconnected",
			CallID:       activeCall.CallID,
			InternalID:   activeCall.ID.String(),
			WebSocketURL: wsURL,
			Timestamp:    time.Now(),
		}
		data, _ := json.Marshal(event)
		_ = h.publisher(context.Background(), api.RedisChannelCallEvents, data)
	}

	// Trigger Connected
	activeCall.StartRTPHandlers()

	pubMu.Lock()
	if len(publishedMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(publishedMessages))
	}
	var event api.CallEvent
	_ = json.Unmarshal([]byte(publishedMessages[0]), &event)
	if event.Event != "connected" {
		t.Errorf("Expected connected event, got %s", event.Event)
	}
	pubMu.Unlock()

	// Trigger Disconnected
	activeCall.EndCall()

	pubMu.Lock()
	if len(publishedMessages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(publishedMessages))
	}
	_ = json.Unmarshal([]byte(publishedMessages[1]), &event)
	if event.Event != "disconnected" {
		t.Errorf("Expected disconnected event, got %s", event.Event)
	}
	pubMu.Unlock()
}
