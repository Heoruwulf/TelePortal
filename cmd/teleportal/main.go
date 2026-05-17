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
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/Heoruwulf/TelePortal/internal/app"
	"github.com/Heoruwulf/TelePortal/internal/platform/config"
	"github.com/Heoruwulf/TelePortal/internal/platform/logging"
	"go.uber.org/zap"
)

func main() {
	// Load configuration from environment variables.
	cfg, err := config.LoadCoreConfig()
	if err != nil {
		panic("Failed to load configuration: " + err.Error())
	}

	logger, atomicLevel, err := logging.New(cfg.Log)
	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("Starting TelePortal Service...")
	defer logger.Info("TelePortal service shutdown complete.")

	// Application Startup
	application, err := app.NewApp(logger, atomicLevel, cfg)
	if err != nil {
		logger.Fatal("Failed to create application", zap.Error(err))
	}

	// Run The Service
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Dedicated handler for SIGHUP and SIGQUIT
	otherSigs := make(chan os.Signal, 1)
	signal.Notify(otherSigs, syscall.SIGHUP, syscall.SIGQUIT)

	go func() {
		for sig := range otherSigs {
			switch sig {
			case syscall.SIGHUP:
				_ = application.Reload(context.Background())
			case syscall.SIGQUIT:
				logger.Info("SIGQUIT received: dumping stacks and initiating shutdown")
				dumpStacks(logger)
				stop() // Trigger the primary shutdown context
			}
		}
	}()

	// Use a dedicated context for the application's run loop so we can control
	// when listeners stop independently of the signal.
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	// Use a channel to listen for and report errors from the main application routine.
	serverErrors := make(chan error, 1)

	go func() {
		serverErrors <- application.Run(runCtx)
	}()

	// Mark application as ready
	application.SetReady(true)
	logger.Info("TelePortal service is now ready to receive traffic")

	// Graceful Shutdown Orchestration
	select {
	case err := <-serverErrors:
		if !errors.Is(err, context.Canceled) {
			logger.Error("Server error, shutting down", zap.Error(err))
		} else {
			logger.Info("Server shutdown initiated gracefully")
		}

	case <-sigCtx.Done():
		// This case handles the SIGINT/SIGTERM signal.
		logger.Info("Shutdown signal received, initiating safe shutdown sequence", zap.Duration("shutdown_timeout", cfg.ShutdownTimeout))
		stop() // Stop listening for signals

		// Drain active calls
		drainCtx, drainCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		if err := application.Drain(drainCtx); err != nil {
			logger.Warn("Drain phase completed with warning (likely timeout)", zap.Error(err))
		}
		drainCancel()

		// Stop listeners by canceling the run context
		runCancel()
	}

	// Final cleanup (force hangups if any calls remain)
	const finalCleanupTimeout = 10 * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), finalCleanupTimeout)
	defer cancel()

	if err := application.Shutdown(shutdownCtx); err != nil {
		logger.Error("Final cleanup failed", zap.Error(err))
	}
}

func dumpStacks(log *zap.Logger) {
	buf := make([]byte, 1024*1024)
	n := runtime.Stack(buf, true)
	fmt.Fprintf(os.Stderr, "=== STACK DUMP ===\n%s\n", buf[:n])
	log.Info("Stack dump written to stderr", zap.Int("bytes", n))
}
