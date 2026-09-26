package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/heoruwulf/teleportal/internal/audio"
	internalwebrtc "github.com/heoruwulf/teleportal/internal/webrtc"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type WebRTCDriver struct {
	pc          *webrtc.PeerConnection
	audioTrack  *webrtc.TrackLocalStaticRTP
	wsConn      *websocket.Conn
	wsURL       string
	callID      string
	payload     []byte
	sampleRate  int
	payloadType uint8
}

func NewWebRTCDriver(wsURL string, payloadType uint8, sampleRate int, payload []byte) *WebRTCDriver {
	return &WebRTCDriver{
		wsURL:       wsURL,
		callID:      uuid.New().String(),
		payloadType: payloadType,
		payload:     payload,
		sampleRate:  sampleRate,
	}
}

func (d *WebRTCDriver) CallID() string {
	return d.callID
}

func (d *WebRTCDriver) Dial(ctx context.Context) error {
	// 1. Create PeerConnection using custom MediaEngine to support L16
	me, err := internalwebrtc.NewMediaEngine()
	if err != nil {
		return fmt.Errorf("failed to create media engine: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))

	config := webrtc.Configuration{}
	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	d.pc = pc

	// 2. Create audio track
	// For loadtest, we will just use Opus (111) or L16
	mimeType := webrtc.MimeTypeOpus
	if d.payloadType == 112 || d.payloadType == 11 || d.payloadType == audio.PayloadTypeL16 { // L16
		mimeType = "audio/L16"
	}

	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: mimeType, ClockRate: uint32(d.sampleRate)}, "audio", "pion")
	if err != nil {
		return fmt.Errorf("failed to create track: %w", err)
	}
	d.audioTrack = track

	if _, err = pc.AddTrack(track); err != nil {
		return fmt.Errorf("failed to add track: %w", err)
	}

	// 3. Create Offer
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("failed to create offer: %w", err)
	}

	if err = pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete before sending the offer
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	// 4. Send this offer over WebSocket to the server
	// and wait for the Answer.
	ws, _, err := websocket.DefaultDialer.Dial(d.wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to dial websocket: %w", err)
	}
	d.wsConn = ws

	if err := ws.WriteJSON(offer); err != nil {
		return fmt.Errorf("failed to write offer: %w", err)
	}

	var answer webrtc.SessionDescription
	if err := ws.ReadJSON(&answer); err != nil {
		return fmt.Errorf("failed to read answer: %w", err)
	}

	if err := pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	return nil
}

func (d *WebRTCDriver) Start(ctx context.Context) error {
	frameSize := d.sampleRate / 50 // 20ms
	bytesPerSample := 1
	if d.payloadType == 112 || d.payloadType == 11 { // L16
		bytesPerSample = 2
	}
	chunkSize := frameSize * bytesPerSample

	if chunkSize == 0 || len(d.payload) == 0 {
		return fmt.Errorf("invalid payload or chunk size")
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	pos := 0
	sequenceNumber := uint16(rand.Uint32())
	timestamp := rand.Uint32()
	ssrc := rand.Uint32()
	samplesPerPacket := uint32(d.sampleRate / 50)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			end := pos + chunkSize
			if end > len(d.payload) {
				// Loop back to start
				pos = 0
				end = min(
					// If payload is smaller than one chunk, this will panic. Safe to assume payload is larger for load testing.
					chunkSize, len(d.payload))
			}

			chunk := d.payload[pos:end]
			pos = end

			pt := uint8(111)
			if d.payloadType == 112 || d.payloadType == 11 || d.payloadType == 96 {
				if d.sampleRate == 16000 {
					pt = 112
				} else {
					pt = 11
				}
			}

			packet := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    pt,
					SequenceNumber: sequenceNumber,
					Timestamp:      timestamp,
					SSRC:           ssrc,
				},
				Payload: chunk,
			}

			if err := d.audioTrack.WriteRTP(packet); err != nil {
				return fmt.Errorf("writing RTP packet: %w", err)
			}
			sequenceNumber++
			timestamp += samplesPerPacket
		}
	}
}

func (d *WebRTCDriver) Hangup(ctx context.Context) error {
	if d.wsConn != nil {
		d.wsConn.Close()
	}
	if d.pc != nil {
		return d.pc.Close()
	}
	return nil
}
