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
package sdp

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/Heoruwulf/TelePortal/internal/audio"
	"github.com/pion/sdp/v3"
)

// GenerateAnswer generates an SDP answer for an incoming call.
// It prioritizes the negotiated codec provided in the arguments.
func GenerateAnswer(ip net.IP, port int, stream audio.Stream) ([]byte, error) {
	sessionID := uint64(time.Now().UnixNano())

	formats := []string{strconv.Itoa(int(stream.Codec.PayloadType))}
	if stream.DTMFPayloadType != 0 {
		formats = append(formats, strconv.Itoa(int(stream.DTMFPayloadType)))
	}

	// Construct attributes based on the negotiated stream settings.
	attributes := []sdp.Attribute{
		{Key: "ptime", Value: strconv.Itoa(stream.PTime)},
		{Key: "sendrecv"},
	}

	// Add rtpmap attribute.
	// Format: <payload type> <encoding name>/<clock rate>[/<encoding parameters>]
	rtpmapVal := fmt.Sprintf("%d %s/%d", stream.Codec.PayloadType, stream.Codec.Name, stream.Codec.SampleRate)
	if stream.Codec.Channels > 1 {
		rtpmapVal += fmt.Sprintf("/%d", stream.Codec.Channels)
	}
	attributes = append([]sdp.Attribute{{Key: "rtpmap", Value: rtpmapVal}}, attributes...)

	if stream.DTMFPayloadType != 0 {
		dtmfRtpmap := fmt.Sprintf("%d telephone-event/%d", stream.DTMFPayloadType, stream.Codec.SampleRate)
		attributes = append(attributes, sdp.Attribute{Key: "rtpmap", Value: dtmfRtpmap})
		attributes = append(attributes, sdp.Attribute{Key: "fmtp", Value: fmt.Sprintf("%d 0-16", stream.DTMFPayloadType)})
	}

	desc := sdp.SessionDescription{
		Version: sdp.Version(0),
		Origin: sdp.Origin{
			Username:       "-",
			SessionID:      sessionID,
			SessionVersion: sessionID,
			NetworkType:    "IN",
			AddressType:    "IP4",
			UnicastAddress: ip.String(),
		},
		SessionName: sdp.SessionName("TelePortal Session"),
		ConnectionInformation: &sdp.ConnectionInformation{
			NetworkType: "IN",
			AddressType: "IP4",
			Address:     &sdp.Address{Address: ip.String()},
		},
		TimeDescriptions: []sdp.TimeDescription{
			{
				Timing: sdp.Timing{
					StartTime: 0,
					StopTime:  0,
				},
			},
		},
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "audio",
					Port:    sdp.RangedPort{Value: port},
					Protos:  []string{"RTP", "AVP"},
					Formats: formats,
				},
				ConnectionInformation: &sdp.ConnectionInformation{
					NetworkType: "IN",
					AddressType: "IP4",
					Address:     &sdp.Address{Address: ip.String()},
				},
				Attributes: attributes,
			},
		},
	}

	return desc.Marshal()
}
