// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"regexp"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const (
	metricNamespace = "bkmonitor"
	metricSubsystem = "alarmd"
	otherLabel      = "_other"
)

type BuildInfo struct {
	Version       string
	Commit        string
	SchemaVersion string
}

type Recorder struct {
	registry          *prometheus.Registry
	lifecycleMu       sync.Mutex
	lifecycleBound    bool
	healthMu          sync.Mutex
	healthBound       bool
	fleetMu           sync.Mutex
	fleetBound        bool
	queryPermitMu     sync.Mutex
	queryPermitBound  bool
	capacityLoadMu    sync.Mutex
	capacityLoadBound bool
	resourceMu        sync.Mutex
	resourceBound     bool
	observations      observationMetrics
	receipts          receiptMetrics
	phaseTwo          phaseTwoMetrics
}

func NewRecorder(build BuildInfo) *Recorder {
	registry := prometheus.NewRegistry()
	buildInfo := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "build_info",
			Help:      "alarmd build information.",
		},
		[]string{"version", "commit", "schema_version"},
	)
	buildInfo.WithLabelValues(build.Version, build.Commit, build.SchemaVersion).Set(1)
	observations := newObservationMetrics()
	receipts := newReceiptMetrics()
	phaseTwo := newPhaseTwoMetrics()

	collectorsToRegister := []prometheus.Collector{
		// The legacy Go collector exports memstats but no CPU split, so the
		// share of CPU spent in GC versus user code is not observable. alarmd
		// is allocation driven (hundreds of MB/s), which makes that split the
		// first question of any CPU work; add the bounded rule rather than
		// MetricsAll so the series count stays predictable.
		collectors.NewGoCollector(collectors.WithGoCollectorRuntimeMetrics(
			collectors.GoRuntimeMetricsRule{Matcher: regexp.MustCompile(`^/cpu/classes/`)},
			collectors.GoRuntimeMetricsRule{Matcher: regexp.MustCompile(`^/sched/latencies:seconds$`)},
		)),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		// Where the live heap comes from, by allocating function; see heap_sites.go.
		newHeapSiteCollector(time.Now),
		buildInfo,
	}
	collectorsToRegister = append(collectorsToRegister, observations.collectors()...)
	collectorsToRegister = append(collectorsToRegister, receipts.collectors()...)
	collectorsToRegister = append(collectorsToRegister, phaseTwo.collectors()...)
	registry.MustRegister(collectorsToRegister...)

	return &Recorder{
		registry:     registry,
		observations: observations,
		receipts:     receipts,
		phaseTwo:     phaseTwo,
	}
}

func (r *Recorder) Gatherer() prometheus.Gatherer {
	return r.registry
}
