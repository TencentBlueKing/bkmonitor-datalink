// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// cliAuthMetrics counts the CLI authorization's events and when the admin's
// roster was last applied. The event list is cliauth's, bound once at wiring
// (PresetCLIAuth), so every cell exists before the first event.
type cliAuthMetrics struct {
	mu            sync.Mutex
	events        []string
	total         *prometheus.CounterVec
	rosterApplied prometheus.Gauge
}

func newCLIAuthMetrics() *cliAuthMetrics {
	return &cliAuthMetrics{
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Subsystem: metricSubsystem, Name: "cli_auth_total",
			Help: "CLI authorization events this process handled, by event: grants issued on the authorization page " +
				"(grants_issued) and through the admin's front surface (front_grants_issued); front grants refused before " +
				"the first roster sync (front_grants_refused_roster_not_synced) and for an operator off the roster " +
				"(front_grants_refused_not_on_roster); exchanges, pairings and renewals and their refusals; rosters applied, " +
				"unchanged (the same digest again) and rejected; pairings the roster revoked (roster_pairings_revoked, " +
				"counted per pairing); and store_unavailable. Reader: the operator of an admin in front of the deployment, " +
				"and anyone asking why a CLI login failed.",
		}, []string{"event"}),
		rosterApplied: prometheus.NewGauge(prometheus.GaugeOpts{
			Subsystem: metricSubsystem, Name: "cli_roster_applied_timestamp_seconds",
			Help: "When this process last applied a new operator roster from the admin, as Unix seconds; 0 until one is. " +
				"The roster is shared: another channel replica may have applied the latest one.",
		}),
	}
}

// PresetCLIAuth binds the event names and creates every cell.
func (r *Recorder) PresetCLIAuth(events []string) {
	if r == nil || r.phaseTwo.cliAuth == nil {
		return
	}
	m := r.phaseTwo.cliAuth
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append([]string(nil), events...)
	for _, event := range events {
		m.total.WithLabelValues(event)
	}
}

// RecordCLIAuth counts n of one event; an event outside the bound list is
// not counted.
func (r *Recorder) RecordCLIAuth(event string, n uint64) {
	if r == nil || r.phaseTwo.cliAuth == nil {
		return
	}
	m := r.phaseTwo.cliAuth
	m.mu.Lock()
	events := m.events
	m.mu.Unlock()
	if knownLabel(events, event) {
		m.total.WithLabelValues(event).Add(float64(n))
	}
}

// SetCLIRosterApplied publishes when this process last applied a roster.
func (r *Recorder) SetCLIRosterApplied(at time.Time) {
	if r == nil || r.phaseTwo.cliAuth == nil {
		return
	}
	r.phaseTwo.cliAuth.rosterApplied.Set(float64(at.Unix()))
}
