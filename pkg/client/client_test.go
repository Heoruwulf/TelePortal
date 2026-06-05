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
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/heoruwulf/teleportal/pkg/api"
	"github.com/heoruwulf/teleportal/pkg/audio"
)

var upgrader = websocket.Upgrader{}

func TestClient_Connect(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send metadata
		meta := api.WsMetadataMessage{
			Type:       "metadata",
			Codec:      "PCMU",
			SampleRate: 8000,
			PTime:      20,
		}
		_ = conn.WriteJSON(meta)

		// Wait a bit then close
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{URL: url})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	if client.Metadata().Codec != "PCMU" {
		t.Errorf("Expected codec PCMU, got %s", client.Metadata().Codec)
	}
}

func TestClient_AudioFlow(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send metadata
		meta := api.WsMetadataMessage{
			Type:       "metadata",
			Codec:      "PCMU",
			SampleRate: 8000,
			PTime:      20,
		}
		_ = conn.WriteJSON(meta)

		// Echo audio back
		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if mt == websocket.BinaryMessage {
				_ = conn.WriteMessage(mt, p)
			}
		}
		close(done)
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{URL: url})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	testData := []byte{1, 2, 3, 4, 5}
	if err := client.Write(ctx, testData); err != nil {
		t.Fatalf("Failed to write audio: %v", err)
	}

	received, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read audio: %v", err)
	}

	if len(received) != len(testData) {
		t.Errorf("Expected %d bytes, got %d", len(testData), len(received))
	}
	for i := range testData {
		if received[i] != testData[i] {
			t.Errorf("Byte mismatch at %d: expected %d, got %d", i, testData[i], received[i])
		}
	}
	audio.PutBuffer(received)
}

func TestClient_Muting(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteJSON(api.WsMetadataMessage{
			Type:       "metadata",
			Codec:      "PCMU",
			SampleRate: 8000,
			PTime:      20,
		})

		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if mt == websocket.BinaryMessage {
				_ = conn.WriteMessage(mt, p)
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{URL: url})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test MuteOutbound
	client.MuteOutbound(true)
	testData := []byte{1, 2, 3, 4, 5}
	_ = client.Write(ctx, testData)

	received, _ := client.Read(ctx)
	for _, b := range received {
		if b != 0 {
			t.Errorf("Expected silence when muted outbound, got %d", b)
			break
		}
	}
	audio.PutBuffer(received)

	// Test MuteInbound
	client.MuteOutbound(false)
	client.MuteInbound(true)
	_ = client.Write(ctx, testData)

	// Read should timeout or we should receive nothing
	readCtx, readCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer readCancel()
	_, err = client.Read(readCtx)
	if err == nil {
		t.Error("Expected timeout when muted inbound, but received data")
	}
}

func TestClient_ListenOnly(t *testing.T) {
	t.Parallel()

	receivedChan := make(chan []byte, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteJSON(api.WsMetadataMessage{
			Type:       "metadata",
			Codec:      "PCMU",
			SampleRate: 8000,
			PTime:      20,
		})

		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if mt == websocket.BinaryMessage {
				buf := make([]byte, len(p))
				copy(buf, p)
				receivedChan <- buf
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{URL: url, ListenOnly: true})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Wait for at least one silent frame
	select {
	case buf := <-receivedChan:
		if len(buf) == 0 {
			t.Error("Received empty frame")
		}
		for _, b := range buf {
			if b != 0 {
				t.Errorf("Expected silence in ListenOnly mode, got %d", b)
				break
			}
		}
	case <-time.After(1 * time.Second):
		t.Error("Timed out waiting for silent frames in ListenOnly mode")
	}
}

func TestClient_Close(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteJSON(api.WsMetadataMessage{
			Type:       "metadata",
			Codec:      "PCMU",
			SampleRate: 8000,
			PTime:      20,
		})

		// Keep connection open until client closes
		_, _, _ = conn.NextReader()
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx := context.Background()

	client, err := NewClient(ctx, Config{URL: url})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}

	// Verify that Write returns error after close
	if err := client.Write(ctx, []byte{1, 2, 3}); err == nil {
		t.Error("Expected error when writing to closed client")
	}
}

func TestClient_DTMFAndHangup(t *testing.T) {
	t.Parallel()

	dtmfReceived := make(chan api.WsDtmfRequest, 1)
	byeReceived := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteJSON(api.WsMetadataMessage{
			Type:        "metadata",
			Codec:       "PCMU",
			SampleRate:  8000,
			PTime:       20,
			DTMFEnabled: true,
		})

		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if mt == websocket.TextMessage {
				var msg map[string]any
				json.Unmarshal(p, &msg)
				if msg["type"] == "dtmf" {
					var req api.WsDtmfRequest
					json.Unmarshal(p, &req)
					dtmfReceived <- req
				} else if msg["type"] == "bye" {
					byeReceived <- struct{}{}
				}
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{URL: url})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// 1. Send DTMF
	if err := client.SendDTMF(ctx, "5", 160); err != nil {
		t.Fatalf("SendDTMF failed: %v", err)
	}
	select {
	case req := <-dtmfReceived:
		if req.Digit != "5" || req.Duration != 160 {
			t.Errorf("got dtmf %+v, want digit 5, duration 160", req)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for DTMF on server")
	}

	// 2. Send DTMF - Validation Errors
	err = client.SendDTMF(ctx, "", 160)
	if err == nil || !strings.Contains(err.Error(), "must be exactly one character") {
		t.Errorf("expected length error, got %v", err)
	}

	err = client.SendDTMF(ctx, "X", 160)
	if err == nil || !strings.Contains(err.Error(), "invalid DTMF digit") {
		t.Errorf("expected invalid digit error, got %v", err)
	}

	err = client.SendDTMF(ctx, "12", 160)
	if err == nil || !strings.Contains(err.Error(), "must be exactly one character") {
		t.Errorf("expected multi-character error, got %v", err)
	}

	err = client.SendDTMF(ctx, "1", -10)
	if err == nil || !strings.Contains(err.Error(), "invalid DTMF duration") {
		t.Errorf("expected negative duration error, got %v", err)
	}

	err = client.SendDTMF(ctx, "1", 10000)
	if err == nil || !strings.Contains(err.Error(), "invalid DTMF duration") {
		t.Errorf("expected excessive duration error, got %v", err)
	}

	// 3. Send Hangup
	if err := client.Hangup(ctx); err != nil {
		t.Fatalf("Hangup failed: %v", err)
	}
	select {
	case <-byeReceived:
		// success
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for BYE on server")
	}
}

func TestClient_OnDTMF(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteJSON(api.WsMetadataMessage{
			Type:        "metadata",
			Codec:       "PCMU",
			SampleRate:  8000,
			PTime:       20,
			DTMFEnabled: true,
		})

		time.Sleep(100 * time.Millisecond)
		_ = conn.WriteJSON(api.WsDtmfMessage{
			Type:      "dtmf",
			Digit:     "7",
			Duration:  200,
			Timestamp: time.Now().UnixMilli(),
		})

		// Keep connection open
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{URL: url})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	receivedDigit := make(chan string, 1)
	client.OnDTMF(func(digit string, duration int) {
		receivedDigit <- digit
	})

	select {
	case digit := <-receivedDigit:
		if digit != "7" {
			t.Errorf("expected digit 7, got %s", digit)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for DTMF from server")
	}
}
