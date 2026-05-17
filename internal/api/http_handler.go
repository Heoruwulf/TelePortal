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
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Heoruwulf/TelePortal/internal/call"
	"github.com/Heoruwulf/TelePortal/internal/platform/config"
	"github.com/Heoruwulf/TelePortal/internal/platform/metrics"
	pkgapi "github.com/Heoruwulf/TelePortal/pkg/api"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// TelePortalClaims represents the standard JWT claims plus custom scopes for TelePortal.
type TelePortalClaims struct {
	jwt.RegisteredClaims
	Admin bool `json:"admin,omitempty"`
}

// HTTPHandler handles all HTTP-based requests for the core service,
// including WebSocket upgrades and API calls.
type HTTPHandler struct {
	// --- Pointer-containing fields (prefix for GC scanning) ---

	// 16 bytes
	metrics metrics.Provider

	// 8 bytes
	log         *zap.Logger
	callManager *call.CallManager
	config      *config.CoreConfig
	isReady     *atomic.Bool

	// Struct containing multiple pointer fields
	upgrader websocket.Upgrader
}

// NewHTTPHandler creates a new handler.
func NewHTTPHandler(log *zap.Logger, cm *call.CallManager, m metrics.Provider, cfg *config.CoreConfig, isReady *atomic.Bool) *HTTPHandler {
	upgrader := websocket.Upgrader{}

	// Override default CheckOrigin if custom CORS origins are configured
	if cfg.HTTPServer.CORSOrigins != "" {
		upgrader.CheckOrigin = func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // Non-browser clients usually don't send an Origin header
			}

			for _, allowed := range strings.Split(cfg.HTTPServer.CORSOrigins, ",") {
				allowed = strings.TrimSpace(allowed)
				if allowed == "*" || strings.EqualFold(allowed, origin) {
					return true
				}
			}
			return false
		}
	}

	return &HTTPHandler{
		log:         log.Named("http_handler"),
		callManager: cm,
		metrics:     m,
		upgrader:    upgrader,
		config:      cfg,
		isReady:     isReady,
	}
}

// authMiddleware handles JWT validation from the Authorization header or token query parameter.
func (h *HTTPHandler) authMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if h.config.Auth.JWTSecret == "" {
				return next(c) // Authentication disabled
			}

			tokenString := ""
			// 1. Try to get token from Authorization header
			authHeader := c.Request().Header.Get(echo.HeaderAuthorization)
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenString = strings.TrimPrefix(authHeader, "Bearer ")
			}

			// 2. Fallback to query parameter (needed for standard WebSockets in browser)
			if tokenString == "" {
				tokenString = c.QueryParam("token")
			}

			if tokenString == "" {
				return c.String(http.StatusUnauthorized, "Missing authentication token")
			}

			claims := &TelePortalClaims{}
			token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return []byte(h.config.Auth.JWTSecret), nil
			})

			if err != nil || !token.Valid {
				h.log.Warn("Invalid API auth token", zap.Error(err))
				return c.String(http.StatusUnauthorized, "Invalid authentication token")
			}

			c.Set("claims", claims)
			return next(c)
		}
	}
}

// RegisterHandlers registers the API routes with the Echo instance.
func (h *HTTPHandler) RegisterHandlers(e *echo.Echo) {
	// Middleware
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:    true,
		LogStatus: true,
		LogMethod: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			h.log.Info("request",
				zap.String("URI", v.URI),
				zap.Int("status", v.Status),
				zap.String("method", v.Method),
				zap.String("remote_ip", v.RemoteIP),
			)
			return nil
		},
	}))
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())

	if h.config.HTTPServer.CORSOrigins != "" {
		origins := strings.Split(h.config.HTTPServer.CORSOrigins, ",")
		for i := range origins {
			origins[i] = strings.TrimSpace(origins[i])
		}
		e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins: origins,
			AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
		}))
		h.log.Info("CORS middleware configured", zap.Strings("allowed_origins", origins))
	}

	// Root level routes
	e.GET("/healthz", h.HandleHealthz) // Legacy liveness
	e.GET("/livez", h.HandleLivez)     // Modern liveness
	e.GET("/readyz", h.HandleReadyz)   // Readiness

	if h.config.Features.Prometheus {
		e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
	}

	// API v1 group
	v1 := e.Group("/v1")
	v1.Use(h.authMiddleware())
	{
		v1.GET("/calls", h.HandleListCalls)
		v1.GET("/listen/:internal_id/:call_id", h.HandleUpgrade)
	}
}

var healthyResponseBytes = []byte(`{"status": "healthy"}`)
var unreadyResponseBytes = []byte(`{"status": "unready"}`)

// HandleHealthz is the liveness probe. It returns 200 as long as the server is running.
func (h *HTTPHandler) HandleHealthz(c echo.Context) error {
	return c.JSONBlob(http.StatusOK, healthyResponseBytes)
}

