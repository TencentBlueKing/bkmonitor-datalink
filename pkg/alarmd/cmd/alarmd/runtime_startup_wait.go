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
	"io"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const (
	startupWaitInitial = time.Second
	startupWaitCeiling = 15 * time.Second
)

// startupWaiter holds startup at one dependency that does not answer, instead
// of ending the process. Ending it handed the wait to the kubelet, whose
// restart backoff grows to five minutes: a Redis that was loading for twenty
// seconds kept the replica down for minutes after it answered again. Waiting
// here keeps the replica alive and not ready (the HTTP server already runs),
// so a rollout stops on it exactly as it did on the crash, and it joins
// within one backoff of the dependency answering.
//
// Only a dependency that is not answering is waited on. One that answers
// with a refusal - a wrong password, a server that cannot run the fence - is
// the deployment's to fix, and startup still ends on it by name.
type startupWaiter struct {
	initial, ceiling time.Duration
	recorder         *metric.Recorder
	logger           *observability.Logger
	health           *phaseTwoApplicationHealth
}

func newStartupWaiter(recorder *metric.Recorder, logger *observability.Logger, health *phaseTwoApplicationHealth) startupWaiter {
	return startupWaiter{initial: startupWaitInitial, ceiling: startupWaitCeiling, recorder: recorder, logger: logger, health: health}
}

// await runs attempt until it succeeds, fails with a refusal, or ctx ends.
func (waiter startupWaiter) await(ctx context.Context, dependency string, attempt func(context.Context) error) error {
	delay := waiter.initial
	if delay <= 0 {
		delay = startupWaitInitial
	}
	ceiling := waiter.ceiling
	if ceiling < delay {
		ceiling = delay
	}
	for waited := 0; ; waited++ {
		err := attempt(ctx)
		if err == nil {
			if waited > 0 && waiter.logger != nil {
				waiter.logger.Info(observability.StageStartup, "dependency_answered", waited, 0,
					slog.String("dependency", dependency))
			}
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("startup stopped while waiting on %s: %w", dependency, errors.Join(ctx.Err(), err))
		}
		if !startupDependencyTransient(err) {
			return err
		}
		waiter.recorder.RecordStartupDependencyWait(dependency)
		if waiter.logger != nil {
			waiter.logger.Error(observability.StageStartup, "dependency_waiting", waited+1, 0,
				slog.String("dependency", dependency), slog.Duration("retry_in", delay), slog.String("error", err.Error()))
		}
		if waiter.health != nil {
			waiter.health.Update(phaseTwoReadiness{
				State: observability.HealthStarting, Reasons: []observability.ReasonCode{observability.ReasonCode(contract.ReasonRedisUnavailable)},
			})
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("startup stopped while waiting on %s: %w", dependency, errors.Join(ctx.Err(), err))
		case <-timer.C:
		}
		if delay *= 2; delay > ceiling {
			delay = ceiling
		}
	}
}

// openRedis opens a client and waits in place until it answers. The client
// is made once and asked again: a Sentinel client rediscovers its master on
// each new connection, so a failover in progress resolves without a new one.
func (waiter startupWaiter) openRedis(
	ctx context.Context, dependency string, connection config.RedisConnectionConfig, hook *metric.RedisCallHook,
) (redis.UniversalClient, error) {
	client := redis.NewUniversalClient(productionRedisOptions(connection))
	if hook != nil {
		client.AddHook(hook)
	}
	if err := waiter.await(ctx, dependency, func(ctx context.Context) error { return client.Ping(ctx).Err() }); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// startupTransientReplies are the server replies that mean "not now" rather
// than "no": the dataset is loading, a replica lost its master, a cluster
// slot is moving or down, a Sentinel failover left this connection on the
// demoted master, or a script is running. Any other reply is a refusal.
var startupTransientReplies = []string{"LOADING", "MASTERDOWN", "TRYAGAIN", "CLUSTERDOWN", "READONLY", "BUSY"}

// startupDependencyTransient reports whether a failed startup attempt is a
// dependency not answering, which is waited on, rather than one refusing,
// which ends startup.
func startupDependencyTransient(err error) bool {
	if err == nil {
		return false
	}
	var reply redis.Error
	if errors.As(err, &reply) {
		message := reply.Error()
		for _, prefix := range startupTransientReplies {
			if message == prefix || strings.HasPrefix(message, prefix+" ") {
				return true
			}
		}
		return false
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	// go-redis reports these two as plain strings.
	message := err.Error()
	return strings.Contains(message, "all sentinels specified in configuration are unreachable") ||
		strings.Contains(message, "connection pool timeout")
}
