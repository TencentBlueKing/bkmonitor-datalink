// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"math"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// ViewClientCounts is the Worker's account of decision-016's view stream:
// whether it holds a stream to the Leader, which version it has installed,
// how many of that version's objects it cannot read, and what happened on
// the way. Every Worker reports these; in the shadow step nothing executes
// off the installed view, so a Worker with no stream is a Worker with one
// fewer diagnostic and nothing else.
type ViewClientCounts struct {
	Connected                         bool
	InstalledRevision, InstalledEpoch uint64
	// ObjectsMissing is read only when ObjectsProbed; unprobed, the gauge
	// is NaN, which is what "unknown" is on a gauge, and never 0.
	ObjectsMissing     int
	ObjectsProbed      bool
	Installs           map[string]uint64
	InstallFailures    map[string]uint64
	SnapshotsRequested uint64
	Refusals           map[string]uint64
	Connections        uint64
	// DiscoveryMisses is by reason: viewClientDiscoveryMisses.
	DiscoveryMisses map[string]uint64
}

// viewClientInstallFailures and viewClientRefusals are the closed label
// sets; a word outside them is reported under other, so a new reason
// cannot add a series without being named here.
var (
	viewClientInstallKinds    = []string{"snapshot", "delta", "empty_delta"}
	viewClientInstallFailures = []string{"DELTA_BASE_MISMATCH", "DELTA_DIGEST_MISMATCH", "SNAPSHOT_INVALID", "SNAPSHOT_INCOMPLETE", "VIEW_FOR_ANOTHER_WORKER"}
	viewClientRefusals        = []string{"NOT_LEADER", "UNKNOWN_WORKER", "BAD_TOKEN", "PROTOCOL_VERSION", "REGISTRY_UNAVAILABLE", "HELLO_EXPECTED", "REPLACED_BY_NEW_STREAM", "IDLE", "SHUTDOWN"}
	// viewClientDiscoveryMisses tells "no control leader lease" from "the
	// Leader's registration advertises no endpoint": #216 counted the second
	// as the first on every Worker while the Leader published.
	viewClientDiscoveryMisses = []string{"NO_LEADER", "LEADER_UNREGISTERED", "LEADER_NO_ENDPOINT", "DISCOVERY_FAILED"}
)

type viewClientCollector struct {
	mu              sync.Mutex
	source          func() ViewClientCounts
	connected       *prometheus.Desc
	installed       *prometheus.Desc
	objectsMissing  *prometheus.Desc
	installs        *prometheus.Desc
	installFailures *prometheus.Desc
	snapshots       *prometheus.Desc
	refusals        *prometheus.Desc
	connections     *prometheus.Desc
	discoveryMisses *prometheus.Desc
}

