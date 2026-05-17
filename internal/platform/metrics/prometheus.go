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
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PrometheusProvider is the production implementation of the Provider interface.
type PrometheusProvider struct {
	sipRequests         *prometheus.CounterVec
	activeCalls         prometheus.Gauge
	wsConnections       prometheus.Gauge
	transcodingDuration *prometheus.HistogramVec
	jbUnderflows        *prometheus.CounterVec
	jbSilence           *prometheus.CounterVec
	jbDropped           *prometheus.CounterVec
}

// NewPrometheusProvider initializes a new PrometheusProvider and registers its collectors.
func NewPrometheusProvider(registry prometheus.Registerer) *PrometheusProvider {
	p := &PrometheusProvider{
		sipRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "teleportal_sip_requests_total",
				Help: "Total number of SIP requests handled.",
			},
			[]string{"method", "status"},
		),
		activeCalls: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "teleportal_calls_active",
				Help: "Number of currently active calls.",
			},
		),
		wsConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "teleportal_ws_connections_active",
				Help: "Number of currently active WebSocket connections.",
			},
		),
		transcodingDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "teleportal_transcoding_duration_seconds",
				Help:    "Histogram of transcoding duration.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"codec"},
		),
		jbUnderflows: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "teleportal_jb_underflows_total",
				Help: "Total number of jitter buffer underflows (gaps).",
			},
			[]string{"call_id"},
		),
		jbSilence: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "teleportal_jb_silence_emitted_total",
				Help: "Total number of silence packets emitted by the jitter buffer.",
			},
			[]string{"call_id"},
		),
		jbDropped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "teleportal_jb_dropped_packets_total",
				Help: "Total number of packets dropped by the jitter buffer.",
			},
			[]string{"call_id"},
		),
	}

	registry.MustRegister(
		p.sipRequests,
		p.activeCalls,
		p.wsConnections,
		p.transcodingDuration,
		p.jbUnderflows,
		p.jbSilence,
		p.jbDropped,
	)

	return p
}

func (p *PrometheusProvider) IncSIPRequests(method string, status string) {
	p.sipRequests.WithLabelValues(method, status).Inc()
}

func (p *PrometheusProvider) UpdateActiveCalls(count int) {
	p.activeCalls.Set(float64(count))
}

func (p *PrometheusProvider) IncWSConnections() {
	p.wsConnections.Inc()
}

func (p *PrometheusProvider) DecWSConnections() {
	p.wsConnections.Dec()
}

func (p *PrometheusProvider) ObserveTranscodingDuration(codec string, duration time.Duration) {
	p.transcodingDuration.WithLabelValues(codec).Observe(duration.Seconds())
}

func (p *PrometheusProvider) ReportJitterBufferStats(callID string, underflows, silence, dropped uint64) {
	p.jbUnderflows.WithLabelValues(callID).Add(float64(underflows))
	p.jbSilence.WithLabelValues(callID).Add(float64(silence))
	p.jbDropped.WithLabelValues(callID).Add(float64(dropped))
}
