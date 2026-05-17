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

import "time"

const (
	// RedisChannelCallEvents is the Redis Pub/Sub channel used for broadcasting call lifecycle events.
	RedisChannelCallEvents = "teleportal.calls.events"
)

// CallsResponse defines the JSON structure for the /v1/calls endpoint.
type CallsResponse struct {
	Calls []CallDetail `json:"calls"`
}

// CallDetail defines the structure for detailed information about an active call.
type CallDetail struct {
	Headers map[string]string `json:"headers"`
	CallID  string            `json:"call_id"`
}

// CallEvent defines the standard structure for call lifecycle events published to Redis.
type CallEvent struct {
	Timestamp    time.Time         `json:"timestamp"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Event        string            `json:"event"`
	CallID       string            `json:"call_id"`
	InternalID   string            `json:"internal_id"`
	WebSocketURL string            `json:"websocket_url"`
}

// WsHeadersMessage defines the initial headers message sent when a WebSocket connects.
type WsHeadersMessage struct {
	Type    string            `json:"type"`
	Headers map[string]string `json:"headers"`
	Started string            `json:"started"` // time.Time format
}

// WsErrorMessage defines an error message sent over the WebSocket.
type WsErrorMessage struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

// WebSocketMessage is the unified JSON structure for server-to-client WebSocket messages.
type WebSocketMessage struct {
	Type      string `json:"type"` // e.g., "audio", "call_ended", "metadata"
	Timestamp int64  `json:"ts"`   // Unix timestamp in milliseconds
	CallEnded bool   `json:"call_ended,omitempty"`
}

// WsMetadataMessage defines the metadata sent to the client upon connection.
type WsMetadataMessage struct {
	Type        string `json:"type"`
	Codec       string `json:"codec"`
	SampleRate  int    `json:"sample_rate"`
	Channels    int    `json:"channels"`
	PTime       int    `json:"ptime"`
	DTMFEnabled bool   `json:"dtmf_enabled"`
	IsBigEndian bool   `json:"is_big_endian"`
}

// WsDtmfMessage defines a server-to-client DTMF event notification.
type WsDtmfMessage struct {
	Type      string `json:"type"` // "dtmf"
	Digit     string `json:"digit"`
	Duration  int    `json:"duration"` // in milliseconds
	Timestamp int64  `json:"ts"`       // Unix timestamp in milliseconds
}

// WsDtmfRequest defines a client-to-server request to send a DTMF digit.
type WsDtmfRequest struct {
	Type     string `json:"type"` // "dtmf"
	Digit    string `json:"digit"`
	Duration int    `json:"duration"` // in milliseconds
}

// WsByeRequest defines a client-to-server request to hang up the call.
type WsByeRequest struct {
	Type string `json:"type"` // "bye"
}
