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
package api

import (
	"encoding/json"
	"testing"
)

func TestWebSocketMessageTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name: "WsMetadataMessage",
			input: WsMetadataMessage{
				Type:        "metadata",
				Codec:       "opus",
				SampleRate:  48000,
				Channels:    1,
				PTime:       20,
				DTMFEnabled: true,
				IsBigEndian: false,
			},
			expected: `{"type":"metadata","codec":"opus","sample_rate":48000,"channels":1,"ptime":20,"dtmf_enabled":true,"is_big_endian":false}`,
		},
		{
			name: "WsDtmfMessage",
			input: WsDtmfMessage{
				Type:      "dtmf",
				Digit:     "5",
				Duration:  100,
				Timestamp: 1621000000000,
			},
			expected: `{"type":"dtmf","digit":"5","duration":100,"ts":1621000000000}`,
		},
		{
			name: "WsDtmfRequest",
			input: WsDtmfRequest{
				Type:     "dtmf",
				Digit:    "9",
				Duration: 250,
			},
			expected: `{"type":"dtmf","digit":"9","duration":250}`,
		},
		{
			name: "WsByeRequest",
			input: WsByeRequest{
				Type: "bye",
			},
			expected: `{"type":"bye"}`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("got %s, want %s", string(data), tt.expected)
			}

			// Test unmarshal back
			var unmarshaled map[string]interface{}
			if err := json.Unmarshal(data, &unmarshaled); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
		})
	}
}
