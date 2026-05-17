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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Heoruwulf/TelePortal/pkg/api"
	"github.com/Heoruwulf/TelePortal/pkg/audio"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
)

const (
	writeWait       = 10 * time.Second
	pongWait        = 60 * time.Second
	pingPeriod      = (pongWait * 9) / 10
	maxMessageSize  = 65536
	ValidDTMFDigits = "0123456789*#ABCDabcd"
)

// Config holds the client configuration.
type Config struct {
	Metrics    prometheus.Registerer
	Logger     *zap.Logger
	URL        string
	Secret     string
	InternalID string
	CallID     string
	ListenOnly bool
}

// MarshalLogObject implements zapcore.ObjectMarshaler.
func (c Config) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("url", c.URL)
	enc.AddString("internal_id", c.InternalID)
	enc.AddString("call_id", c.CallID)
	enc.AddBool("listen_only", c.ListenOnly)
	if c.Secret == "" {
		enc.AddString("secret", "[EMPTY]")
	} else {
		enc.AddString("secret", "[REDACTED]")
	}
	return nil
}

// queuedMessage represents a message waiting to be sent to the server.
type queuedMessage struct {
	Payload []byte
	Type    int
}

// DTMFHandler is the callback function type for incoming DTMF events.
type DTMFHandler func(digit string, duration int)

// Client is a TelePortal WebSocket audio client.
type Client struct {
	cfg      Config
	ctx      context.Context
	receive  chan []byte
	onDTMF   DTMFHandler
	metrics  *clientMetrics
	conn     *websocket.Conn
	metaWait chan struct{}
	send     chan queuedMessage
	eg       *errgroup.Group
	log      *zap.Logger
	cancel   context.CancelFunc
	metadata api.WsMetadataMessage
	onDTMFMu sync.RWMutex
	metaOnce sync.Once
	muteIn   atomic.Bool
	muteOut  atomic.Bool
	closed   atomic.Bool
}

type clientMetrics struct {
	connections prometheus.Gauge
	bytesSent   prometheus.Counter
	bytesRecv   prometheus.Counter
	errors      *prometheus.CounterVec
}

func newClientMetrics(reg prometheus.Registerer) *clientMetrics {
	m := &clientMetrics{
		connections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "teleportal_client_connections_active",
			Help: "Number of active TelePortal client connections",
		}),
		bytesSent: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "teleportal_client_bytes_sent_total",
			Help: "Total bytes sent by TelePortal client",
		}),
		bytesRecv: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "teleportal_client_bytes_received_total",
			Help: "Total bytes received by TelePortal client",
		}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "teleportal_client_errors_total",
			Help: "Total errors encountered by TelePortal client",
		}, []string{"type"}),
	}
	if reg != nil {
		reg.MustRegister(m.connections, m.bytesSent, m.bytesRecv, m.errors)
	}
	return m
}

// generateToken creates a short-lived JWT scoped to the configured InternalID.
func (c Config) generateToken() (string, error) {
	if c.Secret == "" {
		return "", nil
	}

	claims := jwt.RegisteredClaims{
		Subject:   c.InternalID,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(c.Secret))
}

// NewClient creates and connects a new TelePortal client.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}

	c := &Client{
		cfg:      cfg,
		log:      cfg.Logger,
		metrics:  newClientMetrics(cfg.Metrics),
		send:     make(chan queuedMessage, 100),
		receive:  make(chan []byte, 100),
		metaWait: make(chan struct{}),
	}

	c.ctx, c.cancel = context.WithCancel(ctx)

	dialer := websocket.DefaultDialer
	headers := http.Header{}

	if cfg.Secret != "" {
		tokenString, err := cfg.generateToken()
		if err != nil {
			c.cancel()
			return nil, fmt.Errorf("generating auth token: %w", err)
		}
		headers.Set("Authorization", "Bearer "+tokenString)
	}

	conn, _, err := dialer.DialContext(c.ctx, cfg.URL, headers)
	if err != nil {
		c.cancel()
		return nil, fmt.Errorf("dialing teleportal: %w", err)
	}
	c.conn = conn
	c.conn.SetReadLimit(maxMessageSize)

	c.metrics.connections.Inc()

	c.eg, c.ctx = errgroup.WithContext(c.ctx)
	c.eg.Go(c.readPump)
	c.eg.Go(c.writePump)

	// Wait for metadata or error/timeout
	select {
	case <-c.metaWait:
		// Success
	case <-c.ctx.Done():
		c.Close()
		return nil, fmt.Errorf("handshake failed: %w", c.ctx.Err())
	case <-time.After(10 * time.Second):
		c.Close()
		return nil, errors.New("timeout waiting for metadata")
	}

	if cfg.ListenOnly {
		c.eg.Go(c.listenOnlyPump)
	}

	return c, nil
}

