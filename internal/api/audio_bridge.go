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
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Heoruwulf/TelePortal/internal/audio"
	"github.com/Heoruwulf/TelePortal/internal/platform/metrics"
	"github.com/Heoruwulf/TelePortal/internal/rtp/rtpdefs"
	pkgapi "github.com/Heoruwulf/TelePortal/pkg/api"
	audiopool "github.com/Heoruwulf/TelePortal/pkg/audio"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// queuedMessage represents a message waiting to be sent to a client.
type queuedMessage struct {
	Payload []byte
	Type    int
}

// Client wraps a WebSocket connection and a buffered channel for outgoing messages.
// This ensures writes to the socket are serialized by a single goroutine (pump).
type Client struct {
	conn       *websocket.Conn
	send       chan queuedMessage
	disconnect sync.Once
}

func newClient(conn *websocket.Conn) *Client {
	return &Client{
		conn: conn,
		send: make(chan queuedMessage, 200), // Buffered to handle typical network jitter without excessive memory usage
	}
}

// writePump pumps messages from the send channel to the websocket connection.
// It ensures that only one goroutine writes to the socket at a time.
func (c *Client) writePump(log *zap.Logger, remove func()) {
	ticker := time.NewTicker(50 * time.Second) // Ping interval, slightly less than the 60s read deadline
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
		// Drain the channel and return any binary payloads to the pool
		for {
			select {
			case message, ok := <-c.send:
				if !ok {
					// Channel is closed, drain is complete
					remove()
					return
				}
				if message.Type == websocket.BinaryMessage {
					audiopool.PutBuffer(message.Payload)
				}
			default:
				remove()
				return
			}
		}
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				// Channel closed by the audio bridge
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(message.Type, message.Payload); err != nil {
				log.Warn("Failed to write to WebSocket client", zap.Error(err), zap.String("remote_addr", c.conn.RemoteAddr().String()))
				if message.Type == websocket.BinaryMessage {
					audiopool.PutBuffer(message.Payload)
				}
				return
			}

			if message.Type == websocket.BinaryMessage {
				audiopool.PutBuffer(message.Payload)
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// AudioBridge manages exactly one WebSocket listener for a single call.
type AudioBridge struct {
	ctx         context.Context
	metrics     metrics.Provider
	OnBye       func()
	recorder    *audio.StereoRecorder
	OnDTMF      func(digit string, duration int)
	log         *zap.Logger
	client      *Client
	audioInput  <-chan rtpdefs.RTPPacket
	audioOutput chan<- []byte
	g           *errgroup.Group
	callID      string
	wsCodec     string
	stream      audio.Stream
	mu          sync.RWMutex
	closeOnce   sync.Once
	reserved    bool
}

// SetOnDTMF sets the callback for outbound DTMF requests.
func (b *AudioBridge) SetOnDTMF(handler func(digit string, duration int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.OnDTMF = handler
}

// SetOnBye sets the callback for outbound BYE requests.
func (b *AudioBridge) SetOnBye(handler func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.OnBye = handler
}

// NewAudioBridge creates a new audio bridge for a call.
func NewAudioBridge(
	ctx context.Context,
	log *zap.Logger,
	m metrics.Provider,
	audioInput <-chan rtpdefs.RTPPacket,
	callID string,
	stream audio.Stream,
	recordingPath string,
	wsCodec string,
) *AudioBridge {
	recorder, err := audio.NewStereoRecorder(ctx, log, recordingPath, callID, stream.Codec.SampleRate)
	if err != nil {
		log.Error("Failed to initialize stereo recorder", zap.Error(err), zap.String("call_id", callID))
	}

	return &AudioBridge{
		log:        log,
		metrics:    m,
		audioInput: audioInput,
		ctx:        ctx,
		callID:     callID,
		stream:     stream,
		wsCodec:    wsCodec,
		recorder:   recorder,
	}
}

// SetAudioOutput sets the channel where audio from WebSockets should be sent.
func (b *AudioBridge) SetAudioOutput(ch chan<- []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.audioOutput = ch
}

// WsCodec returns the configured WebSocket codec.
func (b *AudioBridge) WsCodec() string {
	return b.wsCodec
}

// Start begins the audio processing.
func (b *AudioBridge) Start() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.g != nil {
		return
	}
	var gCtx context.Context
	b.g, gCtx = errgroup.WithContext(b.ctx)
	b.g.Go(func() error {
		return b.runAudioProcessor(gCtx)
	})
}

// Wait waits for the audio processor to finish.
func (b *AudioBridge) Wait() error {
	b.mu.RLock()
	g := b.g
	b.mu.RUnlock()
	if g == nil {
		return nil
	}
	return g.Wait()
}

// CloseAll closes the active WebSocket client channel and other resources.
func (b *AudioBridge) CloseAll() {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		client := b.client
		b.client = nil
		b.reserved = false
		b.mu.Unlock()

		if client != nil {
			client.disconnect.Do(func() {
				close(client.send)
				b.metrics.DecWSConnections()
				b.log.Info("WebSocket client forcefully removed during shutdown", zap.String("remote_addr", client.conn.RemoteAddr().String()))
			})
		}

		if b.recorder != nil {
			if err := b.recorder.Close(); err != nil {
				b.log.Error("Failed to close stereo recorder", zap.Error(err))
			}
		}
	})
}

