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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/metric"
	httpservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/service/http"
)

type recordingComparisonAuditSink struct {
	recorder *metric.Recorder
	next     enginekafka.ComparisonAuditSink
}

func (s *recordingComparisonAuditSink) WriteBatch(ctx context.Context, batch *contract.ComparisonAuditBatch) error {
	if err := s.next.WriteBatch(ctx, batch); err != nil {
		return err
	}
	for _, audit := range batch.Audits {
		switch audit.Verdict {
		case contract.ComparisonVerdictMatch:
			s.recorder.RecordShadowCompare(metric.ComponentTrigger, metric.CompareMatch)
		case contract.ComparisonVerdictHardDiff:
			s.recorder.RecordShadowCompare(metric.ComponentTrigger, metric.CompareMismatch)
		default:
			if audit.Coverage.Phase != contract.ComparisonCoverageMissingAtBarrier {
				continue
			}
			for _, role := range audit.Coverage.MissingAtBarrierRoles {
				switch role {
				case contract.ComparisonRoleGo:
					s.recorder.RecordShadowCompare(metric.ComponentTrigger, metric.CompareMissingGo)
				case contract.ComparisonRolePython:
					s.recorder.RecordShadowCompare(metric.ComponentTrigger, metric.CompareMissingPython)
				}
			}
		}
	}
	return nil
}

var (
	errComparatorServiceStopped = errors.New("comparator runtime: Kafka service stopped unexpectedly")
	errComparatorHTTPStopped    = errors.New("comparator runtime: HTTP service stopped unexpectedly")
	errComparatorShutdown       = errors.New("comparator runtime: shutdown timeout")
)

func runComparatorApplication(ctx context.Context, configuration config.ComparatorConfig, recorder *metric.Recorder) error {
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

	sink, err := enginekafka.OpenComparisonAuditSink(configuration.Kafka.AuditSinkCoordinates())
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return shutdownComparatorSink(sink, time.Now().Add(configuration.ShutdownTimeout.Duration()))
	}
	service, err := enginekafka.OpenComparatorService(
		configuration.Kafka.ServiceCoordinates(),
		&recordingComparisonAuditSink{recorder: recorder, next: sink},
		configuration.ShutdownTimeout.Duration(),
	)
	if err != nil {
		return errors.Join(err, shutdownComparatorSink(sink, time.Now().Add(configuration.ShutdownTimeout.Duration())))
	}
	if err := ctx.Err(); err != nil {
		deadline := time.Now().Add(configuration.ShutdownTimeout.Duration())
		return errors.Join(service.Close(), shutdownComparatorSink(sink, deadline))
	}
	server, err := httpservice.NewWithLifecycle(recorder, service)
	if err != nil {
		deadline := time.Now().Add(configuration.ShutdownTimeout.Duration())
		return errors.Join(err, service.Close(), shutdownComparatorSink(sink, deadline))
	}
	if err := ctx.Err(); err != nil {
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
	case serviceErr = <-serviceDone:
		serviceFinished = true
		serviceEarly = ctx.Err() == nil
		if serviceErr == nil && serviceEarly {
			serviceErr = errComparatorServiceStopped
		}
	case httpErr = <-httpDone:
		httpFinished = true
		httpEarly = ctx.Err() == nil
		if httpErr == nil && httpEarly {
			httpErr = errComparatorHTTPStopped
		}
	}

	deadline := time.Now().Add(configuration.ShutdownTimeout.Duration())
	cancelRuntime()
	if !serviceFinished {
		serviceErr = waitComparatorComponent(serviceDone, deadline)
	}
	sinkErr := shutdownComparatorSink(sink, deadline)
	if !httpFinished {
		httpErr = waitComparatorComponent(httpDone, deadline)
	}
	return errors.Join(
		triggerErr,
		normalizeComparatorComponent(serviceErr, serviceEarly),
		normalizeComparatorComponent(httpErr, httpEarly),
		sinkErr,
	)
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
