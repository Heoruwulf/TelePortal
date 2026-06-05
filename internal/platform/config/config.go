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
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/heoruwulf/teleportal/internal/audio"
)

// LogConfig contains configuration for the logger.
type LogConfig struct {
	Level  string
	Format string
	Path   string
}

// SIPConfig contains configuration for the SIP listener and advertiser.
type SIPConfig struct {
	BindAddress     string
	ExternalAddress string
	BindPort        int
	ExternalPort    int
	MaxCalls        int
}

// RTPConfig contains configuration for the RTP listeners.
type RTPConfig struct {
	BindAddress     string
	ExternalAddress string
	PortMin         uint16
	PortMax         uint16
}

// AudioConfig contains configuration for audio processing.
type AudioConfig struct {
	RecordingPath              string
	WebSocketCodec             string
	JitterBufferMinPacketCount int
}

// HTTPServerConfig contains configuration for the internal HTTP server (for WebSockets).
type HTTPServerConfig struct {
	BindAddress string
	PublicURL   string
	CORSOrigins string
}

type FeaturesConfig struct {
	PProf      bool
	Prometheus bool
}

// AuthConfig contains configuration for authentication.
type AuthConfig struct {
	JWTSecret string
}

// RedisConfig contains configuration for Redis connection.
type RedisConfig struct {
	Address  string
	Password string
	DB       int
}

// CoreConfig is the top-level configuration for the service.
type CoreConfig struct {
	Log             LogConfig
	HTTPServer      HTTPServerConfig
	Auth            AuthConfig
	RTP             RTPConfig
	Audio           AudioConfig
	Redis           RedisConfig
	SIP             SIPConfig
	Features        FeaturesConfig
	ShutdownTimeout time.Duration
}

// String implements the fmt.Stringer interface to redact sensitive fields when logging.
func (c CoreConfig) String() string {
	// Create a shallow copy so we can mutate the sensitive fields before marshaling
	safeConfig := c

	if safeConfig.Auth.JWTSecret == "" {
		safeConfig.Auth.JWTSecret = "[EMPTY]"
	} else {
		safeConfig.Auth.JWTSecret = "[REDACTED]"
	}

	if safeConfig.Redis.Password == "" {
		safeConfig.Redis.Password = "[EMPTY]"
	} else {
		safeConfig.Redis.Password = "[REDACTED]"
	}

	b, _ := json.Marshal(safeConfig)
	return string(b)
}

// LoadCoreConfig loads configuration for the core service from environment variables.
func LoadCoreConfig() (*CoreConfig, error) {
	wsCodec := strings.ToUpper(getEnv("TELEPORTAL_WS_CODEC", ""))
	switch wsCodec {
	case string(audio.CodecL16), string(audio.CodecPCMU), string(audio.CodecPCMA), string(audio.CodecPass):
		// valid
	case "":
		return nil, fmt.Errorf("TELEPORTAL_WS_CODEC is mandatory and must be one of: %s, %s, %s, %s", audio.CodecL16, audio.CodecPCMU, audio.CodecPCMA, audio.CodecPass)
	default:
		return nil, fmt.Errorf("invalid TELEPORTAL_WS_CODEC '%s': must be one of: %s, %s, %s, %s", wsCodec, audio.CodecL16, audio.CodecPCMU, audio.CodecPCMA, audio.CodecPass)
	}

	return &CoreConfig{
		Log: LogConfig{
			Level:  getEnv("TELEPORTAL_LOG_LEVEL", "info"),
			Format: getEnv("TELEPORTAL_LOG_FORMAT", "console"),
			Path:   getEnv("TELEPORTAL_LOG_PATH", ""),
		},
		SIP: SIPConfig{
			BindAddress:     getEnv("TELEPORTAL_SIP_BIND_ADDRESS", "0.0.0.0"),
			BindPort:        getEnvInt("TELEPORTAL_SIP_BIND_PORT", 5060),
			ExternalAddress: getEnv("TELEPORTAL_SIP_EXTERNAL_ADDRESS", ""),
			ExternalPort:    getEnvInt("TELEPORTAL_SIP_EXTERNAL_PORT", 0),
			MaxCalls:        getEnvInt("TELEPORTAL_MAX_CALLS", 0),
		},
		RTP: RTPConfig{
			BindAddress:     getEnv("TELEPORTAL_RTP_BIND_ADDRESS", "0.0.0.0"),
			ExternalAddress: getEnv("TELEPORTAL_RTP_EXTERNAL_ADDRESS", ""),
			PortMin:         uint16(getEnvInt("TELEPORTAL_RTP_PORT_MIN", 10000)),
			PortMax:         uint16(getEnvInt("TELEPORTAL_RTP_PORT_MAX", 20000)),
		},
		Audio: AudioConfig{
			JitterBufferMinPacketCount: getEnvInt("TELEPORTAL_AUDIO_JB_MIN_PACKETS", 0),
			RecordingPath:              getEnv("TELEPORTAL_RECORDING_PATH", ""),
			WebSocketCodec:             wsCodec,
		},
		HTTPServer: HTTPServerConfig{
			BindAddress: getEnv("TELEPORTAL_HTTP_BIND_ADDRESS", "0.0.0.0:8080"),
			PublicURL:   getEnv("TELEPORTAL_HTTP_PUBLIC_URL", "http://localhost:8080"),
			CORSOrigins: getEnv("TELEPORTAL_HTTP_CORS_ORIGINS", ""),
		},
		Features: FeaturesConfig{
			PProf:      getEnvBool("TELEPORTAL_FEATURES_PPROF", false),
			Prometheus: getEnvBool("TELEPORTAL_FEATURES_PROMETHEUS", false),
		},
		Auth: AuthConfig{
			JWTSecret: getEnv("TELEPORTAL_AUTH_JWT_SECRET", ""),
		},
		Redis: RedisConfig{
			Address:  getEnv("TELEPORTAL_REDIS_ADDRESS", "localhost:6379"),
			Password: getEnv("TELEPORTAL_REDIS_PASSWORD", ""),
			DB:       getEnvInt("TELEPORTAL_REDIS_DB", 0),
		},
		ShutdownTimeout: getEnvDuration("TELEPORTAL_SHUTDOWN_TIMEOUT", 5*time.Minute),
	}, nil
}

// Helper functions for getting environment variables with defaults.

func getEnv(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, ok := os.LookupEnv(key); ok {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value, ok := os.LookupEnv(key); ok {
		if durationValue, err := time.ParseDuration(value); err == nil {
			return durationValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}
