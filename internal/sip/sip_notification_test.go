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

	"github.com/Heoruwulf/TelePortal/internal/audio"
	"github.com/Heoruwulf/TelePortal/internal/call"
	"github.com/Heoruwulf/TelePortal/internal/platform/metrics"
	"github.com/Heoruwulf/TelePortal/internal/rtp/rtpdefs"
	"github.com/Heoruwulf/TelePortal/pkg/api"
	"github.com/emiago/sipgo/sip"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type mockCache struct {
	publishedMessages []string
	mu                sync.Mutex
}

func (m *mockCache) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	return nil
}
func (m *mockCache) Get(ctx context.Context, key string) (string, error) {
	return "", nil
}
func (m *mockCache) Del(ctx context.Context, key string) error {
	return nil
}
func (m *mockCache) Publish(ctx context.Context, channel string, message any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishedMessages = append(m.publishedMessages, string(message.([]byte)))
	return nil
}
func (m *mockCache) Close() error {
	return nil
}

// Add Wait method for mockAudioBridge to implement AudioBridgeInterface
type mockAudioBridge struct{}

func (m *mockAudioBridge) Start()                                             {}
func (m *mockAudioBridge) Wait() error                                        { return nil }
func (m *mockAudioBridge) BroadcastCallEnded()                                {}
func (m *mockAudioBridge) BroadcastDTMF(digit string, duration int)           {}
func (m *mockAudioBridge) SetOnDTMF(handler func(digit string, duration int)) {}
func (m *mockAudioBridge) SetOnBye(handler func())                            {}
func (m *mockAudioBridge) AddClient(conn *websocket.Conn)                     {}
func (m *mockAudioBridge) ReadPump(conn *websocket.Conn)                      {}
func (m *mockAudioBridge) RemoveClient(conn *websocket.Conn)                  {}
func (m *mockAudioBridge) SetAudioOutput(ch chan<- []byte)                    {}
func (m *mockAudioBridge) WsCodec() string                                    { return "L16" }
func (m *mockAudioBridge) CloseAll()                                          {}
func (m *mockAudioBridge) TryLock() bool                                      { return true }
func (m *mockAudioBridge) Unlock()                                            {}

func TestSIPNotification(t *testing.T) {
	log := zap.NewNop()
	mCache := &mockCache{}
	m := metrics.NewNoOpProvider()
	instanceURL := "http://localhost:8080"

	h := &SIPHandler{
		log:         log,
		cache:       mCache,
		metrics:     m,
		instanceURL: instanceURL,
	}

	req := sip.NewRequest(sip.INVITE, sip.Uri{User: "user", Host: "localhost"})
	req.AppendHeader(sip.NewHeader("Call-ID", "test-call-id"))

	codec, _ := audio.NewCodecG711MuLaw()
	streamInfo := audio.Stream{Codec: codec, PTime: 20}

	activeCall := call.NewActiveCall(
		log,
		nil,
		nil,
		req,
		streamInfo,
		0,
		m,
		func(ctx context.Context, log *zap.Logger, audioInput <-chan rtpdefs.RTPPacket, callID string, stream audio.Stream) call.AudioBridgeInterface {
			return &mockAudioBridge{}
		},
	)
	activeCall.RemoteRTPAddr = &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}

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
		_ = h.cache.Publish(context.Background(), api.RedisChannelCallEvents, data)
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
		_ = h.cache.Publish(context.Background(), api.RedisChannelCallEvents, data)
	}

	// Trigger Connected
	activeCall.StartRTPHandlers()

	mCache.mu.Lock()
	if len(mCache.publishedMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(mCache.publishedMessages))
	}
	var event api.CallEvent
	json.Unmarshal([]byte(mCache.publishedMessages[0]), &event)
	if event.Event != "connected" {
		t.Errorf("Expected connected event, got %s", event.Event)
	}
	mCache.mu.Unlock()

	// Trigger Disconnected
	activeCall.EndCall()

	mCache.mu.Lock()
	if len(mCache.publishedMessages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(mCache.publishedMessages))
	}
	json.Unmarshal([]byte(mCache.publishedMessages[1]), &event)
	if event.Event != "disconnected" {
		t.Errorf("Expected disconnected event, got %s", event.Event)
	}
	mCache.mu.Unlock()
}
