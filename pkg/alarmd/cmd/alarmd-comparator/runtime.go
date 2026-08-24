// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	httpservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/service/http"
)

// comparisonCounted remembers what one input has already contributed to the
// comparison counters.
type comparisonCounted struct {
	verdict       string
	missingGo     bool
	missingPython bool
}

// comparisonResultLedger keeps a bounded recent deduplication window for the
// comparison counters. One input can be audited again on replay and as coverage
// changes, while a long-running Shadow process must not retain every historical
// input forever.
type comparisonResultLedger struct {
	capacity int
	counted  map[string]comparisonCounted
	order    []string
	head     int
}

func newComparisonResultLedger(capacity int) (*comparisonResultLedger, error) {
	if capacity <= 0 {
		return nil, errors.New("comparator runtime: comparison result capacity must be positive")
	}
	return &comparisonResultLedger{capacity: capacity, counted: make(map[string]comparisonCounted)}, nil
}

// admit returns only the results this audit adds on top of what the input has
// already contributed.
func (l *comparisonResultLedger) admit(audit contract.ComparisonAudit) []metric.CompareResult {
	state, exists := l.counted[audit.InputID]
	if !exists {
		l.makeRoom()
		l.order = append(l.order, audit.InputID)
	}
	var results []metric.CompareResult
	switch audit.Verdict {
	case contract.ComparisonVerdictMatch, contract.ComparisonVerdictHardDiff:
		if state.verdict != audit.Verdict {
			state.verdict = audit.Verdict
			if audit.Verdict == contract.ComparisonVerdictMatch {
				results = append(results, metric.CompareMatch)
			} else {
				results = append(results, metric.CompareMismatch)
			}
		}
	default:
		if audit.Coverage.Phase == contract.ComparisonCoverageMissingAtBarrier {
			for _, role := range audit.Coverage.MissingAtBarrierRoles {
				switch role {
				case contract.ComparisonRoleGo:
					if !state.missingGo {
						state.missingGo = true
						results = append(results, metric.CompareMissingGo)
					}
				case contract.ComparisonRolePython:
					if !state.missingPython {
						state.missingPython = true
						results = append(results, metric.CompareMissingPython)
					}
				}
			}
		}
	}
	l.counted[audit.InputID] = state
	return results
}

func (l *comparisonResultLedger) makeRoom() {
	for len(l.counted) >= l.capacity && l.head < len(l.order) {
		inputID := l.order[l.head]
		l.head++
		delete(l.counted, inputID)
	}
	if l.head == len(l.order) {
		l.order = nil
		l.head = 0
	} else if l.head >= 1024 && l.head*2 >= len(l.order) {
		l.order = append([]string(nil), l.order[l.head:]...)
		l.head = 0
	}
}

type recordingComparisonAuditSink struct {
	recorder *metric.Recorder
	next     enginekafka.ComparisonAuditSink
	logger   *observability.Logger

	mu     sync.Mutex
	ledger *comparisonResultLedger
}

func newRecordingComparisonAuditSink(
	recorder *metric.Recorder,
	next enginekafka.ComparisonAuditSink,
	capacity int,
) (*recordingComparisonAuditSink, error) {
	return newRecordingComparisonAuditSinkWithLogger(
		recorder, next, capacity, observability.Discard(observability.ComponentComparator),
	)
}

func newRecordingComparisonAuditSinkWithLogger(
	recorder *metric.Recorder,
	next enginekafka.ComparisonAuditSink,
	capacity int,
	logger *observability.Logger,
) (*recordingComparisonAuditSink, error) {
	if recorder == nil || next == nil {
		return nil, errors.New("comparator runtime: metric recorder and audit sink are required")
	}
	ledger, err := newComparisonResultLedger(capacity)
	if err != nil {
		return nil, err
	}
	return &recordingComparisonAuditSink{recorder: recorder, next: next, logger: logger, ledger: ledger}, nil
}

