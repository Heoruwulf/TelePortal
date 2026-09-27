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
package call

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/heoruwulf/teleportal/internal/rtp"
	"github.com/heoruwulf/teleportal/internal/rtp/rtpdefs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// CallConfig defines parameters for initializing a call session (SIP or WebRTC).
type CallConfig struct {
	RTPStream      net.PacketConn
	RemoteRTPAddr  net.Addr
	OnConnected    func()
	OnFinished     func()
	Headers        map[string]string
	Dialog         *sipgo.DialogServerSession
	OnDisconnected func()
	AudioInput     chan rtpdefs.RTPPacket
	WsCodec        string
	RecordingPath  string
	CallID         string
	StreamInfo     audio.Stream
	MinPacketCount int
	ID             uuid.UUID
}

// ActiveCall holds all state for a single ongoing call (SIP or WebRTC).
// Note: Fields are strictly ordered by size (largest to smallest) and pointer status
// to minimize memory padding and reduce GC scan overhead.
type ActiveCall struct {
	LiveAt           time.Time
	JitterBuffer     rtpdefs.JitterBuffer
	ctx              context.Context
	metrics          metrics.Provider
	RTPStream        net.PacketConn
	RemoteRTPAddr    net.Addr
	Headers          map[string]string
	g                *errgroup.Group
	Dialog           *sipgo.DialogServerSession
	cancel           context.CancelFunc
	dtmfSource       chan rtp.DTMFRequest
	OnDisconnected   func()
	OnConnected      func()
	OnFinished       func()
	log              *zap.Logger
	AudioBridge      *AudioBridge
	CallID           string
	NegotiatedStream audio.Stream
	startOnce        sync.Once
	closeOnce        sync.Once
	mu               sync.Mutex
	ID               uuid.UUID
	callEnded        bool
}

// NewActiveCall creates and initializes a unified ActiveCall session for SIP or WebRTC.
func NewActiveCall(log *zap.Logger, m metrics.Provider, cfg CallConfig) *ActiveCall {
	ctx, cancel := context.WithCancel(context.Background())
	id := cfg.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	callLog := log.With(zap.String("internal_id", id.String()), zap.String("call_id", cfg.CallID))

	var jitterBuffer rtpdefs.JitterBuffer
	var audioBridgeInput <-chan rtpdefs.RTPPacket

	if cfg.AudioInput != nil {
		// WebRTC or pre-provided input channel
		audioBridgeInput = cfg.AudioInput
	} else {
		// SIP standard jitter buffer
		jb := audio.NewPionJitterBuffer(
			ctx,
			callLog,
			m,
			cfg.CallID,
			cfg.StreamInfo.PTime,
			cfg.StreamInfo.Codec.SampleRate,
			cfg.StreamInfo.Codec.Name,
			cfg.MinPacketCount,
		)
		jitterBuffer = jb
		audioBridgeInput = jb.Pop()
	}

	bridge := NewAudioBridge(
		ctx,
		callLog.Named("audio_bridge"),
		m,
		audioBridgeInput,
		cfg.CallID,
		cfg.StreamInfo,
		cfg.RecordingPath,
		cfg.WsCodec,
	)
	bridge.Start()

	headers := cfg.Headers
	if headers == nil {
		headers = make(map[string]string)
	}

	call := &ActiveCall{
		ID:               id,
		CallID:           cfg.CallID,
		Headers:          headers,
		Dialog:           cfg.Dialog,
		RTPStream:        cfg.RTPStream,
		RemoteRTPAddr:    cfg.RemoteRTPAddr,
		JitterBuffer:     jitterBuffer,
		AudioBridge:      bridge,
		metrics:          m,
		log:              callLog,
		ctx:              ctx,
		cancel:           cancel,
		NegotiatedStream: cfg.StreamInfo,
		dtmfSource:       make(chan rtp.DTMFRequest, 10),
		OnConnected:      cfg.OnConnected,
		OnDisconnected:   cfg.OnDisconnected,
		OnFinished:       cfg.OnFinished,
	}

	bridge.SetOnDTMF(func(digit string, duration int) {
		select {
		case call.dtmfSource <- rtp.DTMFRequest{Digit: digit, Duration: duration}:
		case <-call.ctx.Done():
		default:
			callLog.Warn("DTMF request dropped, source channel full")
		}
	})

	bridge.SetOnBye(func() {
		callLog.Info("Client requested BYE via WebSocket")
		if cfg.Dialog != nil {
			go func() {
				if err := cfg.Dialog.Bye(context.Background()); err != nil {
					callLog.Warn("Failed to send SIP BYE", zap.Error(err))
				}
			}()
		}
		call.EndCall()
	})

	return call
}