// Read reads an audio frame from the TelePortal server.
func (c *Client) Read(ctx context.Context) ([]byte, error) {
	select {
	case data := <-c.receive:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}

// Write writes an audio frame to the TelePortal server.
func (c *Client) Write(ctx context.Context, data []byte) error {
	if c.closed.Load() {
		return errors.New("client closed")
	}

	var payload []byte
	if c.muteOut.Load() {
		// Send silence instead of provided data
		payload = audio.GetBuffer(len(data))
		for i := range payload {
			payload[i] = 0
		}
	} else {
		// Use a pooled buffer for the write pump to consume
		payload = audio.GetBuffer(len(data))
		copy(payload, data)
	}

	select {
	case c.send <- queuedMessage{Type: websocket.BinaryMessage, Payload: payload}:
		return nil
	case <-ctx.Done():
		audio.PutBuffer(payload)
		return ctx.Err()
	case <-c.ctx.Done():
		audio.PutBuffer(payload)
		return c.ctx.Err()
	}
}

// SendDTMF sends a single DTMF digit to the TelePortal server.
func (c *Client) SendDTMF(ctx context.Context, digit string, duration int) error {
	if !c.metadata.DTMFEnabled {
		return errors.New("DTMF not supported for this call")
	}

	if len(digit) != 1 {
		return errors.New("invalid DTMF digit: must be exactly one character")
	}

	isValid := false
	for _, v := range ValidDTMFDigits {
		if rune(digit[0]) == v {
			isValid = true
			break
		}
	}
	if !isValid {
		return fmt.Errorf("invalid DTMF digit: %s", digit)
	}

	if duration <= 0 || duration > 5000 {
		return fmt.Errorf("invalid DTMF duration: %d (must be between 1 and 5000 ms)", duration)
	}

	msg := api.WsDtmfRequest{
		Type:     "dtmf",
		Digit:    strings.ToUpper(digit), // Ensure we only send a single character, uppercase
		Duration: duration,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	select {
	case c.send <- queuedMessage{Type: websocket.TextMessage, Payload: data}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

// Hangup requests the TelePortal server to hang up the SIP call.
func (c *Client) Hangup(ctx context.Context) error {
	msg := api.WsByeRequest{
		Type: "bye",
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	select {
	case c.send <- queuedMessage{Type: websocket.TextMessage, Payload: data}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

// OnDTMF registers a handler for incoming DTMF events from the server.
func (c *Client) OnDTMF(handler DTMFHandler) {
	c.onDTMFMu.Lock()
	defer c.onDTMFMu.Unlock()
	c.onDTMF = handler
}

// MuteInbound enables or disables inbound audio muting.
func (c *Client) MuteInbound(mute bool) {
	c.muteIn.Store(mute)
}

// MuteOutbound enables or disables outbound audio muting.
func (c *Client) MuteOutbound(mute bool) {
	c.muteOut.Store(mute)
}

// Close closes the client connection and stops all goroutines.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	c.cancel()
	if c.conn != nil {
		_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = c.conn.Close()
	}

	err := c.eg.Wait()

	// Drain channels back to pool
	close(c.send)
	for m := range c.send {
		if m.Type == websocket.BinaryMessage {
			audio.PutBuffer(m.Payload)
		}
	}
	close(c.receive)
	for b := range c.receive {
		audio.PutBuffer(b)
	}

	c.metrics.connections.Dec()

	return err
}

// Metadata returns the metadata received from the server.
func (c *Client) Metadata() api.WsMetadataMessage {
	return c.metadata
}

func (c *Client) readPump() error {
	defer c.cancel()

	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		mt, reader, err := c.conn.NextReader()
		if err != nil {
			if c.ctx.Err() != nil {
				return nil
			}
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure) {
				c.log.Error("WebSocket read error", zap.Error(err))
				c.metrics.errors.WithLabelValues("read").Inc()
			}
			return err
		}

		switch mt {
		case websocket.BinaryMessage:
			if c.muteIn.Load() {
				continue
			}

			// Read directly into pooled buffer
			// We don't know the size, but audio frames are usually small.
			// Let's use 4096 as a safe upper bound for single frames.
			buf := audio.GetBuffer(4096)
			n, err := reader.Read(buf)
			if err != nil && err.Error() != "EOF" {
				audio.PutBuffer(buf)
				continue
			}
			data := buf[:n]

			c.metrics.bytesRecv.Add(float64(n))

			select {
			case c.receive <- data:
			default:
				// Receiver slow, drop frame
				audio.PutBuffer(buf)
			}

		case websocket.TextMessage:
			var msg map[string]any
			if err := json.NewDecoder(reader).Decode(&msg); err != nil {
				continue
			}

			msgType, _ := msg["type"].(string)
			switch msgType {
			case "metadata":
				var meta api.WsMetadataMessage
				data, _ := json.Marshal(msg)
				if err := json.Unmarshal(data, &meta); err == nil {
					c.metaOnce.Do(func() {
						c.metadata = meta
						close(c.metaWait)
					})
				}
			case "dtmf":
				var dtmfMsg api.WsDtmfMessage
				data, _ := json.Marshal(msg)
				if err := json.Unmarshal(data, &dtmfMsg); err == nil {
					c.onDTMFMu.RLock()
					cb := c.onDTMF
					c.onDTMFMu.RUnlock()
					if cb != nil {
						cb(dtmfMsg.Digit, dtmfMsg.Duration)
					}
				}
			case "call_ended":
				c.log.Info("Call ended by server")
				return nil
			case "error":
				errMsg, _ := msg["error"].(string)
				c.log.Error("Server error", zap.String("error", errMsg))
			}
		}
	}
}

func (c *Client) writePump() error {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return nil
		case msg, ok := <-c.send:
			if !ok {
				return nil
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(msg.Type, msg.Payload); err != nil {
				if msg.Type == websocket.BinaryMessage {
					audio.PutBuffer(msg.Payload)
				}
				if c.ctx.Err() != nil {
					return nil
				}
				return err
			}
			if msg.Type == websocket.BinaryMessage {
				c.metrics.bytesSent.Add(float64(len(msg.Payload)))
				audio.PutBuffer(msg.Payload)
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return err
			}
		}
	}
}

func (c *Client) listenOnlyPump() error {
	// Wait for metadata to know ptime and sample rate
	select {
	case <-c.metaWait:
	case <-c.ctx.Done():
		return nil
	}

	ptime := c.metadata.PTime
	if ptime == 0 {
		ptime = 20 // Default 20ms
	}

	bytesPerTick := (c.metadata.SampleRate * ptime) / 1000
	switch c.metadata.Codec {
	case "PCMU", "PCMA":
		// G.711 is 1 byte per sample
	case "L16":
		bytesPerTick *= 2
	default:
		// Unknown codec, assume L16 for safety or PCMU?
		// For now, let's stick to metadata.
	}

	ticker := time.NewTicker(time.Duration(ptime) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return nil
		case <-ticker.C:
			buf := audio.GetBuffer(bytesPerTick)
			for i := range buf {
				buf[i] = 0
			}
			select {
			case c.send <- queuedMessage{Type: websocket.BinaryMessage, Payload: buf}:
			case <-c.ctx.Done():
				audio.PutBuffer(buf)
				return nil
			default:
				// Write pump full, drop silence
				audio.PutBuffer(buf)
			}
		}
	}
}