func (s *recordingComparisonAuditSink) WriteBatch(ctx context.Context, batch *contract.ComparisonAuditBatch) error {
	started := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.next.WriteBatch(ctx, batch); err != nil {
		return err
	}
	counts := map[metric.CompareResult]int{
		metric.CompareMatch:         0,
		metric.CompareMismatch:      0,
		metric.CompareMissingGo:     0,
		metric.CompareMissingPython: 0,
	}
	for _, audit := range batch.Audits {
		for _, result := range s.ledger.admit(audit) {
			s.recorder.RecordShadowCompare(metric.ComponentTrigger, result)
			counts[result]++
		}
	}
	s.logger.Info(
		observability.StageComparisonACK,
		observability.ResultBrokerACK,
		len(batch.Audits),
		time.Since(started),
		slog.Int("match", counts[metric.CompareMatch]),
		slog.Int("mismatch", counts[metric.CompareMismatch]),
		slog.Int("missing_go", counts[metric.CompareMissingGo]),
		slog.Int("missing_python", counts[metric.CompareMissingPython]),
	)
	return nil
}

var (
	errComparatorServiceStopped = errors.New("comparator runtime: Kafka service stopped unexpectedly")
	errComparatorHTTPStopped    = errors.New("comparator runtime: HTTP service stopped unexpectedly")
	errComparatorShutdown       = errors.New("comparator runtime: shutdown timeout")
)

func runComparatorApplication(ctx context.Context, configuration config.ComparatorConfig, recorder *metric.Recorder) error {
	return runComparatorApplicationWithLogger(
		ctx, configuration, recorder, observability.Discard(observability.ComponentComparator),
	)
}