// BroadcastCallEnded sends a notification that the SIP call has ended.
func (b *AudioBridge) BroadcastCallEnded() {
	now := time.Now().UnixMilli()
	msg, err := json.Marshal(pkgapi.WebSocketMessage{
		Type:      "call_ended",
		Timestamp: now,
		CallEnded: true,
	})
	if err != nil {
		b.log.Error("Failed to marshal call_ended message", zap.Error(err))
		return
	}
	b.broadcast(websocket.TextMessage, msg)
}

// BroadcastDTMF sends a DTMF event to the connected client.
func (b *AudioBridge) BroadcastDTMF(digit string, duration int) {
	now := time.Now().UnixMilli()
	msg, err := json.Marshal(pkgapi.WsDtmfMessage{
		Type:      "dtmf",
		Digit:     digit,
		Duration:  duration,
		Timestamp: now,
	})
	if err != nil {
		b.log.Error("Failed to marshal dtmf message", zap.Error(err))
		return
	}
	b.broadcast(websocket.TextMessage, msg)
}

// TryLock attempts to reserve the single client slot. Returns true if successful.
func (b *AudioBridge) TryLock() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil || b.reserved {
		return false
	}
	b.reserved = true
	return true
}

// Unlock releases the reservation if no client was actually added.
func (b *AudioBridge) Unlock() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved = false
}

// AddClient registers a new WebSocket client and starts the write pump.
func (b *AudioBridge) AddClient(conn *websocket.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.ctx.Err() != nil {
		_ = conn.Close()
		return
	}

	// If a client already exists, close the new connection.
	// This shouldn't happen if TryLock is used correctly.
	if b.client != nil {
		_ = conn.Close()
		return
	}

	client := newClient(conn)
	b.client = client
	b.reserved = false // Fully established

	b.metrics.IncWSConnections()

	// Send initial metadata
	codecName := b.wsCodec
	isBigEndian := false // Default for L16 target
	if b.wsCodec == string(audio.CodecPass) {
		codecName = string(b.stream.Codec.Name)
		isBigEndian = b.stream.Codec.IsBigEndian
	} else if b.wsCodec == string(audio.CodecPCMU) || b.wsCodec == string(audio.CodecPCMA) {
		isBigEndian = false // G.711 is byte-oriented
	}

	metadata := pkgapi.WsMetadataMessage{
		Type:        "metadata",
		Codec:       codecName,
		SampleRate:  b.stream.Codec.SampleRate,
		Channels:    b.stream.Codec.Channels,
		PTime:       b.stream.PTime,
		DTMFEnabled: b.stream.DTMFPayloadType != 0,
		IsBigEndian: isBigEndian,
	}

	if jsonMeta, err := json.Marshal(metadata); err == nil {
		client.send <- queuedMessage{Type: websocket.TextMessage, Payload: jsonMeta}
	}

	// Start write pump
	go client.writePump(b.log, func() {
		b.RemoveClient(conn)
	})

	b.log.Info("WebSocket client added", zap.String("remote_addr", conn.RemoteAddr().String()))
}

