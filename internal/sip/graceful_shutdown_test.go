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
package sip

import (
	"sync/atomic"
	"testing"

	"github.com/Heoruwulf/TelePortal/internal/platform/metrics"
	"github.com/emiago/sipgo/sip"
	"go.uber.org/zap"
)

type mockTransaction struct {
	sip.ServerTransaction
	lastResponse *sip.Response
}

func (m *mockTransaction) Respond(res *sip.Response) error {
	m.lastResponse = res
	return nil
}

func TestSIPHandler_NotReadyReturns503(t *testing.T) {
	log := zap.NewNop()
	m := metrics.NewNoOpProvider()
	var isReady atomic.Bool
	isReady.Store(false) // NOT READY

	h := &SIPHandler{
		log:     log,
		metrics: m,
		isReady: &isReady,
	}

	req := sip.NewRequest(sip.INVITE, sip.Uri{User: "user", Host: "localhost"})
	req.AppendHeader(sip.NewHeader("Call-ID", "test-call-id"))
	req.AppendHeader(&sip.FromHeader{Address: sip.Uri{User: "alice", Host: "atlanta.com"}})
	req.AppendHeader(&sip.ToHeader{Address: sip.Uri{User: "bob", Host: "biloxi.com"}})
	tx := &mockTransaction{}

	h.handleInvite(req, tx)

	if tx.lastResponse == nil {
		t.Fatal("Expected a response, got nil")
	}
	if tx.lastResponse.StatusCode != 503 {
		t.Errorf("Expected status code 503, got %d", tx.lastResponse.StatusCode)
	}
}

func TestSIPHandler_OptionsWhenNotReadyReturns503(t *testing.T) {
	log := zap.NewNop()
	m := metrics.NewNoOpProvider()
	var isReady atomic.Bool
	isReady.Store(false) // NOT READY

	h := &SIPHandler{
		log:     log,
		metrics: m,
		isReady: &isReady,
	}

	req := sip.NewRequest(sip.OPTIONS, sip.Uri{User: "user", Host: "localhost"})
	req.AppendHeader(sip.NewHeader("Call-ID", "test-call-id"))
	tx := &mockTransaction{}

	h.handleOptions(req, tx)

	if tx.lastResponse == nil {
		t.Fatal("Expected a response, got nil")
	}
	if tx.lastResponse.StatusCode != 503 {
		t.Errorf("Expected status code 503, got %d", tx.lastResponse.StatusCode)
	}
}
