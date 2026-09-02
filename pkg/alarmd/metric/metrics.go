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
	"math"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const (
	metricNamespace    = "bkmonitor"
	metricSubsystem    = "alarmd"
	otherLabel         = "_other"
	CustomSeriesBudget = 19000
)

var (
	processDurationBuckets = []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 7.5, 10, 30}
	pipelineLatencyBuckets = []float64{1, 2, 3, 5, 10, 15, 20, 30, 60, 180, 300}
)

type Stage string

const (
	StageDetect  Stage = "detect"
	StageTrigger Stage = "trigger"
	stageOther   Stage = otherLabel
)

type Mode string

const (
	ModeShadow Mode = "shadow"
	ModeOwner  Mode = "owner"
	modeOther  Mode = otherLabel
)

type Status string

const (
	StatusSuccess  Status = "success"
	StatusFailed   Status = "failed"
	StatusDeferred Status = "deferred"
	statusOther    Status = otherLabel
)

type ErrorCode string

const (
	ErrorNone         ErrorCode = "none"
	ErrorInvalidInput ErrorCode = "invalid_input"
	ErrorUnsupported  ErrorCode = "unsupported"
	ErrorInternal     ErrorCode = "internal"
	errorOther        ErrorCode = otherLabel
)

type Direction string

const (
	DirectionInput  Direction = "input"
	DirectionOutput Direction = "output"
	directionOther  Direction = otherLabel
)

type RecordType string

const (
	RecordDetectionOutcome RecordType = "detection_outcome"
	RecordTriggerDecision  RecordType = "trigger_decision"
	recordTypeOther        RecordType = otherLabel
)

type Component string

const (
	ComponentTrigger Component = "trigger"
	componentOther   Component = otherLabel
)

type CompareResult string

const (
	CompareMatch         CompareResult = "match"
	CompareMismatch      CompareResult = "mismatch"
	CompareMissingPython CompareResult = "missing_python"
	CompareMissingGo     CompareResult = "missing_go"
	CompareTimeout       CompareResult = "timeout"
	compareOther         CompareResult = otherLabel
)

type BuildInfo struct {
	Version       string
	Commit        string
	SchemaVersion string
}

type Recorder struct {
	registry        *prometheus.Registry
	lifecycleMu     sync.Mutex
	lifecycleBound  bool
	healthMu        sync.Mutex
	healthBound     bool
	resourceMu      sync.Mutex
	resourceBound   bool
	processDuration *prometheus.HistogramVec
	processTotal    *prometheus.CounterVec
	recordsTotal    *prometheus.CounterVec
	pipelineLatency *prometheus.HistogramVec
	shadowCompare   *prometheus.CounterVec
	observations    observationMetrics
	receipts        receiptMetrics
}

func NewRecorder(build BuildInfo) *Recorder {
	registry := prometheus.NewRegistry()
	processDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "process_duration_seconds",
			Help:      "alarmd processing duration in seconds.",
			Buckets:   append([]float64(nil), processDurationBuckets...),
		},
		[]string{"stage", "mode"},
	)
	processTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "process_total",
			Help:      "alarmd processing results.",
		},
		[]string{"stage", "mode", "status", "error_code"},
	)
	recordsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "records_total",
			Help:      "alarmd input and output records.",
		},
		[]string{"stage", "mode", "direction", "record_type"},
	)
	pipelineLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "pipeline_latency_seconds",
			Help:      "alarmd latency between pipeline stages in seconds.",
			Buckets:   append([]float64(nil), pipelineLatencyBuckets...),
		},
		[]string{"from_stage", "to_stage", "mode"},
	)
	shadowCompare := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "shadow_compare_total",
			Help:      "alarmd shadow comparison results.",
		},
		[]string{"component", "result"},
	)
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

	collectorsToRegister := []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		processDuration,
		processTotal,
		recordsTotal,
		pipelineLatency,
		shadowCompare,
	}
	collectorsToRegister = append(collectorsToRegister, observations.collectors()...)
	collectorsToRegister = append(collectorsToRegister, receipts.collectors()...)
	registry.MustRegister(collectorsToRegister...)

	return &Recorder{
		registry:        registry,
		processDuration: processDuration,
		processTotal:    processTotal,
		recordsTotal:    recordsTotal,
		pipelineLatency: pipelineLatency,
		shadowCompare:   shadowCompare,
		observations:    observations,
		receipts:        receipts,
	}
}

func (r *Recorder) Gatherer() prometheus.Gatherer {
	return r.registry
}