// ReadPump handles incoming messages from a WebSocket client. It should be called from the HTTP handler.
func (b *AudioBridge) ReadPump(conn *websocket.Conn) {
	defer b.RemoveClient(conn)

	b.mu.RLock()
	c := b.client
	b.mu.RUnlock()

	if c == nil || c.conn != conn {
		return
	}

	c.conn.SetReadLimit(64 * 1024) // 64KB limit is plenty for audio packets
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		messageType, reader, err := c.conn.NextReader()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				b.log.Warn("WebSocket read error", zap.Error(err))
			}
			break
		}

		// Extend the read deadline since we successfully received a frame header
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		switch messageType {
		case websocket.BinaryMessage:
			// Read the payload into a pooled buffer
			// For WebSockets, we don't know the exact size upfront without reading it all,
			// but we know it should fit in our 64KB read limit.
			// However, typically RTP audio frames are small (320-1000 bytes).
			// We can read into a reasonably sized pooled buffer.
			payload := audiopool.GetBuffer(2048)
			n, readErr := reader.Read(payload)

			// Some messages might be larger than 4K, so we should read it all.
			// For zero-allocation, we'll append to our pooled buffer if needed.
			var fullPayload []byte = payload[:n]

			for readErr == nil {
				if len(fullPayload) == cap(fullPayload) {
					// We need to grow, this is an unexpected large packet, fallback to allocation
					// but this should be rare for audio frames.
					newPayload := audiopool.GetBuffer(cap(fullPayload) * 2)
					copy(newPayload, fullPayload)
					audiopool.PutBuffer(payload)
					payload = newPayload
					fullPayload = payload[:len(fullPayload)]
				}

				var m int
				m, readErr = reader.Read(payload[len(fullPayload):cap(payload)])
				fullPayload = payload[:len(fullPayload)+m]
			}

			// Handle recorder (requires L16 LE)
			if b.recorder != nil {
				actualCodec := b.wsCodec
				if actualCodec == string(audio.CodecPass) {
					actualCodec = string(b.stream.Codec.Name)
				}

				var recorderPayload []byte
				switch actualCodec {
				case string(audio.CodecPCMU):
					recorderPayload, _ = audio.DecodePCMUToL16(fullPayload)
				case string(audio.CodecPCMA):
					recorderPayload, _ = audio.DecodePCMAToL16(fullPayload)
				case string(audio.CodecL16):
					// If PASS mode, the client is sending what SIP negotiates (often BE).
					// If not PASS mode, our WS target is L16 LE (IsBigEndian = false in WS metadata).
					if b.wsCodec == string(audio.CodecPass) && b.stream.Codec.IsBigEndian {
						recorderPayload, _ = audio.DecodeL16BEToL16LE(fullPayload)
					} else {
						recorderPayload = fullPayload // Already LE
					}
				default:
					recorderPayload = fullPayload
				}

				if len(recorderPayload) > 0 {
					b.recorder.PushRight(recorderPayload)
				}

				if len(recorderPayload) > 0 && &recorderPayload[0] != &fullPayload[0] {
					audiopool.PutBuffer(recorderPayload)
				}
			}

			// Forward raw payload to the SIP side (audioOutput).
			b.mu.RLock()
			out := b.audioOutput
			b.mu.RUnlock()

			if out != nil {
				select {
				case out <- payload[:len(fullPayload)]:
				default:
					// SIP side is too slow, drop audio
					audiopool.PutBuffer(payload)
				}
			} else {
				audiopool.PutBuffer(payload)
			}
		case websocket.TextMessage:
			// Read text payload
			payload := audiopool.GetBuffer(1024)
			n, _ := reader.Read(payload) // Ignore error, best effort for small JSON

			var baseMsg struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(payload[:n], &baseMsg); err == nil {
				switch baseMsg.Type {
				case "dtmf":
					var dtmfReq pkgapi.WsDtmfRequest
					if err := json.Unmarshal(payload[:n], &dtmfReq); err == nil {
						b.mu.RLock()
						cb := b.OnDTMF
						b.mu.RUnlock()
						if cb != nil {
							cb(dtmfReq.Digit, dtmfReq.Duration)
						}
					}
				case "bye":
					b.mu.RLock()
					cb := b.OnBye
					b.mu.RUnlock()
					if cb != nil {
						cb()
					}
				case "mark":
					// Existing mark handling
					var markMsg struct {
						ID any `json:"id"`
					}
					if err := json.Unmarshal(payload[:n], &markMsg); err == nil {
						b.log.Debug("Received mark from WebSocket", zap.Any("id", markMsg.ID))
					}
				}
			}
			audiopool.PutBuffer(payload)
		}
	}
}

