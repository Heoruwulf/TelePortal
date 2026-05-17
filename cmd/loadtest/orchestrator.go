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
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Heoruwulf/TelePortal/internal/audio"
	"github.com/emiago/sipgo"
	"github.com/go-audio/wav"
	"golang.org/x/sync/errgroup"
)

func (o *Orchestrator) loadAudio() error {
	f, err := os.Open(o.cfg.AudioFile)
	if err != nil {
		return fmt.Errorf("opening audio file: %w", err)
	}
	defer f.Close()

	d := wav.NewDecoder(f)
	if !d.IsValidFile() {
		return fmt.Errorf("invalid WAV file: %s", o.cfg.AudioFile)
	}

	format := d.Format()
	if format.SampleRate != 8000 && format.SampleRate != 16000 && format.SampleRate != 48000 {
		return fmt.Errorf("unsupported sample rate: %d", format.SampleRate)
	}
	if format.NumChannels != 1 {
		return fmt.Errorf("unsupported channels: %d", format.NumChannels)
	}

	// Read raw bytes from the WAV data chunk
	d.FwdToPCM()
	intBuf, err := d.FullPCMBuffer()
	if err != nil {
		return fmt.Errorf("reading PCM data: %w", err)
	}

	// Convert the IntBuffer data back to raw bytes.
	// Since WAV is little-endian, we reconstruct the bytes accordingly depending on bit depth.
	var buf []byte
	switch d.BitDepth {
	case 16:
		buf = make([]byte, len(intBuf.Data)*2)
		for i, sample := range intBuf.Data {
			buf[i*2] = byte(sample & 0xFF)
			buf[i*2+1] = byte((sample >> 8) & 0xFF)
		}
	case 8:
		buf = make([]byte, len(intBuf.Data))
		for i, sample := range intBuf.Data {
			buf[i] = byte(sample)
		}
	default:
		return fmt.Errorf("unsupported bit depth: %d", d.BitDepth)
	}

	// Determine Codec based on WAV format
	o.sampleRate = int(format.SampleRate)

	// In go-audio/wav, format.AudioFormat 1 is PCM, 6 is A-law, 7 is mu-law
	switch d.WavAudioFormat {
	case 7: // mu-law
		o.payloadType = audio.PayloadTypePCMU
		o.codecName = audio.CodecPCMU
		o.payload = buf
	case 6: // A-law
		o.payloadType = audio.PayloadTypePCMA
		o.codecName = audio.CodecPCMA
		o.payload = buf
	case 1: // PCM
		o.payloadType = audio.PayloadTypeL16
		o.codecName = audio.CodecL16
		// WAV PCM is Little Endian. RTP expects Big Endian.
		if d.BitDepth == 16 {
			beBuf, err := audio.DecodeL16LEToL16BE(buf)
			if err != nil {
				return fmt.Errorf("converting LE to BE: %w", err)
			}
			o.payload = beBuf
		} else {
			return fmt.Errorf("unsupported bit depth for PCM: %d", d.BitDepth)
		}
	default:
		return fmt.Errorf("unsupported audio format tag: %d", d.WavAudioFormat)
	}

	log.Printf("Loaded %d bytes of %s audio from %s (Sample Rate: %d)", len(o.payload), o.codecName, o.cfg.AudioFile, o.sampleRate)
	return nil
}

type CallState string

const (
	StateDialing   CallState = "Dialing"
	StateConnected CallState = "Connected"
	StateWSEstab   CallState = "WS Established"
	StateError     CallState = "Error"
	StateFinished  CallState = "Finished"
)

type Simulator struct {
	StartTime  time.Time
	LastError  error
	caller     *Caller
	rtpEngine  *RTPEngine
	wsAgent    *WSEchoAgent
	cancel     context.CancelFunc
	CallID     string
	State      CallState
	ID         int
	DTMFSent   int
	DTMFEchoed int
}

type Orchestrator struct {
	sipClient    *sipgo.Client
	redisWatch   *RedisWatcher
	stateUpdates chan *Simulator
	codecName    audio.CodecName
	payload      []byte
	sims         []*Simulator
	cfg          Config
	sampleRate   int
	simMu        sync.Mutex
	payloadType  uint8
}

// ... skipped ...

func (o *Orchestrator) GetStats() (total, connected, ws, errs, dtmfSent, dtmfEchoed int) {
	o.simMu.Lock()
	defer o.simMu.Unlock()
	total = len(o.sims)
	for _, sim := range o.sims {
		switch sim.State {
		case StateConnected:
			connected++
		case StateWSEstab:
			ws++
			connected++ // it's also connected via SIP
		case StateError:
			errs++
		}
		dtmfSent += sim.DTMFSent
		dtmfEchoed += sim.DTMFEchoed
	}
	return
}

func NewOrchestrator(cfg Config) (*Orchestrator, error) {
	ua, err := sipgo.NewUA()
	if err != nil {
		return nil, fmt.Errorf("creating SIP UA: %w", err)
	}

	client, err := sipgo.NewClient(ua)

	if err != nil {
		return nil, fmt.Errorf("creating SIP client: %w", err)
	}

	rw := NewRedisWatcher(cfg.RedisAddr)

	o := &Orchestrator{
		cfg:          cfg,
		sipClient:    client,
		redisWatch:   rw,
		sims:         make([]*Simulator, 0, cfg.Calls),
		stateUpdates: make(chan *Simulator, 100),
	}

	if err := o.loadAudio(); err != nil {
		return nil, fmt.Errorf("loading audio: %w", err)
	}

	return o, nil
}

