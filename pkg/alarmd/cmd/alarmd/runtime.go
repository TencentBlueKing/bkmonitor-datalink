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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

var (
	ErrApplicationShutdownTimeout = errors.New("alarmd runtime: shutdown timeout")
	errKafkaServiceStopped        = errors.New("alarmd runtime: Kafka service stopped unexpectedly")
	errHTTPServiceStopped         = errors.New("alarmd runtime: HTTP service stopped unexpectedly")
	errFatalWithoutReason         = errors.New("alarmd runtime: fatal signal has no error")
)

type decisionSinkRuntime interface {
	trigger.DecisionSink
	Shutdown(context.Context) error
}

type serviceRuntime interface {
	lifecycle.Source
	Run(context.Context) error
	Close() error
	FatalSignal() <-chan struct{}
	FatalError() error
}

type httpRuntime interface {
	Run(context.Context, string, time.Duration) error
}

type applicationDependencies struct {
	logger      *observability.Logger
	openSink    func(config.KafkaConfig) (decisionSinkRuntime, error)
	openService func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error)
	newHTTP     func(*metric.Recorder, lifecycle.Source) (httpRuntime, error)
}

type recordingDecisionSink struct {
	recorder *metric.Recorder
	next     trigger.DecisionSink
	logger   *observability.Logger
}

type recordingProcessor struct {
	recorder *metric.Recorder
	next     consumer.Processor
}

func newRecordingDecisionSink(recorder *metric.Recorder, next trigger.DecisionSink) *recordingDecisionSink {
	return newRecordingDecisionSinkWithLogger(
		recorder, next, observability.Discard(observability.ComponentTrigger),
	)
}

func newRecordingDecisionSinkWithLogger(
	recorder *metric.Recorder,
	next trigger.DecisionSink,
	logger *observability.Logger,
) *recordingDecisionSink {
	return &recordingDecisionSink{recorder: recorder, next: next, logger: logger}
}

func (s *recordingDecisionSink) WriteBatch(ctx context.Context, batch *contract.TriggerDecisionBatch) error {
	started := time.Now()
	if err := s.next.WriteBatch(ctx, batch); err != nil {
		return err
	}
	s.recorder.RecordRecords(
		metric.StageTrigger,
		metric.ModeShadow,
		metric.DirectionOutput,
		metric.RecordTriggerDecision,
		float64(len(batch.Decisions)),
	)
	attrs := make([]slog.Attr, 0, 1)
	if batch.BatchID != "" {
		attrs = append(attrs, slog.String("batch_id", batch.BatchID))
	}
	s.logger.Info(
		observability.StageDecisionACK,
		observability.ResultBrokerACK,
		len(batch.Decisions),
		time.Since(started),
		attrs...,
	)
	return nil
}

func newRecordingProcessor(recorder *metric.Recorder, next consumer.Processor) *recordingProcessor {
	return &recordingProcessor{recorder: recorder, next: next}
}

func (p *recordingProcessor) Process(ctx context.Context, key, payload []byte) error {
	started := time.Now()
	err := p.next.Process(ctx, key, payload)
	if err != nil {
		p.recorder.RecordProcess(metric.StageTrigger, metric.ModeShadow, metric.StatusFailed, metric.ErrorInternal, time.Since(started))
		return err
	}
	p.recorder.RecordProcess(metric.StageTrigger, metric.ModeShadow, metric.StatusSuccess, metric.ErrorNone, time.Since(started))
	return nil
}

