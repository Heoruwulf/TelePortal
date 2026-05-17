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
package app

import (
	"context"
	"os"
	"testing"

	"github.com/Heoruwulf/TelePortal/internal/platform/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestApp_Reload(t *testing.T) {
	// We cannot run this test in parallel if we use os.Setenv globally,
	// but t.Setenv is scoped to the test and is safe for parallel tests
	// if they don't depend on the same env vars.
	// However, config.LoadCoreConfig reads global env, so t.Setenv is perfect.

	initialLogLevel := "info"
	t.Setenv("TELEPORTAL_LOG_LEVEL", initialLogLevel)
	t.Setenv("TELEPORTAL_MAX_CALLS", "100")
	t.Setenv("TELEPORTAL_WS_CODEC", "L16")

	cfg, err := config.LoadCoreConfig()
	if err != nil {
		t.Fatalf("Failed to load initial config: %v", err)
	}

	atomicLevel := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	log := zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zapcore.AddSync(os.Stdout),
		atomicLevel,
	))

	a := &App{
		log:            log,
		logAtomicLevel: atomicLevel,
		config:         cfg,
	}

	t.Run("ReloadLogLevel", func(t *testing.T) {
		newLogLevel := "debug"
		t.Setenv("TELEPORTAL_LOG_LEVEL", newLogLevel)

		if err := a.Reload(context.Background()); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}

		if a.config.Log.Level != newLogLevel {
			t.Errorf("Expected log level %s, got %s", newLogLevel, a.config.Log.Level)
		}

		if atomicLevel.Level() != zapcore.DebugLevel {
			t.Errorf("Expected atomic level to be DebugLevel, got %v", atomicLevel.Level())
		}
	})

	t.Run("ReloadMaxCalls", func(t *testing.T) {
		newMaxCalls := "500"
		t.Setenv("TELEPORTAL_MAX_CALLS", newMaxCalls)

		if err := a.Reload(context.Background()); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}

		if a.config.SIP.MaxCalls != 500 {
			t.Errorf("Expected max calls 500, got %d", a.config.SIP.MaxCalls)
		}
	})

	t.Run("ReloadJitterBuffer", func(t *testing.T) {
		newJB := "10"
		t.Setenv("TELEPORTAL_AUDIO_JB_MIN_PACKETS", newJB)

		if err := a.Reload(context.Background()); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}

		if a.config.Audio.JitterBufferMinPacketCount != 10 {
			t.Errorf("Expected jitter buffer 10, got %d", a.config.Audio.JitterBufferMinPacketCount)
		}
	})
}
