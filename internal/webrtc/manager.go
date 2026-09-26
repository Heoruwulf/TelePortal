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
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/call"
	"github.com/heoruwulf/teleportal/internal/rtp/rtpdefs"
	pkgapi "github.com/heoruwulf/teleportal/pkg/api"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// Publisher interface to abstract Redis publishing
type Publisher interface {
	Publish(ctx context.Context, channel string, message any) error
}

// CallManager handles the lifecycle of WebRTC calls and publishes events to Redis.
type CallManager struct {
	log       *zap.Logger
	publisher Publisher
	cm        *call.CallManager
	factory   call.AudioBridgeFactory
	api       *webrtc.API
}

// NewCallManager creates a new WebRTC CallManager.
func NewCallManager(log *zap.Logger, p Publisher, cm *call.CallManager, factory call.AudioBridgeFactory) *CallManager {
	me, err := NewMediaEngine()
	if err != nil {
		log.Fatal("Failed to create WebRTC MediaEngine", zap.Error(err))
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))

	return &CallManager{
		log:       log.Named("webrtc_manager"),
		publisher: p,
		cm:        cm,
		factory:   factory,
		api:       api,
	}
}

// HandleInboundCall simulates the setup of an inbound WebRTC call
// and publishes an event to the WebRTC Redis channel.
func (m *CallManager) HandleInboundCall(ctx context.Context, callID, wsBaseURL string) (string, error) {
	m.log.Info("Handling inbound WebRTC call", zap.String("call_id", callID))

	internalID := uuid.New().String()
	wsURL := fmt.Sprintf("%s/v1/listen/%s/%s", wsBaseURL, internalID, callID)

	event := pkgapi.CallEvent{
		Timestamp:    time.Now(),
		Event:        "webrtc_connected",
		CallID:       callID,
		InternalID:   internalID,
		WebSocketURL: wsURL,
	}

	data, err := json.Marshal(event)
	if err != nil {
		return "", err
	}

	if m.publisher != nil {
		if err := m.publisher.Publish(ctx, pkgapi.RedisChannelWebRTCCallEvents, data); err != nil {
			m.log.Error("Failed to publish WebRTC call event", zap.Error(err))
			return "", err
		}
	}

	return internalID, nil
}

// ProcessSignaling handles an incoming SDP offer, establishes the PeerConnection,
// creates the internal AudioBridge, and returns the SDP answer.
func (m *CallManager) ProcessSignaling(ctx context.Context, callID, wsBaseURL string, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	internalID, err := m.HandleInboundCall(ctx, callID, wsBaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to handle inbound call: %w", err)
	}

	uid, err := uuid.Parse(internalID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse UUID: %w", err)
	}

	sampleRate := 48000
	mimeType := webrtc.MimeTypeOpus
	if strings.Contains(offer.SDP, "L16/16000") {
		sampleRate = 16000
		mimeType = "audio/L16"
	} else if strings.Contains(offer.SDP, "L16/8000") {
		sampleRate = 8000
		mimeType = "audio/L16"
	}

	codecName := audio.CodecOpus
	if mimeType == "audio/L16" {
		codecName = audio.CodecL16
	}

	streamInfo := audio.Stream{
		Codec: audio.Codec{
			Name:       codecName,
			SampleRate: sampleRate,
		},
	}

	audioInput := make(chan rtpdefs.RTPPacket, 50)
	bridge := m.factory(ctx, m.log, audioInput, callID, streamInfo)
	bridge.Start()

	activeCall := &call.ActiveCall{
		ID:          uid,
		CallID:      callID,
		AudioBridge: bridge,
	}
	m.cm.Add(activeCall)

	pc, err := m.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, fmt.Errorf("creating peer connection: %w", err)
	}

	// Create local track for outbound audio
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: mimeType, ClockRate: uint32(sampleRate)}, "audio", "pion")
	if err != nil {
		return nil, fmt.Errorf("creating local track: %w", err)
	}
	if _, err := pc.AddTrack(trackLocal); err != nil {
		return nil, fmt.Errorf("adding track: %w", err)
	}

	payloadType := uint8(111) // Default Opus
	if mimeType == "audio/L16" {
		if sampleRate == 16000 {
			payloadType = 112
		} else {
			payloadType = 11
		}
	}

	outboundAudioChan := make(chan []byte, 50)
	bridge.SetAudioOutput(outboundAudioChan)
	m.HandleOutboundAudio(ctx, trackLocal, outboundAudioChan, payloadType)

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		m.HandleWebRTCTrack(ctx, track, audioInput)
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		return nil, fmt.Errorf("setting remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return nil, fmt.Errorf("creating answer: %w", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return nil, fmt.Errorf("setting local description: %w", err)
	}
	<-gatherComplete

	return pc.LocalDescription(), nil
}

// HandleWebRTCTrack bridges a pion.TrackRemote into the TelePortal AudioBridge
func (m *CallManager) HandleWebRTCTrack(ctx context.Context, track *webrtc.TrackRemote, audioInput chan<- rtpdefs.RTPPacket) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				rtpPacket, _, err := track.ReadRTP()
				if err != nil {
					m.log.Error("Failed to read RTP from WebRTC track", zap.Error(err))
					return
				}

				select {
				case audioInput <- rtpdefs.RTPPacket{
					Raw:       rtpPacket,
					Payload:   rtpPacket.Payload,
					Sequence:  rtpPacket.SequenceNumber,
					Timestamp: rtpPacket.Timestamp,
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

// HandleOutboundAudio bridges the AudioBridge output to a WebRTC TrackLocal
func (m *CallManager) HandleOutboundAudio(ctx context.Context, trackLocal *webrtc.TrackLocalStaticRTP, outboundAudioChan <-chan []byte, payloadType uint8) {
	go func() {
		// Initialize with random values per RFC 3550
		sequenceNumber := uint16(rand.Uint32())
		timestamp := rand.Uint32()
		ssrc := rand.Uint32()

		codec := trackLocal.Codec()
		samplesPerPacket := uint32(codec.ClockRate / 50)

		for {
			select {
			case <-ctx.Done():
				return
			case payload, ok := <-outboundAudioChan:
				if !ok {
					return
				}
				packet := &rtp.Packet{
					Header: rtp.Header{
						Version:        2,
						PayloadType:    payloadType,
						SequenceNumber: sequenceNumber,
						Timestamp:      timestamp,
						SSRC:           ssrc,
					},
					Payload: payload,
				}
				// Pass the raw RTP packet to the WebRTC peer
				if err := trackLocal.WriteRTP(packet); err != nil {
					m.log.Error("Failed to write RTP to WebRTC track", zap.Error(err))
					return
				}
				sequenceNumber++
				timestamp += samplesPerPacket
			}
		}
	}()
}