func runApplication(ctx context.Context, cfg config.Config, recorder *metric.Recorder, dependencies applicationDependencies) error {
	if ctx == nil {
		return errors.New("alarmd runtime: context is required")
	}
	if ctx.Err() != nil {
		return nil
	}
	if dependencies.openSink == nil {
		return errors.New("alarmd runtime: decision sink factory is required")
	}
	if dependencies.openService == nil {
		return errors.New("alarmd runtime: Kafka service factory is required")
	}
	if dependencies.newHTTP == nil {
		return errors.New("alarmd runtime: HTTP service factory is required")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	eventLogger := dependencies.logger
	if eventLogger == nil {
		eventLogger = observability.Discard(observability.ComponentTrigger)
	}
	applicationStarted := time.Now()
	eventLogger.Info(observability.StageStartup, observability.ResultStarted, 0, 0)
	startupFailed := func(reason string) {
		eventLogger.Error(
			observability.StageStartup, observability.ResultFailed, 0, time.Since(applicationStarted),
			slog.String("reason", reason),
		)
	}
	sink, err := dependencies.openSink(cfg.Kafka)
	if err != nil {
		startupFailed("sink")
		return err
	}
	if ctx.Err() != nil {
		startupFailed("canceled")
		return shutdownSinkBefore(sink, time.Now().Add(cfg.ShutdownTimeout.Duration()))
	}
	recordingSink := newRecordingDecisionSinkWithLogger(recorder, sink, eventLogger)
	service, err := dependencies.openService(
		cfg.Kafka,
		func() consumer.Processor {
			return newRecordingProcessor(recorder, detect.NewProcessor(recordingSink))
		},
		cfg.ShutdownTimeout.Duration(),
	)
	if err != nil {
		startupFailed("service")
		return errors.Join(err, shutdownSinkBefore(sink, time.Now().Add(cfg.ShutdownTimeout.Duration())))
	}
	if ctx.Err() != nil {
		startupFailed("canceled")
		deadline := time.Now().Add(cfg.ShutdownTimeout.Duration())
		return errors.Join(service.Close(), shutdownSinkBefore(sink, deadline))
	}
	server, err := dependencies.newHTTP(recorder, service)
	if err != nil {
		startupFailed("http")
		deadline := time.Now().Add(cfg.ShutdownTimeout.Duration())
		return errors.Join(err, service.Close(), shutdownSinkBefore(sink, deadline))
	}
	if ctx.Err() != nil {
		startupFailed("canceled")
		deadline := time.Now().Add(cfg.ShutdownTimeout.Duration())
		return errors.Join(service.Close(), shutdownSinkBefore(sink, deadline))
	}

	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	serviceDone := make(chan error, 1)
	httpDone := make(chan error, 1)
	go func() { serviceDone <- service.Run(runtimeContext) }()
	go func() { httpDone <- server.Run(runtimeContext, cfg.HTTP.Listen, cfg.ShutdownTimeout.Duration()) }()

	var triggerErr, serviceErr, httpErr error
	serviceFinished := false
	httpFinished := false
	serviceStoppedBeforeShutdown := false
	httpStoppedBeforeShutdown := false
	select {
	case <-ctx.Done():
	case <-service.FatalSignal():
		triggerErr = service.FatalError()
		if triggerErr == nil {
			triggerErr = errFatalWithoutReason
		}
		eventLogger.Error(
			observability.StageFatal, observability.ResultFailed, 0, time.Since(applicationStarted),
			slog.String("reason", "kafka"),
		)
	case serviceErr = <-serviceDone:
		serviceFinished = true
		serviceStoppedBeforeShutdown = ctx.Err() == nil
		if serviceErr == nil && serviceStoppedBeforeShutdown {
			serviceErr = errKafkaServiceStopped
		}
		if serviceStoppedBeforeShutdown {
			eventLogger.Error(
				observability.StageFatal, observability.ResultFailed, 0, time.Since(applicationStarted),
				slog.String("reason", "kafka"),
			)
		}
	case httpErr = <-httpDone:
		httpFinished = true
		httpStoppedBeforeShutdown = ctx.Err() == nil
		if httpErr == nil && httpStoppedBeforeShutdown {
			httpErr = errHTTPServiceStopped
		}
		if httpStoppedBeforeShutdown {
			eventLogger.Error(
				observability.StageFatal, observability.ResultFailed, 0, time.Since(applicationStarted),
				slog.String("reason", "http"),
			)
		}
	}

	shutdownStarted := time.Now()
	deadline := time.Now().Add(cfg.ShutdownTimeout.Duration())
	cancelRuntime()
	if !serviceFinished {
		serviceErr = waitRuntimeComponent(serviceDone, deadline)
	}
	sinkErr := shutdownSinkBefore(sink, deadline)
	if !httpFinished {
		httpErr = waitRuntimeComponent(httpDone, deadline)
	}
	result := errors.Join(
		triggerErr,
		normalizeRuntimeShutdownError(serviceErr, serviceStoppedBeforeShutdown),
		normalizeRuntimeShutdownError(httpErr, httpStoppedBeforeShutdown),
		sinkErr,
	)
	shutdownResult := observability.ResultSuccess
	if errors.Is(result, ErrApplicationShutdownTimeout) || errors.Is(result, context.DeadlineExceeded) {
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

func shutdownSinkBefore(sink decisionSinkRuntime, deadline time.Time) error {
	shutdownContext, cancelShutdown := context.WithDeadline(context.Background(), deadline)
	defer cancelShutdown()
	return sink.Shutdown(shutdownContext)
}

func waitRuntimeComponent(done <-chan error, deadline time.Time) error {
	wait := time.Until(deadline)
	if wait <= 0 {
		select {
		case err := <-done:
			return err
		default:
			return ErrApplicationShutdownTimeout
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
			return ErrApplicationShutdownTimeout
		}
	}
}

func normalizeRuntimeShutdownError(err error, stoppedBeforeShutdown bool) error {
	if err == nil || (err == context.Canceled && !stoppedBeforeShutdown) {
		return nil
	}
	return fmt.Errorf("runtime component: %w", err)
}
