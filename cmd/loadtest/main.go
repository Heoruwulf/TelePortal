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
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type Config struct {
	SIPAddr   string
	RedisAddr string
	AudioFile string
	RampUp    time.Duration
	Duration  time.Duration
	Calls     int
	DTMF      int // Number of DTMF events to send per call
}

func parseFlags() Config {
	cfg := Config{}
	flag.StringVar(&cfg.SIPAddr, "sip-addr", "127.0.0.1:5060", "TelePortal SIP address")
	flag.StringVar(&cfg.RedisAddr, "redis-addr", "127.0.0.1:6379", "Redis address")
	flag.IntVar(&cfg.Calls, "calls", 50, "Total number of concurrent calls")
	flag.DurationVar(&cfg.RampUp, "ramp-up", 2*time.Second, "Ramp-up time to reach total calls")
	flag.DurationVar(&cfg.Duration, "duration", 60*time.Second, "Duration to hold calls before teardown")
	flag.StringVar(&cfg.AudioFile, "audio", "./testdata/sample.wav", "Path to WAV file to stream")
	flag.IntVar(&cfg.DTMF, "dtmf", 0, "Number of DTMF events to send per call")
	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()

	// Handle graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("\nReceived interrupt, shutting down...")
		cancel()
	}()

	log.Printf("Starting load test with %d calls...", cfg.Calls)

	orch, err := NewOrchestrator(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize orchestrator: %v", err)
	}

	go func() {
		if err := orch.Start(ctx); err != nil && err != context.Canceled {
			log.Printf("Orchestrator exited with error: %v", err)
		}
	}()

	p := tea.NewProgram(initialModel(orch))
	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running program: %v", err)
	}

	// Trigger teardown
	cancel()
	orch.StopAll()
	log.Println("Load test complete.")
}