func (r *Recorder) RecordProcess(stage Stage, mode Mode, status Status, code ErrorCode, duration time.Duration) {
	stage = normalizeStage(stage)
	mode = normalizeMode(mode)
	r.processTotal.WithLabelValues(string(stage), string(mode), string(normalizeStatus(status)), string(normalizeError(code))).Inc()
	if duration >= 0 {
		r.processDuration.WithLabelValues(string(stage), string(mode)).Observe(duration.Seconds())
	}
}

func (r *Recorder) RecordRecords(stage Stage, mode Mode, direction Direction, recordType RecordType, count float64) {
	if count <= 0 || math.IsNaN(count) || math.IsInf(count, 0) {
		return
	}
	r.recordsTotal.WithLabelValues(
		string(normalizeStage(stage)),
		string(normalizeMode(mode)),
		string(normalizeDirection(direction)),
		string(normalizeRecordType(recordType)),
	).Add(count)
}

func (r *Recorder) RecordPipelineLatency(from, to Stage, mode Mode, duration time.Duration) {
	if duration < 0 {
		return
	}
	from, to = normalizeEdge(from, to)
	r.pipelineLatency.WithLabelValues(string(from), string(to), string(normalizeMode(mode))).Observe(duration.Seconds())
}

func (r *Recorder) RecordShadowCompare(component Component, result CompareResult) {
	r.shadowCompare.WithLabelValues(string(normalizeComponent(component)), string(normalizeCompareResult(result))).Inc()
}

func MaxCustomSeries() int {
	histogramSeries := func(bucketCount int) int {
		return bucketCount + 1 + 2 // explicit buckets, +Inf, sum and count
	}

	processTotal := len(allStages) * len(allModes) * len(allStatuses) * len(allErrors)
	processDuration := len(allStages) * len(allModes) * histogramSeries(len(processDurationBuckets))
	recordsTotal := len(allStages) * len(allModes) * len(allDirections) * len(allRecordTypes)
	pipelineLatency := len(allEdges) * len(allModes) * histogramSeries(len(pipelineLatencyBuckets))
	shadowCompare := len(allComponents) * len(allCompareResults)
	buildInfo := 1
	return processTotal + processDuration + recordsTotal + pipelineLatency + shadowCompare + buildInfo +
		lifecycleCustomSeries() + observationCustomSeries() + healthResourceCustomSeries() + receiptCustomSeries()
}

var (
	allStages         = []Stage{StageDetect, StageTrigger, stageOther}
	allModes          = []Mode{ModeShadow, ModeOwner, modeOther}
	allStatuses       = []Status{StatusSuccess, StatusFailed, StatusDeferred, statusOther}
	allErrors         = []ErrorCode{ErrorNone, ErrorInvalidInput, ErrorUnsupported, ErrorInternal, errorOther}
	allDirections     = []Direction{DirectionInput, DirectionOutput, directionOther}
	allRecordTypes    = []RecordType{RecordDetectionOutcome, RecordTriggerDecision, recordTypeOther}
	allComponents     = []Component{ComponentTrigger, componentOther}
	allCompareResults = []CompareResult{
		CompareMatch,
		CompareMismatch,
		CompareMissingPython,
		CompareMissingGo,
		CompareTimeout,
		compareOther,
	}
	allEdges = [][2]Stage{{StageDetect, StageTrigger}, {stageOther, stageOther}}
)

func normalizeStage(value Stage) Stage {
	if value == StageDetect || value == StageTrigger {
		return value
	}
	return stageOther
}

func normalizeMode(value Mode) Mode {
	if value == ModeShadow || value == ModeOwner {
		return value
	}
	return modeOther
}

func normalizeStatus(value Status) Status {
	if value == StatusSuccess || value == StatusFailed || value == StatusDeferred {
		return value
	}
	return statusOther
}

func normalizeError(value ErrorCode) ErrorCode {
	if value == ErrorNone || value == ErrorInvalidInput || value == ErrorUnsupported || value == ErrorInternal {
		return value
	}
	return errorOther
}

func normalizeDirection(value Direction) Direction {
	if value == DirectionInput || value == DirectionOutput {
		return value
	}
	return directionOther
}

func normalizeRecordType(value RecordType) RecordType {
	if value == RecordDetectionOutcome || value == RecordTriggerDecision {
		return value
	}
	return recordTypeOther
}

func normalizeComponent(value Component) Component {
	if value == ComponentTrigger {
		return value
	}
	return componentOther
}

func normalizeCompareResult(value CompareResult) CompareResult {
	if value == CompareMatch || value == CompareMismatch || value == CompareMissingPython || value == CompareMissingGo || value == CompareTimeout {
		return value
	}
	return compareOther
}

func normalizeEdge(from, to Stage) (Stage, Stage) {
	if from == StageDetect && to == StageTrigger {
		return from, to
	}
	return stageOther, stageOther
}
