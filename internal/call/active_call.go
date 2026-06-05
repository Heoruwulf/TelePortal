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
	"github.com/gorilla/websocket"
	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/heoruwulf/teleportal/internal/rtp"
	"github.com/heoruwulf/teleportal/internal/rtp/rtpdefs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// AudioBridgeInterface defines the interface for components that bridge audio between SIP and WebSockets.
type AudioBridgeInterface interface {
	Start()
	Wait() error
	BroadcastCallEnded()
	BroadcastDTMF(digit string, duration int)
	SetOnDTMF(handler func(digit string, duration int))
	SetOnBye(handler func())
	AddClient(conn *websocket.Conn)
	ReadPump(conn *websocket.Conn)
	RemoveClient(conn *websocket.Conn)
	SetAudioOutput(ch chan<- []byte)
	WsCodec() string
	CloseAll()
	TryLock() bool
	Unlock()
}

// AudioBridgeFactory is a function that creates a new AudioBridgeInterface.
type AudioBridgeFactory func(ctx context.Context, log *zap.Logger, audioInput <-chan rtpdefs.RTPPacket, callID string, stream audio.Stream) AudioBridgeInterface

// ActiveCall holds all state for a single ongoing call.
// Note: Fields are strictly ordered by size (largest to smallest) and pointer status
// to minimize memory padding and reduce GC scan overhead.
type ActiveCall struct {
	LiveAt           time.Time
	ctx              context.Context
	RTPStream        net.PacketConn
	RemoteRTPAddr    net.Addr
	AudioBridge      AudioBridgeInterface
	JitterBuffer     rtpdefs.JitterBuffer
	metrics          metrics.Provider
	Dialog           *sipgo.DialogServerSession
	cancel           context.CancelFunc
	Headers          map[string]string
	OnDisconnected   func()
	log              *zap.Logger
	g                *errgroup.Group
	dtmfSource       chan rtp.DTMFRequest
	OnConnected      func()
	OnFinished       func()
	CallID           string
	NegotiatedStream audio.Stream
	startOnce        sync.Once
	closeOnce        sync.Once
	mu               sync.Mutex
	ID               uuid.UUID
	callEnded        bool
}

// NewActiveCall creates a new state object for an incoming call.
func NewActiveCall(
	log *zap.Logger,
	dialog *sipgo.DialogServerSession,
	rtpStream net.PacketConn,
	req *sip.Request,
	streamInfo audio.Stream,
	minPacketCount int,
	m metrics.Provider,
	createAudioBridge AudioBridgeFactory,
) *ActiveCall {
	ctx, cancel := context.WithCancel(context.Background())
	id := uuid.New()
	callID := req.CallID().Value()
	callLog := log.With(zap.String("internal_id", id.String()), zap.String("sip_call_id", callID))

	// Select Jitter Buffer Strategy
	// We now use PionJitterBuffer for both, but we can still tune it based on codec if needed.
	jitterBuffer := audio.NewPionJitterBuffer(ctx, callLog, m, callID, streamInfo.PTime, streamInfo.Codec.SampleRate, streamInfo.Codec.Name, minPacketCount)

	audioBridge := createAudioBridge(ctx, callLog.Named("audio_bridge"), jitterBuffer.Pop(), callID, streamInfo)
	audioBridge.Start()

	call := &ActiveCall{
		ID:               id,
		CallID:           callID,
		Headers:          extractHeaders(req),
		Dialog:           dialog,
		RTPStream:        rtpStream,
		JitterBuffer:     jitterBuffer,
		AudioBridge:      audioBridge,
		metrics:          m,
		log:              callLog,
		ctx:              ctx,
		cancel:           cancel,
		NegotiatedStream: streamInfo,
		dtmfSource:       make(chan rtp.DTMFRequest, 10),
	}

	// Wire outbound DTMF (WS -> SIP)
	audioBridge.SetOnDTMF(func(digit string, duration int) {
		select {
		case call.dtmfSource <- rtp.DTMFRequest{Digit: digit, Duration: duration}:
		case <-call.ctx.Done():
		default:
			callLog.Warn("DTMF request dropped, source channel full")
		}
	})

	// Wire outbound BYE (WS -> SIP)
	audioBridge.SetOnBye(func() {
		callLog.Info("Client requested BYE via WebSocket")
		// Send SIP BYE if dialog is established
		if dialog != nil {
			go func() {
				if err := dialog.Bye(context.Background()); err != nil {
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

		c.log.Info("Call confirmed (ACK received), starting RTP handlers", zap.String("remote_rtp", c.RemoteRTPAddr.String()))

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
				if end {
					c.AudioBridge.BroadcastDTMF(digit, int(duration))
				}
			})
			return nil
		})

		wsCodec := c.AudioBridge.WsCodec()
		actualWsCodec := wsCodec
		if wsCodec == string(audio.CodecPass) {
			actualWsCodec = string(c.NegotiatedStream.Codec.Name)
		}

		bytesPerSample := 2 // Default for L16
		if actualWsCodec == string(audio.CodecPCMU) || actualWsCodec == string(audio.CodecPCMA) {
			bytesPerSample = 1
		}

		// Setup Inbound Path: AudioBridge -> Packetizer -> RTPWriter
		packetizer := audio.NewPacketizer(c.NegotiatedStream.PTime, c.NegotiatedStream.Codec.SampleRate, bytesPerSample, c.NegotiatedStream.Codec.Channels)
		c.AudioBridge.SetAudioOutput(packetizer.Input())

		c.g.Go(func() error {
			packetizer.Run(gCtx)
			return nil
		})

		c.g.Go(func() error {
			rtp.StartWriter(gCtx, c.log, c.RTPStream, c.RemoteRTPAddr, c.NegotiatedStream, packetizer.Output(), c.dtmfSource, wsCodec)
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

	// Notify Frontend that the call has ended (audio-wise)
	c.AudioBridge.BroadcastCallEnded()

	go c.Shutdown()
}

// Shutdown terminates all background processes associated with the call immediately.
// It is called either when the agent is done (graceful) or on errors/force kill.
func (c *ActiveCall) Shutdown() {
	c.closeOnce.Do(func() {
		c.log.Info("Shutting down active call")
		c.cancel() // Kills JitterBuffer, AudioBridge, and any remaining loops

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

// extractHeaders extracts important SIP headers and stores them.
func extractHeaders(req *sip.Request) map[string]string {
	headers := make(map[string]string)

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
