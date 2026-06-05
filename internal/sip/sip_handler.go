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
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/heoruwulf/teleportal/internal/audio"
	"github.com/heoruwulf/teleportal/internal/call"
	"github.com/heoruwulf/teleportal/internal/platform/cache"
	"github.com/heoruwulf/teleportal/internal/platform/config"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/heoruwulf/teleportal/internal/rtp"
	"github.com/heoruwulf/teleportal/internal/sdp"
	"github.com/heoruwulf/teleportal/pkg/api"
	pionsip "github.com/pion/sdp/v3"
	"go.uber.org/zap"
)

// SIPHandler processes incoming SIP requests.
type SIPHandler struct {
	cache              cache.Cache
	metrics            metrics.Provider
	log                *zap.Logger
	dialogUA           *sipgo.DialogUA
	callManager        *call.CallManager
	rtpManager         *rtp.RTPManager
	config             *config.CoreConfig
	isReady            *atomic.Bool
	audioBridgeFactory call.AudioBridgeFactory
	instanceURL        string
	rtpBindIP          net.IP
	rtpExternalIP      net.IP
}

// NewSIPHandler creates a new SIP request handler.
func NewSIPHandler(
	log *zap.Logger,
	dialogUA *sipgo.DialogUA,
	cm *call.CallManager,
	rm *rtp.RTPManager,
	ca cache.Cache,
	m metrics.Provider,
	instanceURL string,
	rtpBindIP net.IP,
	rtpExternalIP net.IP,
	cfg *config.CoreConfig,
	isReady *atomic.Bool,
	audioBridgeFactory call.AudioBridgeFactory,
) *SIPHandler {
	return &SIPHandler{
		log:                log.Named("handler"),
		dialogUA:           dialogUA,
		callManager:        cm,
		rtpManager:         rm,
		cache:              ca,
		metrics:            m,
		instanceURL:        instanceURL,
		rtpBindIP:          rtpBindIP,
		rtpExternalIP:      rtpExternalIP,
		config:             cfg,
		isReady:            isReady,
		audioBridgeFactory: audioBridgeFactory,
	}
}

// RegisterHandlers connects the handler methods to the sipgo server instance.
func (h *SIPHandler) RegisterHandlers(srv *sipgo.Server) {
	srv.OnInvite(h.handleInvite)
	srv.OnAck(h.handleAck)
	srv.OnBye(h.handleBye)
	srv.OnCancel(h.handleCancel)
	srv.OnOptions(h.handleOptions)
}

