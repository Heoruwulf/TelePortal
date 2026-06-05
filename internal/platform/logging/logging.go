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
// /convointel/internal/platform/logging/logging.go
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/heoruwulf/teleportal/internal/platform/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New creates a new Zap logger based on the provided configuration.
// It returns the logger and its AtomicLevel to allow dynamic level updates.
func New(cfg config.LogConfig) (*zap.Logger, zap.AtomicLevel, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(cfg.Level))); err != nil {
		level = zapcore.InfoLevel // Default level
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	outputPaths := []string{"stdout"}
	errorOutputPaths := []string{"stderr"}

	if cfg.Path != "" {
		if err := os.MkdirAll(cfg.Path, 0755); err != nil {
			return nil, zap.AtomicLevel{}, fmt.Errorf("failed to create log directory: %w", err)
		}
		filename := fmt.Sprintf("%s_teleportal.log", time.Now().Format("20060102_150405"))
		fullPath := filepath.Join(cfg.Path, filename)
		outputPaths = append(outputPaths, fullPath)
		errorOutputPaths = append(errorOutputPaths, fullPath)
	}

	atomicLevel := zap.NewAtomicLevelAt(level)
	config := zap.Config{
		Level:             atomicLevel,
		Development:       false,
		DisableCaller:     false,
		DisableStacktrace: true,
		Sampling: &zap.SamplingConfig{
			Initial:    100,
			Thereafter: 100,
		},
		EncoderConfig:    encoderConfig,
		OutputPaths:      outputPaths,
		ErrorOutputPaths: errorOutputPaths,
	}

	switch strings.ToLower(cfg.Format) {
	case "json":
		config.Encoding = "json"
	case "console":
		config.Encoding = "console"
	default:
		return nil, zap.AtomicLevel{}, fmt.Errorf("unsupported log format: %s", cfg.Format)
	}

	logger, err := config.Build()
	if err != nil {
		return nil, zap.AtomicLevel{}, fmt.Errorf("failed to build zap logger: %w", err)
	}

	return logger, atomicLevel, nil
}