func runComparatorApplicationWithLogger(
	ctx context.Context,
	configuration config.ComparatorConfig,
	recorder *metric.Recorder,
	eventLogger *observability.Logger,
) error {
	if ctx == nil {
		return errors.New("comparator runtime: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	if recorder == nil {
		return errors.New("comparator runtime: metric recorder is required")
	}
	if err := configuration.Validate(); err != nil {
		return err
	}
	if eventLogger == nil {
		eventLogger = observability.Discard(observability.ComponentComparator)
	}
	applicationStarted := time.Now()
	eventLogger.Info(observability.StageStartup, observability.ResultStarted, 0, 0)
	startupFailed := func(reason string) {
		eventLogger.Error(
			observability.StageStartup, observability.ResultFailed, 0, time.Since(applicationStarted),
			slog.String("reason", reason),
		)
	}

	sink, err := enginekafka.OpenComparisonAuditSink(configuration.Kafka.AuditSinkCoordinates())
	if err != nil {
		startupFailed("sink")
		return err
	}
	if err := ctx.Err(); err != nil {
		startupFailed("canceled")
		return shutdownComparatorSink(sink, time.Now().Add(configuration.ShutdownTimeout.Duration()))
	}
	recordingSink, err := newRecordingComparisonAuditSinkWithLogger(
		recorder, sink, configuration.Kafka.MaxEntries, eventLogger,
	)
	if err != nil {
		startupFailed("audit_sink")
		return errors.Join(err, shutdownComparatorSink(sink, time.Now().Add(configuration.ShutdownTimeout.Duration())))
	}
	service, err := enginekafka.OpenComparatorService(
		configuration.Kafka.ServiceCoordinates(),
		recordingSink,
		configuration.ShutdownTimeout.Duration(),
	)
	if err != nil {
		startupFailed("service")
		return errors.Join(err, shutdownComparatorSink(sink, time.Now().Add(configuration.ShutdownTimeout.Duration())))
	}
	if err := ctx.Err(); err != nil {
		startupFailed("canceled")
		deadline := time.Now().Add(configuration.ShutdownTimeout.Duration())
		return errors.Join(service.Close(), shutdownComparatorSink(sink, deadline))
	}
	server, err := httpservice.NewWithLifecycle(recorder, service)
	if err != nil {
		startupFailed("http")
		deadline := time.Now().Add(configuration.ShutdownTimeout.Duration())
		return errors.Join(err, service.Close(), shutdownComparatorSink(sink, deadline))
	}
	if err := ctx.Err(); err != nil {
		startupFailed("canceled")
		deadline := time.Now().Add(configuration.ShutdownTimeout.Duration())
		return errors.Join(service.Close(), shutdownComparatorSink(sink, deadline))
	}

	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	serviceDone := make(chan error, 1)
	httpDone := make(chan error, 1)
	go func() { serviceDone <- service.Run(runtimeContext) }()
	go func() {
		httpDone <- server.Run(runtimeContext, configuration.HTTP.Listen, configuration.ShutdownTimeout.Duration())
	}()

	var triggerErr, serviceErr, httpErr error
	serviceFinished, httpFinished := false, false
	serviceEarly, httpEarly := false, false
	select {
	case <-ctx.Done():
	case <-service.FatalSignal():
		triggerErr = service.FatalError()
		if triggerErr == nil {
			triggerErr = errors.New("comparator runtime: fatal signal has no error")
		}
		eventLogger.Error(
			observability.StageFatal, observability.ResultFailed, 0, time.Since(applicationStarted),
			slog.String("reason", "kafka"),
		)
	case serviceErr = <-serviceDone:
		serviceFinished = true
		serviceEarly = ctx.Err() == nil
		if serviceErr == nil && serviceEarly {
			serviceErr = errComparatorServiceStopped
		}
		if serviceEarly {
			eventLogger.Error(
				observability.StageFatal, observability.ResultFailed, 0, time.Since(applicationStarted),
				slog.String("reason", "kafka"),
			)
		}
	case httpErr = <-httpDone:
		httpFinished = true
		httpEarly = ctx.Err() == nil
		if httpErr == nil && httpEarly {
			httpErr = errComparatorHTTPStopped
		}
		if httpEarly {
			eventLogger.Error(
				observability.StageFatal, observability.ResultFailed, 0, time.Since(applicationStarted),
				slog.String("reason", "http"),
			)
		}
	}

	shutdownStarted := time.Now()
	deadline := time.Now().Add(configuration.ShutdownTimeout.Duration())
	cancelRuntime()
	if !serviceFinished {
		serviceErr = waitComparatorComponent(serviceDone, deadline)
	}
	sinkErr := shutdownComparatorSink(sink, deadline)
	if !httpFinished {
		httpErr = waitComparatorComponent(httpDone, deadline)
	}
	result := errors.Join(
		triggerErr,
		normalizeComparatorComponent(serviceErr, serviceEarly),
		normalizeComparatorComponent(httpErr, httpEarly),
		sinkErr,
	)
	shutdownResult := observability.ResultSuccess
	if errors.Is(result, errComparatorShutdown) || errors.Is(result, context.DeadlineExceeded) {
		shutdownResult = observability.ResultTimeout
	} else if result != nil {
		shutdownResult = observability.ResultFailed
	}
	if result == nil {
		eventLogger.Info(observability.StageShutdown, shutdownResult, 0, time.Since(shutdownStarted))
	} else {
		eventLogger.Error(observability.StageShutdown, shutdownResult, 0, time.Since(shutdownStarted))
	}
	return result
}

func shutdownComparatorSink(sink *enginekafka.ComparisonAuditKafkaSink, deadline time.Time) error {
	shutdownContext, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	return sink.Shutdown(shutdownContext)
}

func waitComparatorComponent(done <-chan error, deadline time.Time) error {
	wait := time.Until(deadline)
	if wait <= 0 {
		select {
		case err := <-done:
			return err
		default:
			return errComparatorShutdown
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		select {
		case err := <-done:
			return err
		default:
			return errComparatorShutdown
		}
	}
}

func normalizeComparatorComponent(err error, stoppedBeforeShutdown bool) error {
	if err == nil || (err == context.Canceled && !stoppedBeforeShutdown) {
		return nil
	}
	return fmt.Errorf("comparator runtime component: %w", err)
}