// StartRTPHandlers begins the RTP reader and writer goroutines for the call.
// This is called upon receiving the ACK, confirming the call is established.
func (c *ActiveCall) StartRTPHandlers() {
	c.startOnce.Do(func() {
		c.mu.Lock()
		c.LiveAt = time.Now()
		onConnected := c.OnConnected
		c.mu.Unlock()

		remoteAddrStr := ""
		if c.RemoteRTPAddr != nil {
			remoteAddrStr = c.RemoteRTPAddr.String()
		}
		c.log.Info("Call confirmed (ACK received), starting RTP handlers", zap.String("remote_rtp", remoteAddrStr))

		if onConnected != nil {
			onConnected()
		}

		var gCtx context.Context
		c.g, gCtx = errgroup.WithContext(c.ctx)

		c.g.Go(func() error {
			rtp.StartReader(gCtx, c.log, c.RTPStream, c.JitterBuffer, c.NegotiatedStream, func() {
				c.log.Warn("Media timeout triggered EndCall")
				c.EndCall()
			}, func(digit string, duration uint16, end bool) {
				if end && c.AudioBridge != nil {
					c.AudioBridge.BroadcastDTMF(digit, int(duration))
				}
			})
			return nil
		})

		wsCodec := ""
		if c.AudioBridge != nil {
			wsCodec = c.AudioBridge.WsCodec()
		}

		// Setup Outbound Path: AudioBridge -> RTPWriter (Packetizer consolidated directly into StartWriter)
		audioChan := make(chan []byte, 100)
		if c.AudioBridge != nil {
			c.AudioBridge.SetAudioOutput(audioChan)
		}

		c.g.Go(func() error {
			rtp.StartWriter(gCtx, c.log, c.RTPStream, c.RemoteRTPAddr, c.NegotiatedStream, audioChan, c.dtmfSource, wsCodec)
			return nil
		})
	})
}

// EndCall signals that the SIP call has ended and media processing should stop.
// However, the agent processing might continue until completion.
func (c *ActiveCall) EndCall() {
	c.mu.Lock()
	if c.callEnded {
		c.mu.Unlock()
		return
	}
	c.callEnded = true
	onDisconnected := c.OnDisconnected
	c.mu.Unlock()

	c.log.Info("Ending SIP call/media processing")

	if onDisconnected != nil {
		onDisconnected()
	}

	if c.AudioBridge != nil {
		c.AudioBridge.BroadcastCallEnded()
	}

	go c.Shutdown()
}

// Shutdown terminates all background processes associated with the call immediately.
// It is called either when the agent is done (graceful) or on errors/force kill.
func (c *ActiveCall) Shutdown() {
	c.closeOnce.Do(func() {
		c.log.Info("Shutting down active call")
		if c.cancel != nil {
			c.cancel() // Kills JitterBuffer, AudioBridge, and any remaining loops
		}

		if c.g != nil {
			if err := c.g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Error("RTP handlers failed during shutdown", zap.Error(err))
			}
		}

		if c.AudioBridge != nil {
			if err := c.AudioBridge.Wait(); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Error("Audio bridge failed during shutdown", zap.Error(err))
			}
		}

		if c.JitterBuffer != nil {
			c.JitterBuffer.Wait()
		}

		c.mu.Lock()
		onFinished := c.OnFinished
		c.mu.Unlock()

		if onFinished != nil {
			onFinished()
		}
	})
}

// ExtractHeaders extracts important SIP headers and stores them.
func ExtractHeaders(req *sip.Request) map[string]string {
	headers := make(map[string]string)
	if req == nil {
		return headers
	}

	// Explicitly capture standard identity headers
	if h := req.CallID(); h != nil {
		headers["Call-ID"] = h.Value()
	}
	if h := req.From(); h != nil {
		headers["From"] = h.Value()
	}
	if h := req.To(); h != nil {
		headers["To"] = h.Value()
	}

	for _, h := range req.Headers() {
		name := h.Name()
		lowerName := strings.ToLower(name)

		// Capture X-Headers, User-Agent, and Content-Type
		if strings.HasPrefix(lowerName, "x-") || lowerName == "user-agent" || lowerName == "content-type" {
			headers[name] = h.Value()
		}
	}
	return headers
}
