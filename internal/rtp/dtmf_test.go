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
package rtp

import "testing"

func TestParseDTMFPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		wantDigit    string
		payload      []byte
		wantDuration uint16
		wantEnd      bool
		wantErr      bool
	}{
		{
			name:         "Digit 5, start/middle, duration 160",
			payload:      []byte{0x05, 0x00, 0x00, 0xA0}, // Digit 5, E=0, R=0, Volume=0, Duration=160
			wantDigit:    "5",
			wantEnd:      false,
			wantDuration: 160,
			wantErr:      false,
		},
		{
			name:         "Digit *, end bit set, duration 480",
			payload:      []byte{0x0A, 0x80, 0x01, 0xE0}, // Digit 10 (*), E=1, R=0, Volume=0, Duration=480
			wantDigit:    "*",
			wantEnd:      true,
			wantDuration: 480,
			wantErr:      false,
		},
		{
			name:         "Digit #, duration 800",
			payload:      []byte{0x0B, 0x00, 0x03, 0x20}, // Digit 11 (#), E=0, R=0, Volume=0, Duration=800
			wantDigit:    "#",
			wantEnd:      false,
			wantDuration: 800,
			wantErr:      false,
		},
		{
			name:         "Invalid payload length",
			payload:      []byte{0x01, 0x02, 0x03},
			wantDigit:    "",
			wantEnd:      false,
			wantDuration: 0,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			digit, end, duration, err := ParseDTMFPayload(tt.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDTMFPayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if digit != tt.wantDigit {
				t.Errorf("ParseDTMFPayload() digit = %v, want %v", digit, tt.wantDigit)
			}
			if end != tt.wantEnd {
				t.Errorf("ParseDTMFPayload() end = %v, want %v", end, tt.wantEnd)
			}
			if duration != tt.wantDuration {
				t.Errorf("ParseDTMFPayload() duration = %v, want %v", duration, tt.wantDuration)
			}
		})
	}
}
