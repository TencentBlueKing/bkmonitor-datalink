// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// LinkdConsoleReading is what the collector reports about the alert link's
// Console: the state word, and each operation's call and failure totals.
type LinkdConsoleReading struct {
	State string
	Calls map[string]LinkdConsoleCalls
}

// LinkdConsoleCalls is one operation's totals since the process started.
type LinkdConsoleCalls struct {
	Calls, Failures uint64
}

// linkdConsoleCollector reports the Console as a dependency of its own.
//
// The absent_strategy families exist only where a Console is configured, so
// a deployment without one was told from one with nothing to close only by
// those families being missing. The state here is present on every replica
// whatever the configuration.
type linkdConsoleCollector struct {
	mu     sync.Mutex
	states []string
	ops    []string
	source func() LinkdConsoleReading
	state  *prometheus.Desc
	calls  *prometheus.Desc
}

func newLinkdConsoleCollector() *linkdConsoleCollector {
	return &linkdConsoleCollector{
		state: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "linkd_console_state"),
			"The alert link's Console as this replica last saw it, one cell per state and 1 on the current one: "+
				"not_configured (the deployment names no Console, so neither the open alert set's calibration nor "+
				"the close of disabled or deleted strategies' alerts runs; by design on a deployment whose events "+
				"all go the Python-compatible way), not_called, unreadable (the latest call of some operation "+
				"failed), link_unhealthy (the Console answered and the link says its own set maintenance is "+
				"failing, never succeeded or is behind; the close refuses every round) and reachable.",
			[]string{"state"}, nil),
		calls: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "linkd_console_calls_total"),
			"Calls this replica made to the alert link's Console, by operation (roster, reconcile, alert_record) "+
				"and result (ok, failed). Only the control leader walks the roster. An alert_record answered with "+
				"'no such alert' is ok. Every cell exists once the source is bound, so a zero is a reading.",
			[]string{"op", "result"}, nil),
	}
}

// SetLinkdConsoleSource binds the collector: the closed state and operation
// lists, and the reading. Safe before or after registration; a nil recorder
// is a no-op.
func (r *Recorder) SetLinkdConsoleSource(states, ops []string, source func() LinkdConsoleReading) {
	if r == nil || r.phaseTwo.linkdConsole == nil {
		return
	}
	c := r.phaseTwo.linkdConsole
	c.mu.Lock()
	c.states, c.ops, c.source = append([]string(nil), states...), append([]string(nil), ops...), source
	c.mu.Unlock()
}

func (c *linkdConsoleCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.state
	ch <- c.calls
}

func (c *linkdConsoleCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	states, ops, source := c.states, c.ops, c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	reading := source()
	for _, state := range states {
		value := 0.0
		if state == reading.State {
			value = 1
		}
		ch <- prometheus.MustNewConstMetric(c.state, prometheus.GaugeValue, value, state)
	}
	for _, op := range ops {
		calls := reading.Calls[op]
		ch <- prometheus.MustNewConstMetric(c.calls, prometheus.CounterValue, float64(calls.Calls-calls.Failures), op, "ok")
		ch <- prometheus.MustNewConstMetric(c.calls, prometheus.CounterValue, float64(calls.Failures), op, "failed")
	}
}
