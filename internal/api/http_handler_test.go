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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/heoruwulf/teleportal/internal/call"
	"github.com/heoruwulf/teleportal/internal/platform/config"
	"github.com/heoruwulf/teleportal/internal/platform/metrics"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

func generateTestToken(secret, subject string, admin bool) string {
	claims := TelePortalClaims{
		Admin: admin,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte(secret))
	return tokenString
}

func TestHTTPHandler_Healthz(t *testing.T) {
	t.Parallel()
	e := echo.New()
	log := zap.NewNop()
	m := metrics.NewNoOpProvider()
	cm := call.NewCallManager(log, m)
	cfg := &config.CoreConfig{}
	var isReady atomic.Bool
	h := NewHTTPHandler(log, cm, m, cfg, &isReady)
	h.RegisterHandlers(e)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestHTTPHandler_Readyz(t *testing.T) {
	t.Parallel()
	e := echo.New()
	log := zap.NewNop()
	m := metrics.NewNoOpProvider()
	cm := call.NewCallManager(log, m)
	cfg := &config.CoreConfig{}
	var isReady atomic.Bool
	h := NewHTTPHandler(log, cm, m, cfg, &isReady)
	h.RegisterHandlers(e)

	// Test unready state
	isReady.Store(false)
	req1 := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec1.Code)
	}

	// Test ready state
	isReady.Store(true)
	req2 := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec2.Code)
	}
}

func TestHTTPHandler_ListCalls_Authentication(t *testing.T) {
	t.Parallel()

	secret := "test-secret"
	validAdminToken := generateTestToken(secret, "", true)
	validWildcardToken := generateTestToken(secret, "*", false)
	validNonAdminToken := generateTestToken(secret, "specific-call", false)

	tests := []struct {
		name           string
		jwtSecret      string
		authHeader     string
		expectedStatus int
	}{
		{
			name:           "No secret -> Success (Unprotected)",
			jwtSecret:      "",
			authHeader:     "",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "With secret, no token -> Unauthorized",
			jwtSecret:      secret,
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "With secret, valid admin token -> Success",
			jwtSecret:      secret,
			authHeader:     "Bearer " + validAdminToken,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "With secret, valid wildcard token -> Success",
			jwtSecret:      secret,
			authHeader:     "Bearer " + validWildcardToken,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "With secret, non-admin token -> Forbidden",
			jwtSecret:      secret,
			authHeader:     "Bearer " + validNonAdminToken,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			log := zap.NewNop()
			m := metrics.NewNoOpProvider()
			cm := call.NewCallManager(log, m)
			cfg := &config.CoreConfig{
				Auth: config.AuthConfig{
					JWTSecret: tt.jwtSecret,
				},
			}
			var isReady atomic.Bool
			h := NewHTTPHandler(log, cm, m, cfg, &isReady)
			h.RegisterHandlers(e)

			req := httptest.NewRequest(http.MethodGet, "/v1/calls", nil)
			if tt.authHeader != "" {
				req.Header.Set(echo.HeaderAuthorization, tt.authHeader)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tt.expectedStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHTTPHandler_HandleUpgrade_Authentication(t *testing.T) {
	t.Parallel()

	secret := "test-secret"
	validTokenTargetCall := generateTestToken(secret, "mock-internal-id", false)
	validTokenWrongCall := generateTestToken(secret, "wrong-internal-id", false)
	validTokenWildcard := generateTestToken(secret, "*", false)
	validTokenAdmin := generateTestToken(secret, "", true)

	tests := []struct {
		name           string
		jwtSecret      string
		authHeader     string
		queryParam     string
		callID         string
		expectedStatus int
	}{
		{
			name:           "No secret, no token -> Skip auth (404 as call not found)",
			jwtSecret:      "",
			authHeader:     "",
			callID:         "target-call",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "With secret, no token -> Unauthorized",
			jwtSecret:      secret,
			authHeader:     "",
			callID:         "target-call",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "With secret, invalid token -> Unauthorized",
			jwtSecret:      secret,
			authHeader:     "Bearer invalid-token",
			callID:         "target-call",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "With secret, valid token wrong subject -> Forbidden",
			jwtSecret:      secret,
			authHeader:     "Bearer " + validTokenWrongCall,
			callID:         "target-call",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "With secret, valid token matching subject -> Success (404 as call not found)",
			jwtSecret:      secret,
			authHeader:     "Bearer " + validTokenTargetCall,
			callID:         "target-call",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "With secret, valid token wildcard subject -> Success (404 as call not found)",
			jwtSecret:      secret,
			authHeader:     "Bearer " + validTokenWildcard,
			callID:         "target-call",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "With secret, valid token admin -> Success (404 as call not found)",
			jwtSecret:      secret,
			authHeader:     "Bearer " + validTokenAdmin,
			callID:         "target-call",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "With secret, valid token from query param -> Success (404 as call not found)",
			jwtSecret:      secret,
			authHeader:     "",
			queryParam:     validTokenTargetCall,
			callID:         "target-call",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			log := zap.NewNop()
			m := metrics.NewNoOpProvider()
			cm := call.NewCallManager(log, m)
			cfg := &config.CoreConfig{
				Auth: config.AuthConfig{
					JWTSecret: tt.jwtSecret,
				},
			}
			var isReady atomic.Bool
			h := NewHTTPHandler(log, cm, m, cfg, &isReady)
			h.RegisterHandlers(e)

			url := "/v1/listen/mock-internal-id/" + tt.callID
			if tt.queryParam != "" {
				url += "?token=" + tt.queryParam
			}

			req := httptest.NewRequest(http.MethodGet, url, nil)
			if tt.authHeader != "" {
				req.Header.Set(echo.HeaderAuthorization, tt.authHeader)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tt.expectedStatus, rec.Code, rec.Body.String())
			}
		})
	}
}