func (o *Orchestrator) Start(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	// Start Redis Watcher
	g.Go(func() error {
		return o.redisWatch.Start(ctx)
	})

	g.Go(func() error {
		o.handleRedisEvents(ctx)
		return nil
	})

	// Start simulators
	g.Go(func() error {
		// Wait a moment to ensure Redis subscription is fully established
		time.Sleep(200 * time.Millisecond)

		// Calculate ramp-up delay
		var delay time.Duration
		if o.cfg.Calls > 1 {
			delay = o.cfg.RampUp / time.Duration(o.cfg.Calls-1)
		}

		for i := 0; i < o.cfg.Calls; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			simCtx, cancel := context.WithCancel(ctx)
			sim := &Simulator{
				ID:        i,
				State:     StateDialing,
				StartTime: time.Now(),
				cancel:    cancel,
			}

			o.simMu.Lock()
			o.sims = append(o.sims, sim)
			o.simMu.Unlock()

			o.notifyState(sim)

			go o.runSimulator(simCtx, sim)

			if i < o.cfg.Calls-1 {
				time.Sleep(delay)
			}
		}

		// Wait for duration
		select {
		case <-ctx.Done():
		case <-time.After(o.cfg.Duration):
		}

		// Stop all
		o.StopAll()
		return nil
	})

	return g.Wait()
}

func (o *Orchestrator) runSimulator(ctx context.Context, sim *Simulator) {
	localIP := net.ParseIP("127.0.0.1") // Can be parameterized

	// Target port is parsed from SIPAddr
	host, _, err := net.SplitHostPort(o.cfg.SIPAddr)
	if err != nil {
		host = o.cfg.SIPAddr
	}
	targetIP := net.ParseIP(host)
	if targetIP == nil {
		addrs, _ := net.LookupIP(host)
		if len(addrs) > 0 {
			targetIP = addrs[0]
		} else {
			targetIP = net.ParseIP("127.0.0.1")
		}
	}

	rtpPortBase := 10000 + (sim.ID * 2)
	rtp, err := NewRTPEngine(localIP, targetIP, rtpPortBase, o.payloadType, o.sampleRate, o.payload)
	if err != nil {
		sim.State = StateError
		sim.LastError = err
		log.Printf("Error running sim: %v", err)
		o.notifyState(sim)
		return
	}
	sim.rtpEngine = rtp

	if o.cfg.DTMF > 0 {
		rtp.SetDTMF(o.cfg.DTMF, o.cfg.Duration, &sim.DTMFSent, &sim.DTMFEchoed)
	}

	caller := NewCaller(o.sipClient, o.cfg.SIPAddr, localIP, rtp.LocalPort(), o.payloadType, string(o.codecName), o.sampleRate)
	sim.caller = caller
	sim.CallID = caller.callID

	err = caller.Dial(ctx)
	if err != nil {
		sim.State = StateError
		sim.LastError = err
		log.Printf("Error running sim: %v", err)
		o.notifyState(sim)
		return
	}

	sim.State = StateConnected
	o.notifyState(sim)

	// Start RTP (this blocks until ctx is done)
	if err := rtp.Start(ctx); err != nil && err != context.Canceled {
		log.Printf("RTP error for sim %d: %v", sim.ID, err)
	}

	// Teardown
	hangCtx, hangCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer hangCancel()
	caller.Hangup(hangCtx)
	rtp.Close()

	sim.State = StateFinished
	o.notifyState(sim)
}

func (o *Orchestrator) handleRedisEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-o.redisWatch.Events():
			if evt.Event == "connected" {
				o.simMu.Lock()
				var targetSim *Simulator
				for _, sim := range o.sims {
					if sim.CallID == evt.CallID {
						targetSim = sim
						break
					}
				}
				o.simMu.Unlock()

				if targetSim != nil && targetSim.wsAgent == nil {
					wsURL := evt.WebSocketURL

					wsAgent := NewWSEchoAgent(wsURL)
					targetSim.wsAgent = wsAgent
					// We pass the orchestrator context so the connection is bound to the test lifecycle
					err := wsAgent.Connect(ctx)
					if err == nil {
						targetSim.State = StateWSEstab
						o.notifyState(targetSim)
						// Start WebSocket echo in a goroutine.
						// We use a raw goroutine here because these are dynamic and independent.
						go func() {
							if err := wsAgent.StartEcho(ctx); err != nil && err != context.Canceled {
								log.Printf("WS Echo error for call %s: %v", evt.CallID, err)
							}
						}()
					} else {
						log.Printf("WS Connect error: %v", err)
					}
				}
			}
		}
	}
}

func (o *Orchestrator) notifyState(sim *Simulator) {
	select {
	case o.stateUpdates <- sim:
	default:
	}
}

func (o *Orchestrator) StopAll() {
	o.simMu.Lock()
	defer o.simMu.Unlock()
	for _, sim := range o.sims {
		if sim.cancel != nil {
			sim.cancel()
		}
	}
}

func (o *Orchestrator) StateUpdates() <-chan *Simulator {
	return o.stateUpdates
}
