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
	"github.com/pion/webrtc/v4"
)

// NewMediaEngine creates a new WebRTC MediaEngine configured with Opus and L16.
func NewMediaEngine() (*webrtc.MediaEngine, error) {
	m := &webrtc.MediaEngine{}

	// Register Opus
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeOpus,
			ClockRate:    48000,
			Channels:     2,
			SDPFmtpLine:  "minptime=10;useinbandfec=1",
			RTCPFeedback: nil,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	// Register L16 Big Endian 16kHz
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  "audio/L16",
			ClockRate: 16000,
			Channels:  1,
		},
		PayloadType: 112,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	// Register L16 Big Endian 8kHz
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  "audio/L16",
			ClockRate: 8000,
			Channels:  1,
		},
		PayloadType: 11,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	return m, nil
}
