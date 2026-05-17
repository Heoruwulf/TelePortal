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
	"reflect"
	"testing"

	"github.com/Heoruwulf/TelePortal/internal/audio"
	"github.com/Heoruwulf/TelePortal/internal/platform/config"
	pionsip "github.com/pion/sdp/v3"
)

func TestNegotiateStream(t *testing.T) {
	h := &SIPHandler{
		config: &config.CoreConfig{
			Audio: config.AudioConfig{
				WebSocketCodec: string(audio.CodecPass),
			},
		},
	}

	tests := []struct {
		name    string
		sdp     pionsip.SessionDescription
		want    audio.Stream
		wantErr bool
	}{
		{
			name: "PCMU default",
			sdp: pionsip.SessionDescription{
				MediaDescriptions: []*pionsip.MediaDescription{
					{
						MediaName: pionsip.MediaName{
							Media:   "audio",
							Formats: []string{"0"},
						},
					},
				},
			},
			want: audio.Stream{
				Codec: audio.Codec{
					Name:           "PCMU",
					PayloadType:    0,
					SampleRate:     8000,
					Channels:       1,
					BytesPerSample: 1,
				},
				PTime: 20,
			},
			wantErr: false,
		},
		{
			name: "L16 8000Hz",
			sdp: pionsip.SessionDescription{
				MediaDescriptions: []*pionsip.MediaDescription{
					{
						MediaName: pionsip.MediaName{
							Media:   "audio",
							Formats: []string{"96"},
						},
						Attributes: []pionsip.Attribute{
							{Key: "rtpmap", Value: "96 L16/8000"},
						},
					},
				},
			},
			want: audio.Stream{
				Codec: audio.Codec{
					Name:           "L16",
					PayloadType:    96,
					SampleRate:     8000,
					Channels:       1,
					IsBigEndian:    true,
					IsSigned:       true,
					BytesPerSample: 2,
				},
				PTime: 20,
			},
			wantErr: false,
		},
		{
			name: "PCMU with telephone-event",
			sdp: pionsip.SessionDescription{
				MediaDescriptions: []*pionsip.MediaDescription{
					{
						MediaName: pionsip.MediaName{
							Media:   "audio",
							Formats: []string{"0", "101"},
						},
						Attributes: []pionsip.Attribute{
							{Key: "rtpmap", Value: "101 telephone-event/8000"},
						},
					},
				},
			},
			want: audio.Stream{
				Codec: audio.Codec{
					Name:           "PCMU",
					PayloadType:    0,
					SampleRate:     8000,
					Channels:       1,
					BytesPerSample: 1,
				},
				PTime:           20,
				DTMFPayloadType: 101,
			},
			wantErr: false,
		},
		{
			name: "No supported codec",
			sdp: pionsip.SessionDescription{
				MediaDescriptions: []*pionsip.MediaDescription{
					{
						MediaName: pionsip.MediaName{
							Media:   "audio",
							Formats: []string{"99"},
						},
						Attributes: []pionsip.Attribute{
							{Key: "rtpmap", Value: "99 UNKNOWN/8000"},
						},
					},
				},
			},
			want:    audio.Stream{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := h.negotiateStream(tt.sdp)
			if (err != nil) != tt.wantErr {
				t.Errorf("negotiateStream() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("negotiateStream() got = %v, want %v", got, tt.want)
			}
		})
	}
}