// HandleLivez is the modern Kubernetes liveness probe.
func (h *HTTPHandler) HandleLivez(c echo.Context) error {
	return c.JSONBlob(http.StatusOK, healthyResponseBytes)
}

// HandleReadyz is the readiness probe.
// It checks if the application is fully initialized and ready to handle traffic.
func (h *HTTPHandler) HandleReadyz(c echo.Context) error {
	if h.isReady != nil && !h.isReady.Load() {
		return c.JSONBlob(http.StatusServiceUnavailable, unreadyResponseBytes)
	}
	return c.JSONBlob(http.StatusOK, healthyResponseBytes)
}

// HandleListCalls is the Echo handler for GET /v1/calls.
func (h *HTTPHandler) HandleListCalls(c echo.Context) error {
	if h.config.Auth.JWTSecret != "" {
		claims, ok := c.Get("claims").(*TelePortalClaims)
		if !ok || claims == nil {
			return c.String(http.StatusInternalServerError, "Missing claims in context")
		}
		// Global or Admin scope required to list calls
		if claims.Subject != "*" && !claims.Admin {
			h.log.Warn("Forbidden: token missing admin scope for list calls")
			return c.String(http.StatusForbidden, "Admin privileges required to list calls")
		}
	}

	activeCalls := h.callManager.ListActiveCalls()

	calls := make([]pkgapi.CallDetail, len(activeCalls))
	for i, ac := range activeCalls {
		calls[i] = pkgapi.CallDetail{
			CallID:  ac.CallID,
			Headers: ac.Headers,
		}
	}

	return c.JSON(http.StatusOK, &pkgapi.CallsResponse{Calls: calls})
}

// HandleUpgrade is the Echo handler for GET /v1/listen/:internal_id/:call_id
func (h *HTTPHandler) HandleUpgrade(c echo.Context) error {
	callID := c.Param("call_id")
	if callID == "" {
		h.log.Warn("Missing call_id parameter")
		return c.String(http.StatusBadRequest, "Missing call_id parameter")
	}

	internalID := c.Param("internal_id")
	if internalID == "" {
		h.log.Warn("Missing internal_id parameter")
		return c.String(http.StatusBadRequest, "Missing internal_id parameter")
	}

	// Scope validation
	if h.config.Auth.JWTSecret != "" {
		claims, ok := c.Get("claims").(*TelePortalClaims)
		if !ok || claims == nil {
			return c.String(http.StatusInternalServerError, "Missing claims in context")
		}

		// Access granted if token has Admin rights, a wildcard subject, or a subject matching the specific internal_id
		if !claims.Admin && claims.Subject != "*" && claims.Subject != internalID {
			h.log.Warn("Forbidden: token scope mismatch", zap.String("token_sub", claims.Subject), zap.String("req_internal_id", internalID))
			return c.String(http.StatusForbidden, "Not authorized for this call")
		}
	}

	activeCall, ok := h.callManager.GetByInternalID(internalID)
	if !ok {
		h.log.Warn("WebSocket upgrade request for non-existent call (internal ID lookup failed)", zap.String("internal_id", internalID))
		return c.String(http.StatusNotFound, "Call not found")
	}

	if activeCall.CallID != callID {
		h.log.Warn("WebSocket upgrade request mismatch: CallID does not match InternalID", zap.String("sip_call_id", callID), zap.String("internal_id", internalID), zap.String("expected_call_id", activeCall.CallID))
		return c.String(http.StatusNotFound, "Call not found")
	}

	// Ensure only one WebSocket listener per call
	if !activeCall.AudioBridge.TryLock() {
		h.log.Warn("WebSocket upgrade request for call already has a listener", zap.String("sip_call_id", callID))
		return c.String(http.StatusConflict, "Call already has an active listener")
	}

	ws, err := h.upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		activeCall.AudioBridge.Unlock() // Release reservation on failure
		h.log.Error("Failed to upgrade WebSocket connection", zap.Error(err))
		return err
	}
	defer ws.Close()

	// Send initial headers to the client BEFORE adding to audio bridge
	// This avoids concurrent write panic since audio bridge starts a write pump goroutine
	if err := ws.WriteJSON(pkgapi.WsHeadersMessage{
		Type:    "headers",
		Headers: activeCall.Headers,
		Started: activeCall.LiveAt.Format(time.RFC3339Nano),
	}); err != nil {
		h.log.Error("Failed to send initial headers over WebSocket", zap.Error(err))
		return err
	}

	activeCall.AudioBridge.AddClient(ws)

	h.log.Info("WebSocket client connected and listening",
		zap.String("sip_call_id", callID),
		zap.String("remote_addr", ws.RemoteAddr().String()),
	)

	// Block the HTTP handler while reading from the WebSocket.
	// The read pump will exit when the connection is closed or an error occurs.
	activeCall.AudioBridge.ReadPump(ws)
	h.log.Info("WebSocket client disconnected gracefully", zap.String("remote_addr", ws.RemoteAddr().String()))

	return nil
}