// RemoveClient unregisters the WebSocket client if it matches.
func (b *AudioBridge) RemoveClient(conn *websocket.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil && b.client.conn == conn {
		client := b.client
		b.client = nil
		b.reserved = false

		client.disconnect.Do(func() {
			close(client.send)
			b.metrics.DecWSConnections()
			b.log.Info("WebSocket client removed", zap.String("remote_addr", conn.RemoteAddr().String()))
		})
	}
}

// HasListeners returns true if there is an active WebSocket client.
func (b *AudioBridge) HasListeners() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.client != nil
}

// runAudioProcessor listens for audio from the jitter buffer and broadcasts it to the client.
func (b *AudioBridge) runAudioProcessor(ctx context.Context) error {
	b.log.Info("Audio processor started",
		zap.String("codec", string(b.stream.Codec.Name)),
		zap.Int("rate", b.stream.Codec.SampleRate),
		zap.Int("channels", b.stream.Codec.Channels),
		zap.Bool("is_big_endian", b.stream.Codec.IsBigEndian),
	)
	defer b.log.Info("Audio processor stopped")
	defer b.CloseAll() // Ensure client is closed when the processor exits.

	// Determine if transcoding is required for WebSockets
	wsCodec := b.wsCodec
	sourceCodec := string(b.stream.Codec.Name)
	transcodingRequired := (wsCodec != string(audio.CodecPass) && wsCodec != sourceCodec)

	for {
		select {
		case <-ctx.Done():
			return nil
		case packet, ok := <-b.audioInput:
			if !ok {
				return nil // Channel closed
			}

			if ctx.Err() != nil {
				if packet.RawBuffer != nil {
					audiopool.PutBuffer(packet.RawBuffer)
				} else {
					audiopool.PutBuffer(packet.Payload)
				}
				rtpdefs.PutPacket(packet.Raw)
				return nil
			}

			// If this is a DTMF packet, ignore it here as it was already handled in the RTP reader.
			// We must still return the buffer to the pool.
			if b.stream.DTMFPayloadType != 0 && packet.Raw != nil && packet.Raw.PayloadType == b.stream.DTMFPayloadType {
				if packet.RawBuffer != nil {
					audiopool.PutBuffer(packet.RawBuffer)
				} else {
					audiopool.PutBuffer(packet.Payload)
				}
				rtpdefs.PutPacket(packet.Raw)
				continue
			}

			// If no one is listening and we are not recording, skip decoding.
			hasListeners := b.HasListeners()
			if !hasListeners && b.recorder == nil {
				if packet.RawBuffer != nil {
					audiopool.PutBuffer(packet.RawBuffer)
				} else {
					audiopool.PutBuffer(packet.Payload)
				}
				rtpdefs.PutPacket(packet.Raw)
				continue
			}

			// Decode to L16 Little-Endian for recording and potentially for the frontend
			var l16Payload []byte
			var decodeErr error
			start := time.Now()

			// We always decode for the recorder if it exists.
			// If transcoding is required for the frontend, we use the decoded l16Payload.
			// If transcoding is NOT required (pass-through), we send the raw payload to the frontend.

			if b.recorder != nil || transcodingRequired {
				switch b.stream.Codec.Name {
				case audio.CodecL16:
					l16Payload, decodeErr = audio.DecodeL16BEToL16LE(packet.Payload)
				case audio.CodecPCMU:
					l16Payload, decodeErr = audio.DecodePCMUToL16(packet.Payload)
				case audio.CodecPCMA:
					l16Payload, decodeErr = audio.DecodePCMAToL16(packet.Payload)
				case audio.CodecOpus:
					decodeErr = fmt.Errorf("opus decoding not implemented")
				default:
					decodeErr = fmt.Errorf("unsupported codec for decoding: %s", b.stream.Codec.Name)
				}

				if decodeErr != nil {
					if b.stream.Codec.Name != audio.CodecOpus {
						b.log.Warn("Failed to decode audio", zap.Error(decodeErr))
					}
					if packet.RawBuffer != nil {
						audiopool.PutBuffer(packet.RawBuffer)
					} else {
						audiopool.PutBuffer(packet.Payload)
					}
					rtpdefs.PutPacket(packet.Raw)
					continue
				}
				b.metrics.ObserveTranscodingDuration(sourceCodec, time.Since(start))
			}

			if b.recorder != nil {
				b.recorder.PushLeft(l16Payload)
			}

			if hasListeners {
				if transcodingRequired {
					// We currently only support transcoding TO L16 LE as the target for frontend if not pass-through.
					// If the target wsCodec was PCMU or PCMA, we would have rejected the call in SDP negotiation
					// if it wasn't a match (because transcoding TO G.711 is not implemented here yet).
					// But for now, the plan implies L16 LE is the target when transcoding.
					b.broadcast(websocket.BinaryMessage, l16Payload)
				} else {
					// Pass-through or source matches target: send raw payload
					// We must copy the payload because broadcast takes ownership and returns to pool,
					// but we also need to return the original packet to pool later.
					// Actually, packet.Payload is what we want to send.

					// To avoid double-free or other issues, let's get a new buffer if we are ALSO recording.
					if b.recorder != nil {
						payloadCopy := audiopool.GetBuffer(len(packet.Payload))
						copy(payloadCopy, packet.Payload)
						b.broadcast(websocket.BinaryMessage, payloadCopy)
					} else {
						// If NOT recording, we can just broadcast packet.Payload and NOT return it to pool here.
						// Wait, the broadcast tool says it returns to pool if it's BinaryMessage.
						// And our loop at the end returns packet.Payload to pool.
						// This is tricky.

						payloadCopy := audiopool.GetBuffer(len(packet.Payload))
						copy(payloadCopy, packet.Payload)
						b.broadcast(websocket.BinaryMessage, payloadCopy)
					}
				}
			} else if transcodingRequired && l16Payload != nil {
				// We decoded it but no one is listening, return l16Payload to pool
				audiopool.PutBuffer(l16Payload)
			}

			if packet.RawBuffer != nil {
				audiopool.PutBuffer(packet.RawBuffer)
			} else {
				audiopool.PutBuffer(packet.Payload)
			}
			rtpdefs.PutPacket(packet.Raw)
		}
	}
}

// broadcast sends a prepared message to the connected client.
func (b *AudioBridge) broadcast(messageType int, payload []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.client == nil {
		if messageType == websocket.BinaryMessage {
			audiopool.PutBuffer(payload)
		}
		return
	}

	msg := queuedMessage{
		Type:    messageType,
		Payload: payload,
	}

	select {
	case b.client.send <- msg:
	default:
		// If the client's buffer is full, drop the message.
		if messageType == websocket.BinaryMessage {
			audiopool.PutBuffer(payload)
		} else {
			b.log.Warn("WebSocket send buffer full, dropped text message")
		}
	}
}
