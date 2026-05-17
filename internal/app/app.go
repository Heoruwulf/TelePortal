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
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/Heoruwulf/TelePortal/internal/api"
	"github.com/Heoruwulf/TelePortal/internal/audio"
	"github.com/Heoruwulf/TelePortal/internal/call"
	"github.com/Heoruwulf/TelePortal/internal/netutil"
	"github.com/Heoruwulf/TelePortal/internal/platform/cache"
	"github.com/Heoruwulf/TelePortal/internal/platform/config"
	"github.com/Heoruwulf/TelePortal/internal/platform/metrics"
	"github.com/Heoruwulf/TelePortal/internal/rtp"
	"github.com/Heoruwulf/TelePortal/internal/rtp/rtpdefs"
	siphandler "github.com/Heoruwulf/TelePortal/internal/sip"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	_ "net/http/pprof"
)

// App is the main application for the core service.
type App struct {
	log            *zap.Logger
	logAtomicLevel zap.AtomicLevel
	config         *config.CoreConfig
	httpServer     *echo.Echo
	sipServer      *sipgo.Server
	callManager    *call.CallManager
	rtpManager     *rtp.RTPManager
	cache          cache.Cache
	metrics        metrics.Provider
	isReady        atomic.Bool
}

// NewApp creates and returns a new instance of the core application.
func NewApp(logger *zap.Logger, logAtomicLevel zap.AtomicLevel, config *config.CoreConfig) (*App, error) {
	// Initialize audio codec lookup tables
	audio.Initialize()

	// --- Metrics Setup ---
	var m metrics.Provider
	if config.Features.Prometheus {
		m = metrics.NewPrometheusProvider(prometheus.DefaultRegisterer)
		logger.Info("Prometheus metrics initialized")
	} else {
		m = metrics.NewNoOpProvider()
		logger.Info("Metrics disabled (using NoOpProvider)")
	}

	// For SDP and SIP signaling, we need to know our external IP.
	// We'll try to find a non-loopback IP as a fallback.
	fallbackIP, err := netutil.GetOutboundIP()
	if err != nil {
		return nil, fmt.Errorf("failed to determine fallback outbound IP: %w", err)
	}

	// Resolve SIP External Address and Port
	sipExternalIP := net.ParseIP(config.SIP.ExternalAddress)
	if sipExternalIP == nil {
		sipExternalIP = fallbackIP
	}

	sipExternalPort := config.SIP.ExternalPort
	if sipExternalPort == 0 {
		sipExternalPort = config.SIP.BindPort
	}

	// Resolve RTP External Address
	rtpExternalIP := net.ParseIP(config.RTP.ExternalAddress)
	if rtpExternalIP == nil {
		rtpExternalIP = fallbackIP
	}

	rtpBindIP := net.ParseIP(config.RTP.BindAddress)
	if rtpBindIP == nil {
		rtpBindIP = net.IPv4zero
	}

	logger.Info("Network configuration resolved",
		zap.String("sip_bind", fmt.Sprintf("%s:%d", config.SIP.BindAddress, config.SIP.BindPort)),
		zap.String("sip_external", fmt.Sprintf("%s:%d", sipExternalIP.String(), sipExternalPort)),
		zap.String("rtp_bind_ip", rtpBindIP.String()),
		zap.String("rtp_external_ip", rtpExternalIP.String()),
	)

	// --- Cache Setup ---
	var c cache.Cache
	if config.Redis.Address != "" {
		var err error
		c, err = cache.NewRedisCache(config.Redis.Address, config.Redis.Password, config.Redis.DB)
		if err != nil {
			return nil, fmt.Errorf("failed to create redis cache: %w", err)
		}
		logger.Info("Redis cache initialized")
	}

	ua, err := sipgo.NewUA()
	if err != nil {
		return nil, fmt.Errorf("failed to create user agent: %w", err)
	}

	// A client is needed by the DialogUA to send subsequent requests within a dialog (e.g., BYE).
	client, err := sipgo.NewClient(ua)
	if err != nil {
		return nil, fmt.Errorf("failed to create sip client: %w", err)
	}

	sipServer, err := sipgo.NewServer(ua)
	if err != nil {
		return nil, fmt.Errorf("failed to create sip server: %w", err)
	}

	dialogUA := &sipgo.DialogUA{
		Client: client,
		ContactHDR: sip.ContactHeader{ // Our server's contact information
			Address: sip.Uri{
				User: "teleportal",
				Host: sipExternalIP.String(),
				Port: sipExternalPort,
			},
		},
		RewriteContact: true, // Useful for NAT traversal
	}

	callManager := call.NewCallManager(logger, m)
	rtpManager, err := rtp.NewRTPManager(logger, config.RTP.PortMin, config.RTP.PortMax)
	if err != nil {
		return nil, fmt.Errorf("failed to create rtp manager: %w", err)
	}

	e := echo.New()
	e.HideBanner = true

	app := &App{
		log:            logger.Named("app"),
		logAtomicLevel: logAtomicLevel,
		config:         config,
		sipServer:      sipServer,
		httpServer:     e,
		callManager:    callManager,
		rtpManager:     rtpManager,
		cache:          c,
		metrics:        m,
	}

	// Register API and WebSocket handlers
	httpHandler := api.NewHTTPHandler(logger, callManager, m, config, &app.isReady)
	httpHandler.RegisterHandlers(e)

	audioBridgeFactory := func(ctx context.Context, log *zap.Logger, audioInput <-chan rtpdefs.RTPPacket, callID string, stream audio.Stream) call.AudioBridgeInterface {
		return api.NewAudioBridge(ctx, log, m, audioInput, callID, stream, config.Audio.RecordingPath, config.Audio.WebSocketCodec)
	}

	sHandler := siphandler.NewSIPHandler(
		logger.Named("sip"),
		dialogUA,
		callManager,
		rtpManager,
		c,
		m,
		config.HTTPServer.PublicURL,
		rtpBindIP,
		rtpExternalIP,
		config,
		&app.isReady,
		audioBridgeFactory,
	)
	sHandler.RegisterHandlers(sipServer)

	return app, nil
}