func newViewClientCollector() *viewClientCollector {
	name := func(suffix string) string { return prometheus.BuildFQName(metricNamespace, metricSubsystem, suffix) }
	return &viewClientCollector{
		connected: prometheus.NewDesc(name("view_client_connected"),
			"1 while this Worker holds an admitted stream to the Control Leader, else 0. Read the fleet's sum against "+
				"the Leader's view_stream_sessions: they count the same streams from both ends.", nil, nil),
		installed: prometheus.NewDesc(name("view_installed_revision"),
			"The view revision this Worker has installed, 0 before any. Read it against the Leader's view_revision: "+
				"a Worker behind by one is waiting for a delta, one behind by more is about to get a snapshot, one stuck "+
				"behind has view_install_total{result} saying why.", nil, nil),
		objectsMissing: prometheus.NewDesc(name("view_objects_missing"),
			"Objects the installed view names that this Worker can neither serve from its cache nor find in the "+
				"catalog, as of the last install, counted over the whole installed view every time. A gauge of the "+
				"current view, not a running count: it answers whether the content is there now. Anything above 0 "+
				"is content the view promises and the Worker could not execute. NaN when the last install could "+
				"not probe the catalog: unknown is not 0.", nil, nil),
		installs: prometheus.NewDesc(name("view_install_total"),
			"Views this Worker installed, by kind: snapshot, delta (something changed for this Worker), empty_delta "+
				"(the revision moved and nothing changed for this Worker). Their sum is the installs; failures are "+
				"on view_install_failure_total.", []string{"kind"}, nil),
		installFailures: prometheus.NewDesc(name("view_install_failure_total"),
			"Views this Worker refused to install, by why: DELTA_BASE_MISMATCH (the delta's base is not the "+
				"installed view), DELTA_DIGEST_MISMATCH (the applied result does not hash to the target), "+
				"SNAPSHOT_INVALID (the assembled snapshot does not verify), SNAPSHOT_INCOMPLETE (a newer snapshot "+
				"began before the last was whole), VIEW_FOR_ANOTHER_WORKER. Each is followed by a snapshot request; "+
				"a reason that keeps rising is a Leader and a Worker that do not agree on the body.",
			[]string{"reason"}, nil),
		snapshots: prometheus.NewDesc(name("view_snapshot_request_total"),
			"Snapshots this Worker asked for after refusing what it was sent.", nil, nil),
		refusals: prometheus.NewDesc(name("view_client_refusal_total"),
			"Streams the Leader refused this Worker, by the Leader's reason.", []string{"reason"}, nil),
		connections: prometheus.NewDesc(name("view_client_connection_total"),
			"Streams this Worker opened and had admitted. Rising without the Leader changing is a stream that keeps "+
				"dropping; the view_session log line says with what reason.", nil, nil),
		discoveryMisses: prometheus.NewDesc(name("view_discovery_miss_total"),
			"Attempts that found no Leader to connect to, by reason: NO_LEADER is no control leader lease; "+
				"LEADER_UNREGISTERED a lease naming a Worker with no live registration; LEADER_NO_ENDPOINT a Leader "+
				"whose registration advertises no endpoint (it could not work out its own address, or predates the "+
				"stream); DISCOVERY_FAILED a registry that could not be read.", []string{"reason"}, nil),
	}
}

// SetViewClientSource binds the collector to the Worker's client.
func (r *Recorder) SetViewClientSource(source func() ViewClientCounts) {
	if r == nil || r.phaseTwo.viewClient == nil {
		return
	}
	r.phaseTwo.viewClient.mu.Lock()
	r.phaseTwo.viewClient.source = source
	r.phaseTwo.viewClient.mu.Unlock()
}

func (c *viewClientCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.connected, c.installed, c.objectsMissing, c.installs, c.installFailures, c.snapshots, c.refusals, c.connections, c.discoveryMisses} {
		ch <- desc
	}
}

func (c *viewClientCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	counts := source()
	connected := 0.0
	if counts.Connected {
		connected = 1
	}
	ch <- prometheus.MustNewConstMetric(c.connected, prometheus.GaugeValue, connected)
	ch <- prometheus.MustNewConstMetric(c.installed, prometheus.GaugeValue, float64(counts.InstalledRevision))
	objectsMissing := math.NaN()
	if counts.ObjectsProbed {
		objectsMissing = float64(counts.ObjectsMissing)
	}
	ch <- prometheus.MustNewConstMetric(c.objectsMissing, prometheus.GaugeValue, objectsMissing)
	ch <- prometheus.MustNewConstMetric(c.snapshots, prometheus.CounterValue, float64(counts.SnapshotsRequested))
	ch <- prometheus.MustNewConstMetric(c.connections, prometheus.CounterValue, float64(counts.Connections))
	emitClosed(ch, c.discoveryMisses, viewClientDiscoveryMisses, counts.DiscoveryMisses)
	emitClosed(ch, c.installs, viewClientInstallKinds, counts.Installs)
	emitClosed(ch, c.installFailures, viewClientInstallFailures, counts.InstallFailures)
	emitClosed(ch, c.refusals, viewClientRefusals, counts.Refusals)
}

// emitClosed emits one series per word of the closed set, zero included,
// and folds any word outside it into other.
func emitClosed(ch chan<- prometheus.Metric, desc *prometheus.Desc, words []string, counts map[string]uint64) {
	known := make(map[string]struct{}, len(words))
	for _, word := range words {
		known[word] = struct{}{}
		ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, float64(counts[word]), word)
	}
	var other uint64
	for word, count := range counts {
		if _, ok := known[word]; !ok {
			other += count
		}
	}
	ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, float64(other), "other")
}
