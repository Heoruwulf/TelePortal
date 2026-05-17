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
	"net"
	"strings"
	"testing"

	"github.com/Heoruwulf/TelePortal/internal/audio"
)

func TestGenerateAnswer(t *testing.T) {
	tests := []struct {
		name         string
		ip           net.IP
		wantContains []string
		stream       audio.Stream
		port         int
		wantErr      bool
	}{
		{
			name: "PCMU",
			ip:   net.ParseIP("127.0.0.1"),
			port: 10000,
			stream: audio.Stream{
				Codec: audio.Codec{
					Name:        audio.CodecPCMU,
					PayloadType: audio.PayloadTypePCMU,
					SampleRate:  8000,
					Channels:    1,
				},
				PTime: 20,
			},
			wantContains: []string{
				"m=audio 10000 RTP/AVP 0",
				"a=rtpmap:0 PCMU/8000",
				"c=IN IP4 127.0.0.1",
				"a=ptime:20",
			},
			wantErr: false,
		},
		{
			name: "PCMA",
			ip:   net.ParseIP("192.168.1.5"),
			port: 20000,
			stream: audio.Stream{
				Codec: audio.Codec{
					Name:        audio.CodecPCMA,
					PayloadType: audio.PayloadTypePCMA,
					SampleRate:  8000,
					Channels:    1,
				},
				PTime: 30,
			},
			wantContains: []string{
				"m=audio 20000 RTP/AVP 8",
				"a=rtpmap:8 PCMA/8000",
				"c=IN IP4 192.168.1.5",
				"a=ptime:30",
			},
			wantErr: false,
		},
		{
			name: "L16 8000Hz",
			ip:   net.ParseIP("10.0.0.1"),
			port: 30000,
			stream: audio.Stream{
				Codec: audio.Codec{
					Name:        audio.CodecL16,
					PayloadType: audio.PayloadTypeL16,
					SampleRate:  8000,
					Channels:    1,
				},
				PTime: 20,
			},
			wantContains: []string{
				"m=audio 30000 RTP/AVP 96",
				"a=rtpmap:96 L16/8000",
			},
			wantErr: false,
		},
		{
			name: "L16 16000Hz",
			ip:   net.ParseIP("10.0.0.1"),
			port: 30002,
			stream: audio.Stream{
				Codec: audio.Codec{
					Name:        audio.CodecL16,
					PayloadType: 97, // Custom PT
					SampleRate:  16000,
					Channels:    1,
				},
				PTime: 20,
			},
			wantContains: []string{
				"m=audio 30002 RTP/AVP 97",
				"a=rtpmap:97 L16/16000",
			},
			wantErr: false,
		},
		{
			name: "Opus 48000Hz Stereo",
			ip:   net.ParseIP("127.0.0.1"),
			port: 40000,
			stream: audio.Stream{
				Codec: audio.Codec{
					Name:        audio.CodecOpus,
					PayloadType: audio.PayloadTypeOpus,
					SampleRate:  48000,
					Channels:    2,
				},
				PTime: 20,
			},
			wantContains: []string{
				"m=audio 40000 RTP/AVP 111",
				// Note: Currently GenerateAnswer does not append channels for Opus.
				// If/When that is fixed, this expectation might need update.
				// Based on current implementation:
				"a=rtpmap:111 opus/48000",
			},
			wantErr: false,
		},
		{
			name: "PCMU with DTMF",
			ip:   net.ParseIP("127.0.0.1"),
			port: 10000,
			stream: audio.Stream{
				Codec: audio.Codec{
					Name:        audio.CodecPCMU,
					PayloadType: audio.PayloadTypePCMU,
					SampleRate:  8000,
					Channels:    1,
				},
				PTime:           20,
				DTMFPayloadType: 101,
			},
			wantContains: []string{
				"m=audio 10000 RTP/AVP 0 101",
				"a=rtpmap:101 telephone-event/8000",
				"a=fmtp:101 0-16",
			},
			wantErr: false,
		},
		{
			name: "L16 16000Hz with DTMF",
			ip:   net.ParseIP("10.0.0.1"),
			port: 30002,
			stream: audio.Stream{
				Codec: audio.Codec{
					Name:        audio.CodecL16,
					PayloadType: 97, // Custom PT
					SampleRate:  16000,
					Channels:    1,
				},
				PTime:           20,
				DTMFPayloadType: 101,
			},
			wantContains: []string{
				"m=audio 30002 RTP/AVP 97 101",
				"a=rtpmap:101 telephone-event/16000",
				"a=fmtp:101 0-16",
			},
			wantErr: false,
		},
		{
			name: "Invalid Format / Unknown Codec",
			ip:   net.ParseIP("127.0.0.1"),
			port: 50000,
			stream: audio.Stream{
				Codec: audio.Codec{
					Name:        "UNKNOWN",
					PayloadType: 127,
					SampleRate:  12345,
					Channels:    1,
				},
				PTime: 20,
			},
			// The function blindly formats what is given, which is a valid SDP syntax behavior
			wantContains: []string{
				"m=audio 50000 RTP/AVP 127",
				"a=rtpmap:127 UNKNOWN/12345",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateAnswer(tt.ip, tt.port, tt.stream)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateAnswer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			sdpStr := string(got)
			for _, want := range tt.wantContains {
				if !strings.Contains(sdpStr, want) {
					t.Errorf("GenerateAnswer() SDP missing substring %q.\nGot:\n%s", want, sdpStr)
				}
			}
		})
	}
}
