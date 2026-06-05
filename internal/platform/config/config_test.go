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
package config

import (
	"os"
	"testing"

	"github.com/heoruwulf/teleportal/internal/audio"
)

func TestLoadCoreConfig_WebSocketCodec(t *testing.T) {
	tests := []struct {
		name      string
		wsCodec   string
		expectErr bool
	}{
		{
			name:      "Valid L16",
			wsCodec:   string(audio.CodecL16),
			expectErr: false,
		},
		{
			name:      "Valid PCMU",
			wsCodec:   string(audio.CodecPCMU),
			expectErr: false,
		},
		{
			name:      "Valid PCMA",
			wsCodec:   string(audio.CodecPCMA),
			expectErr: false,
		},
		{
			name:      "Valid pass",
			wsCodec:   string(audio.CodecPass),
			expectErr: false,
		},
		{
			name:      "Valid pass lowercase",
			wsCodec:   "pass",
			expectErr: false,
		},
		{
			name:      "Empty - Invalid",
			wsCodec:   "",
			expectErr: true,
		},
		{
			name:      "Unknown - Invalid",
			wsCodec:   "opus",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env
			os.Unsetenv("TELEPORTAL_WS_CODEC")
			if tt.wsCodec != "" {
				os.Setenv("TELEPORTAL_WS_CODEC", tt.wsCodec)
			}

			cfg, err := LoadCoreConfig()
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				// The loaded config should always be uppercase, even if input was lowercase
				expectedCodec := tt.wsCodec
				if expectedCodec == "pass" {
					expectedCodec = string(audio.CodecPass)
				}
				if cfg.Audio.WebSocketCodec != expectedCodec {
					t.Errorf("expected WebSocketCodec %s, got %s", expectedCodec, cfg.Audio.WebSocketCodec)
				}
			}
		})
	}
}