// SetReady updates the application's readiness state.
func (a *App) SetReady(ready bool) {
	a.isReady.Store(ready)
}

// Drain initiates the draining phase by stopping new calls and waiting for active ones to finish.
func (a *App) Drain(ctx context.Context) error {
	a.log.Info("Initiating draining phase. No new INVITEs will be accepted.")
	a.SetReady(false)

	stats := a.callManager.Stats()
	if stats.ActiveCalls > 0 {
		a.log.Info("Waiting for active calls to finish...", zap.Int64("count", stats.ActiveCalls))
		if err := a.callManager.WaitEmpty(ctx); err != nil {
			a.log.Warn("Draining timed out or was canceled", zap.Error(err), zap.Int64("remaining_calls", a.callManager.Stats().ActiveCalls))
			return err
		}
		a.log.Info("All active calls have finished naturally.")
	} else {
		a.log.Info("No active calls to drain.")
	}

	return nil
}

// Run starts the core application and blocks until a component fails or the context is canceled.
func (a *App) Run(ctx context.Context) error {
	g, gCtx := errgroup.WithContext(ctx)

	// Start the SIP server listener.
	g.Go(func() error {
		sipAddr := fmt.Sprintf("%s:%d", a.config.SIP.BindAddress, a.config.SIP.BindPort)
		a.log.Info("Starting SIP server", zap.String("address", sipAddr))
		// ListenAndServe will block until the context is canceled or an error occurs.
		return a.sipServer.ListenAndServe(gCtx, "udp", sipAddr)
	})

	// Start the WebSocket HTTP server.
	g.Go(func() error {
		a.log.Info("Starting WebSocket server", zap.String("address", a.config.HTTPServer.BindAddress))
		err := a.httpServer.Start(a.config.HTTPServer.BindAddress)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.log.Error("WebSocket server failed", zap.Error(err))
			return err
		}
		return nil
	})

	// Goroutine to handle graceful shutdown of the HTTP server.
	g.Go(func() error {
		<-gCtx.Done() // Wait for shutdown signal.
		a.log.Info("Shutting down WebSocket server...")
		return a.httpServer.Shutdown(context.Background())
	})

	if a.config.Features.PProf {
		g.Go(func() error {
			// We use the standard library's http server here because pprof registers
			// its handlers on the DefaultServeMux. We bind to localhost for security.
			pprofAddr := "0.0.0.0:6060"
			a.log.Info("Starting pprof server for performance analysis", zap.String("address", pprofAddr))

			// Create a new server instance to handle shutdown gracefully.
			pprofServer := &http.Server{Addr: pprofAddr}

			// This goroutine listens for the context cancellation and shuts down the server.
			go func() {
				<-gCtx.Done()
				a.log.Info("Shutting down pprof server...")
				_ = pprofServer.Shutdown(context.Background())
			}()

			err := pprofServer.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				a.log.Error("pprof server failed", zap.Error(err))
				return err
			}
			return nil
		})
	}

	// Wait for any component to error or for shutdown to be initiated.
	return g.Wait()
}