func (h *SIPHandler) handleInvite(req *sip.Request, tx sip.ServerTransaction) {
	callID := req.CallID().Value()
	h.log.Info(
		"INVITE received",
		zap.String("sip_call_id", callID),
		zap.String("from", req.From().Address.User),
		zap.String("to", req.To().Address.User),
	)

	// --- READINESS CHECK ---
	// This is the gate for all incoming calls.
	if !h.isReady.Load() {
		h.log.Warn("INVITE rejected because service is not ready")
		h.metrics.IncSIPRequests("INVITE", "503")
		// 503 is the correct code to indicate the server is temporarily unable to handle the request.
		_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
		return
	}

	// --- CAPACITY CHECK ---
	if h.config.SIP.MaxCalls > 0 {
		stats := h.callManager.Stats()
		if stats.ActiveCalls >= int64(h.config.SIP.MaxCalls) {
			h.log.Warn("INVITE rejected because maximum call capacity reached",
				zap.Int("max_calls", h.config.SIP.MaxCalls),
				zap.Int64("current_calls", stats.ActiveCalls))
			h.metrics.IncSIPRequests("INVITE", "486")
			// 486 Busy Here is the standard response when the server refuses a call due to capacity limits.
			_ = tx.Respond(sip.NewResponseFromRequest(req, 486, "Busy Here", nil))
			return
		}
	}

	if _, ok := h.callManager.Get(callID); ok {
		h.log.Warn("Retransmitted INVITE received for an existing call, ignoring", zap.String("sip_call_id", callID))
		// The transaction layer will handle re-sending the 200 OK. We don't need to do anything else.
		return
	}

	var incomingSDP pionsip.SessionDescription
	if err := incomingSDP.Unmarshal(req.Body()); err != nil {
		h.log.Error("Failed to parse incoming SDP", zap.Error(err))
		h.metrics.IncSIPRequests("INVITE", "400")
		_ = tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad Request - Invalid SDP", nil))
		return
	}

	// Negotiate Codec & Stream Info (ptime)
	selectedStream, err := h.negotiateStream(incomingSDP)
	if err != nil {
		h.log.Error("Codec negotiation failed", zap.Error(err))
		h.metrics.IncSIPRequests("INVITE", "488")
		_ = tx.Respond(sip.NewResponseFromRequest(req, 488, "Not Acceptable Here", nil))
		return
	}
	h.log.Info("Stream negotiated",
		zap.String("name", string(selectedStream.Codec.Name)),
		zap.Uint8("pt", selectedStream.Codec.PayloadType),
		zap.Int("ptime", selectedStream.PTime),
		zap.Int("rate", selectedStream.Codec.SampleRate),
		zap.Int("channels", selectedStream.Codec.Channels),
		zap.Bool("is_big_endian", selectedStream.Codec.IsBigEndian),
	)

	remoteRTPAddr, err := getRemoteRTPAddr(incomingSDP)
	if err != nil {
		h.log.Error("Could not determine remote RTP address from SDP", zap.Error(err))
		h.metrics.IncSIPRequests("INVITE", "400")
		_ = tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad Request - Missing Media Info", nil))
		return
	}

	// Create a dialog session
	dialog, err := h.dialogUA.ReadInvite(req, tx)
	if err != nil {
		h.log.Error("Failed to create dialog session", zap.Error(err))
		h.metrics.IncSIPRequests("INVITE", "500")
		_ = tx.Respond(sip.NewResponseFromRequest(req, 500, "Internal Server Error", nil))
		return
	}

	// Create a symmetric RTP listener
	rtpStream, err := h.rtpManager.CreateListener(h.rtpBindIP)
	if err != nil {
		h.log.Error("Failed to create RTP listener", zap.Error(err))
		h.metrics.IncSIPRequests("INVITE", "503")
		_ = dialog.WriteResponse(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
		return
	}

	// Get the port from our new listener
	rtpAddr, ok := rtpStream.LocalAddr().(*net.UDPAddr)
	if !ok {
		h.log.Error("RTP listener is not a UDP connection")
		h.rtpManager.ReleaseListener(rtpStream)
		h.metrics.IncSIPRequests("INVITE", "500")
		_ = dialog.WriteResponse(sip.NewResponseFromRequest(req, 500, "Internal Server Error", nil))
		return
	}

	// Generate SDP answer
	answer, err := sdp.GenerateAnswer(h.rtpExternalIP, rtpAddr.Port, selectedStream)
	if err != nil {
		h.log.Error("Failed to generate SDP answer", zap.Error(err))
		h.rtpManager.ReleaseListener(rtpStream)
		h.metrics.IncSIPRequests("INVITE", "500")
		_ = dialog.WriteResponse(sip.NewResponseFromRequest(req, 500, "Internal Server Error", nil))
		return
	}

	// Create and store the active call state
	activeCall := call.NewActiveCall(
		h.log,
		dialog,
		rtpStream,
		req,
		selectedStream,
		h.config.Audio.JitterBufferMinPacketCount,
		h.metrics,
		h.audioBridgeFactory,
	)
	activeCall.RemoteRTPAddr = remoteRTPAddr

	// Set cleanup callback to ensure resources are released when the call is fully done
	activeCall.OnFinished = func() {
		h.rtpManager.ReleaseListener(activeCall.RTPStream)
		h.callManager.Remove(callID)
	}

	// Set Redis notification hooks
	wsURL := fmt.Sprintf("%s/v1/listen/%s/%s", h.instanceURL, activeCall.ID.String(), callID)
	// Ensure we have a ws:// or wss:// prefix
	if !strings.HasPrefix(wsURL, "ws://") && !strings.HasPrefix(wsURL, "wss://") {
		if strings.HasPrefix(wsURL, "https://") {
			wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
		} else if strings.HasPrefix(wsURL, "http://") {
			wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
		} else {
			wsURL = "ws://" + wsURL
		}
	}

	activeCall.OnConnected = func() {
		if h.cache == nil {
			return
		}
		event := api.CallEvent{
			Event:        "connected",
			CallID:       activeCall.CallID,
			InternalID:   activeCall.ID.String(),
			WebSocketURL: wsURL,
			Timestamp:    time.Now(),
			Metadata:     activeCall.Headers,
		}
		data, _ := json.Marshal(event)
		if err := h.cache.Publish(context.Background(), api.RedisChannelCallEvents, data); err != nil {
			h.log.Error("Failed to publish connected event", zap.Error(err))
		}
	}

	activeCall.OnDisconnected = func() {
		if h.cache == nil {
			return
		}
		event := api.CallEvent{
			Event:        "disconnected",
			CallID:       activeCall.CallID,
			InternalID:   activeCall.ID.String(),
			WebSocketURL: wsURL,
			Timestamp:    time.Now(),
			Metadata:     activeCall.Headers,
		}
		data, _ := json.Marshal(event)
		if err := h.cache.Publish(context.Background(), api.RedisChannelCallEvents, data); err != nil {
			h.log.Error("Failed to publish disconnected event", zap.Error(err))
		}
	}

	h.log.Info(
		"Call setup complete, sending 200 OK",
		zap.String("remote_rtp", remoteRTPAddr.String()),
		zap.String("local_rtp", rtpAddr.String()),
	)
	h.callManager.Add(activeCall)

	// Send 200 OK with SDP
	if err := dialog.Respond(200, "OK", answer, sip.NewHeader("Content-Type", "application/sdp")); err != nil {
		h.log.Error("Failed to send 200 OK response to INVITE", zap.Error(err))
		h.metrics.IncSIPRequests("INVITE", "500")
		activeCall.Shutdown()
	} else {
		h.metrics.IncSIPRequests("INVITE", "200")
	}
}

// negotiateStream finds the best matching codec and ptime from the offer.
func (h *SIPHandler) negotiateStream(offer pionsip.SessionDescription) (audio.Stream, error) {
	var audioMedia *pionsip.MediaDescription
	for _, m := range offer.MediaDescriptions {
		if m.MediaName.Media == "audio" {
			audioMedia = m
			break
		}
	}
	if audioMedia == nil {
		return audio.Stream{}, fmt.Errorf("no audio media found in SDP")
	}

	// Extract ptime from attributes, default to 20ms
	ptime := 20
	for _, attr := range audioMedia.Attributes {
		if attr.Key == "ptime" {
			if val, err := strconv.Atoi(attr.Value); err == nil && val > 0 {
				ptime = val
			}
			break
		}
	}

	// Helper to find dynamic payload type for a codec name
	// Returns: payloadType, sampleRate, channels, found
	getDynamicPT := func(encodingName string) (uint8, int, int, bool) {
		for _, attr := range audioMedia.Attributes {
			if attr.Key == "rtpmap" {
				parts := strings.Split(attr.Value, " ")
				if len(parts) >= 2 {
					pt, err := strconv.Atoi(parts[0])
					if err != nil {
						continue
					}
					rest := parts[1] // "L16/8000/1" or "opus/48000/2"
					slashParts := strings.Split(rest, "/")
					if len(slashParts) >= 1 && strings.EqualFold(slashParts[0], encodingName) {
						rate := 8000
						channels := 1
						if len(slashParts) >= 2 {
							if r, err := strconv.Atoi(slashParts[1]); err == nil {
								rate = r
							}
						}
						if len(slashParts) >= 3 {
							if c, err := strconv.Atoi(slashParts[2]); err == nil {
								channels = c
							}
						}
						return uint8(pt), rate, channels, true
					}
				}
			}
		}
		return 0, 8000, 1, false
	}

	var codec audio.Codec
	var err error
	found := false

	wsCodec := h.config.Audio.WebSocketCodec

	if wsCodec == string(audio.CodecPass) {
		// Preference-based negotiation for pass-through: L16 -> PCMU -> PCMA
		// 1. Check for L16 (Dynamic PT)
		if pt, rate, channels, ok := getDynamicPT(string(audio.CodecL16)); ok {
			for _, f := range audioMedia.MediaName.Formats {
				if f == strconv.Itoa(int(pt)) {
					codec, err = audio.NewCodecL16(pt, rate, channels, true, true)
					if err == nil {
						found = true
						break
					}
				}
			}
		}

		// 2. Check for PCMU (Static PT 0)
		if !found {
			for _, f := range audioMedia.MediaName.Formats {
				if f == strconv.Itoa(int(audio.PayloadTypePCMU)) {
					codec, _ = audio.NewCodecG711MuLaw()
					found = true
					break
				}
			}
		}

		// 3. Check for PCMA (Static PT 8)
		if !found {
			for _, f := range audioMedia.MediaName.Formats {
				if f == strconv.Itoa(int(audio.PayloadTypePCMA)) {
					codec, _ = audio.NewCodecG711ALaw()
					found = true
					break
				}
			}
		}
	} else {
		// Strict negotiation for a specific codec
		switch wsCodec {
		case string(audio.CodecL16):
			// 1. Try L16
			if pt, rate, channels, ok := getDynamicPT(string(audio.CodecL16)); ok {
				for _, f := range audioMedia.MediaName.Formats {
					if f == strconv.Itoa(int(pt)) {
						codec, err = audio.NewCodecL16(pt, rate, channels, true, true)
						if err == nil {
							found = true
						}
						break
					}
				}
			}
			// 2. Try PCMU (transcode target)
			if !found {
				for _, f := range audioMedia.MediaName.Formats {
					if f == strconv.Itoa(int(audio.PayloadTypePCMU)) {
						codec, _ = audio.NewCodecG711MuLaw()
						found = true
						break
					}
				}
			}
			// 3. Try PCMA (transcode target)
			if !found {
				for _, f := range audioMedia.MediaName.Formats {
					if f == strconv.Itoa(int(audio.PayloadTypePCMA)) {
						codec, _ = audio.NewCodecG711ALaw()
						found = true
						break
					}
				}
			}
		case string(audio.CodecPCMU):
			for _, f := range audioMedia.MediaName.Formats {
				if f == strconv.Itoa(int(audio.PayloadTypePCMU)) {
					codec, _ = audio.NewCodecG711MuLaw()
					found = true
					break
				}
			}
		case string(audio.CodecPCMA):
			for _, f := range audioMedia.MediaName.Formats {
				if f == strconv.Itoa(int(audio.PayloadTypePCMA)) {
					codec, _ = audio.NewCodecG711ALaw()
					found = true
					break
				}
			}
		}
	}

	if !found {
		if wsCodec == string(audio.CodecPass) {
			return audio.Stream{}, fmt.Errorf("no supported codecs found for pass-through (%s, %s, %s)", audio.CodecL16, audio.CodecPCMU, audio.CodecPCMA)
		}
		return audio.Stream{}, fmt.Errorf("configured WebSocket codec %s not found in offer", wsCodec)
	} else if err != nil {
		return audio.Stream{}, fmt.Errorf("failed to create codec: %w", err)
	}

	// Negotiate DTMF (telephone-event)
	var dtmfPT uint8
	if pt, _, _, ok := getDynamicPT("telephone-event"); ok {
		// Verify it's also in the formats list
		for _, f := range audioMedia.MediaName.Formats {
			if f == strconv.Itoa(int(pt)) {
				dtmfPT = pt
				break
			}
		}
	}

	return audio.Stream{
		Codec:           codec,
		PTime:           ptime,
		DTMFPayloadType: dtmfPT,
	}, nil
}

func (h *SIPHandler) handleAck(req *sip.Request, tx sip.ServerTransaction) {
	callID := req.CallID().Value()
	h.log.Info("ACK received", zap.String("sip_call_id", callID))
	h.metrics.IncSIPRequests("ACK", "200")

	activeCall, ok := h.callManager.Get(callID)
	if !ok {
		h.log.Warn("Received ACK for non-existent call", zap.String("sip_call_id", callID))
		return
	}

	if err := activeCall.Dialog.ReadAck(req, tx); err != nil {
		h.log.Error("Error processing ACK in dialog session", zap.Error(err))
		// If this fails, the dialog state might be inconsistent, but we can still try to start media.
	}

	activeCall.StartRTPHandlers()
}

func (h *SIPHandler) handleBye(req *sip.Request, tx sip.ServerTransaction) {
	callID := req.CallID().Value()
	h.log.Info("BYE received", zap.String("sip_call_id", callID))
	_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	h.metrics.IncSIPRequests("BYE", "200")

	activeCall, ok := h.callManager.Get(callID)
	if !ok {
		h.log.Warn("Received BYE for non-existent call", zap.String("sip_call_id", callID))
		return
	}

	// End the call gracefuly (stops media/STT, notifies frontend, waits for Agent)
	activeCall.EndCall()
}

func (h *SIPHandler) handleCancel(req *sip.Request, tx sip.ServerTransaction) {
	callID := req.CallID().Value()
	h.log.Info("CANCEL received", zap.String("sip_call_id", callID))
	// sipgo handles responding to the original INVITE with 487 automatically.
	// We just need to respond 200 OK to the CANCEL itself.
	_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	h.metrics.IncSIPRequests("CANCEL", "200")

	activeCall, ok := h.callManager.Get(callID)
	if !ok {
		h.log.Warn("Received CANCEL for non-existent call", zap.String("sip_call_id", callID))
		return
	}

	activeCall.EndCall()
}

func (h *SIPHandler) handleOptions(req *sip.Request, tx sip.ServerTransaction) {
	callID := req.CallID().Value()
	h.log.Info("OPTIONS received", zap.String("sip_call_id", callID))

	// --- READINESS CHECK ---
	if !h.isReady.Load() {
		h.log.Warn("OPTIONS rejected because service is not ready")
		h.metrics.IncSIPRequests("OPTIONS", "503")
		_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
		return
	}

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	res.AppendHeader(sip.NewHeader("Allow", "INVITE, ACK, BYE, CANCEL, OPTIONS"))
	res.AppendHeader(sip.NewHeader("Supported", "replaces, timer"))

	if err := tx.Respond(res); err != nil {
		h.log.Error("Failed to respond to OPTIONS", zap.Error(err))
		h.metrics.IncSIPRequests("OPTIONS", "500")
	} else {
		h.metrics.IncSIPRequests("OPTIONS", "200")
	}
}

// getRemoteRTPAddr parses an SDP session description to find the address for the first audio media stream.
func getRemoteRTPAddr(session pionsip.SessionDescription) (net.Addr, error) {
	var remoteIP string
	var remotePort int

	// Connection info can be at the session level or media level.
	// Media level overrides session level.
	if session.ConnectionInformation != nil {
		remoteIP = session.ConnectionInformation.Address.Address
	}

	for _, media := range session.MediaDescriptions {
		if media.MediaName.Media == "audio" {
			if media.ConnectionInformation != nil {
				remoteIP = media.ConnectionInformation.Address.Address
			}
			remotePort = media.MediaName.Port.Value

			if remoteIP != "" && remotePort > 0 {
				return net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", remoteIP, remotePort))
			}
		}
	}

	return nil, fmt.Errorf("could not find valid audio media IP and Port in SDP")
}