// Shutdown gracefully stops the application.
func (a *App) Shutdown(ctx context.Context) error {
	a.log.Info("Shutting down core application...")
	// Hang up all active calls before closing the server.
	a.callManager.StopAll(ctx)

	// The sipServer.ListenAndServe is context-aware, so canceling the context in Run()
	// is the primary mechanism for shutdown. We can also call Close for good measure.
	if err := a.sipServer.Close(); err != nil {
		a.log.Error("Error closing SIP server", zap.Error(err))
		return err
	}
	if a.cache != nil {
		_ = a.cache.Close()
	}
	return nil
}

// Reload reloads the application configuration from environment variables and applies
// updates to components that support hot-reloading.
func (a *App) Reload(ctx context.Context) error {
	a.log.Info("Hot-reloading configuration...")

	newCfg, err := config.LoadCoreConfig()
	if err != nil {
		a.log.Error("Failed to reload configuration", zap.Error(err))
		return err
	}

	// 1. Update Log Level
	if newCfg.Log.Level != a.config.Log.Level {
		var newLevel zapcore.Level
		if err := newLevel.UnmarshalText([]byte(newCfg.Log.Level)); err == nil {
			a.log.Info("Updating log level", zap.String("old", a.config.Log.Level), zap.String("new", newCfg.Log.Level))
			a.logAtomicLevel.SetLevel(newLevel)
			a.config.Log.Level = newCfg.Log.Level
		} else {
			a.log.Error("Invalid log level in reloaded config", zap.String("level", newCfg.Log.Level))
		}
	}

	// 2. Update Max Calls
	if newCfg.SIP.MaxCalls != a.config.SIP.MaxCalls {
		a.log.Info("Updating max calls capacity", zap.Int("old", a.config.SIP.MaxCalls), zap.Int("new", newCfg.SIP.MaxCalls))
		a.config.SIP.MaxCalls = newCfg.SIP.MaxCalls
	}

	// 3. Update Jitter Buffer settings
	if newCfg.Audio.JitterBufferMinPacketCount != a.config.Audio.JitterBufferMinPacketCount {
		a.log.Info("Updating jitter buffer min packet count",
			zap.Int("old", a.config.Audio.JitterBufferMinPacketCount),
			zap.Int("new", newCfg.Audio.JitterBufferMinPacketCount))
		a.config.Audio.JitterBufferMinPacketCount = newCfg.Audio.JitterBufferMinPacketCount
	}

	a.log.Info("Configuration hot-reload complete")
	return nil
}
