// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controllers

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func TestApplyLogsChangedFilesAndPendingReloadRecovery(t *testing.T) {
	reloadErr := errors.New("reload temporarily unavailable")
	var reloadCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	observed := observeSidecarLogs(sidecar)
	sidecar.reloadAgentFn = func() error {
		if reloadCalls.Add(1) == 1 {
			return reloadErr
		}
		return nil
	}
	desired, err := renderDesiredConfigs([]define.LogConfigType{&stubLogConfig{
		name:    "container-1_std_default_config",
		content: []byte("config"),
	}})
	require.NoError(t, err)

	firstErr := sidecar.applyDesiredConfigs(desired)

	require.ErrorIs(t, firstErr, reloadErr)
	failureContext := requireObservedLogContext(
		t,
		observed,
		"configuration files applied but agent reload failed",
	)
	assert.Equal(t, string(convergenceTriggerDirect), failureContext["trigger"])
	assert.Equal(t, convergenceResultFailure, failureContext["result"])
	assert.EqualValues(t, 1, failureContext["writtenConfigCount"])
	assert.EqualValues(t, 0, failureContext["deletedConfigCount"])
	assert.Equal(t, true, failureContext["reloadPending"])

	require.NoError(t, sidecar.applyDesiredConfigs(desired))
	successContext := requireObservedLogContext(t, observed, "configuration apply completed")
	assert.Equal(t, string(convergenceTriggerDirect), successContext["trigger"])
	assert.Equal(t, convergenceResultSuccess, successContext["result"])
	assert.EqualValues(t, 0, successContext["writtenConfigCount"])
	assert.EqualValues(t, 0, successContext["deletedConfigCount"])
	assert.Equal(t, true, successContext["reloadPendingBeforeApply"])
	assert.Equal(t, int32(2), reloadCalls.Load())
}

func TestRuntimeConvergenceLogsRetryRecovery(t *testing.T) {
	reloadErr := errors.New("startup reload temporarily unavailable")
	var reloadCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	observed := observeSidecarLogs(sidecar)
	sidecar.convergenceRetryBaseDelay = time.Millisecond
	sidecar.convergenceRetryMaxDelay = time.Millisecond
	sidecar.reloadAgentFn = func() error {
		if reloadCalls.Add(1) == 1 {
			return reloadErr
		}
		return nil
	}

	err := sidecar.convergeRuntimeSubscription(
		context.Background(),
		make(chan error),
		true,
	)

	require.NoError(t, err)
	failureContext := requireObservedLogContext(
		t,
		observed,
		"runtime configuration convergence failed, retrying",
	)
	assert.Equal(t, string(convergenceTriggerStartup), failureContext["trigger"])
	assert.Equal(t, convergenceResultFailure, failureContext["result"])
	assert.Equal(t, "convergence", failureContext["stage"])
	assert.EqualValues(t, 1, failureContext["convergenceAttempt"])
	assert.Equal(t, time.Millisecond.String(), failureContext["retryAfter"])

	successContext := requireObservedLogContext(
		t,
		observed,
		"runtime configuration convergence succeeded",
	)
	assert.Equal(t, string(convergenceTriggerStartup), successContext["trigger"])
	assert.Equal(t, convergenceResultSuccess, successContext["result"])
	assert.Equal(t, "convergence", successContext["stage"])
	assert.EqualValues(t, 2, successContext["convergenceAttempt"])
	assert.Equal(t, true, successContext["convergenceRecovered"])
}

func TestRuntimeSubscriptionLogsRecoveryAfterSubscribeRetry(t *testing.T) {
	subscribeErr := errors.New("runtime stream temporarily unavailable")
	var subscribeCalls atomic.Int32
	runtime := &stubRuntime{
		subscribeFn: func(context.Context) (<-chan *define.ContainerEvent, <-chan error, error) {
			if subscribeCalls.Add(1) == 1 {
				return nil, nil, subscribeErr
			}
			return make(chan *define.ContainerEvent), make(chan error), nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	observed := observeSidecarLogs(sidecar)
	sidecar.subscribeRetryInterval = time.Millisecond
	sidecar.subscriptionStabilityWindow = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		sidecar.subscribeEvent(ctx, ready)
	}()
	waitForSignal(t, ready, "fallback startup convergence")
	// 第二次 Subscribe 调用发生在稳定窗口之前；必须等恢复收敛真正完成后再取消，
	// 否则测试可能自行截断 established/convergence 日志并产生偶发失败。
	require.Eventually(t, func() bool {
		return observed.FilterMessage("runtime configuration convergence succeeded").Len() > 0
	}, 2*time.Second, 5*time.Millisecond)
	require.GreaterOrEqual(t, subscribeCalls.Load(), int32(2))
	cancel()
	waitForSignal(t, done, "runtime subscription observability test shutdown")

	failureContext := requireObservedLogContext(
		t,
		observed,
		"runtime event subscription start failed, retrying",
	)
	assert.Equal(t, string(convergenceTriggerStartup), failureContext["trigger"])
	assert.Equal(t, convergenceResultFailure, failureContext["result"])
	assert.Equal(t, "subscribe", failureContext["stage"])
	assert.EqualValues(t, 1, failureContext["subscriptionAttempt"])

	establishedContext := requireObservedLogContext(
		t,
		observed,
		"runtime event subscription established",
	)
	assert.Equal(t, string(convergenceTriggerRuntimeReconnect), establishedContext["trigger"])
	assert.Equal(t, convergenceResultSuccess, establishedContext["result"])
	assert.Equal(t, "subscribe", establishedContext["stage"])
	assert.EqualValues(t, 2, establishedContext["subscriptionAttempt"])
	assert.Equal(t, true, establishedContext["subscriptionRecovered"])

	convergenceContext := requireObservedLogContext(
		t,
		observed,
		"runtime configuration convergence succeeded",
	)
	assert.Equal(t, "convergence", convergenceContext["stage"])
	assert.EqualValues(t, 1, convergenceContext["convergenceAttempt"])
	assert.Equal(t, false, convergenceContext["convergenceRecovered"])
}

func TestPeriodicReconcileLogsRecoveryAfterTransientFailure(t *testing.T) {
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	listErr := errors.New("BkLogConfig cache temporarily unavailable")
	var listCalls atomic.Int32
	reader := &stubReader{
		getFn: func(context.Context, client.ObjectKey, client.Object) error {
			return nil
		},
		listFn: func(context.Context, client.ObjectList) error {
			if listCalls.Add(1) == 1 {
				return listErr
			}
			return nil
		},
	}
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, reader)
	observed := observeSidecarLogs(sidecar)
	sidecar.periodicReconcileInterval = time.Millisecond
	sidecar.periodicReconcileDelayFn = func(interval time.Duration, _ float64) time.Duration {
		return interval
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sidecar.periodicReconcile(ctx)
	}()
	require.Eventually(t, func() bool {
		return observed.FilterMessage(
			"periodic node configuration reconciliation recovered",
		).Len() == 1
	}, 2*time.Second, 5*time.Millisecond)
	cancel()
	waitForSignal(t, done, "periodic reconciliation observability test shutdown")

	failureContext := requireObservedLogContext(
		t,
		observed,
		"periodic node configuration reconciliation failed",
	)
	assert.Equal(t, string(convergenceTriggerPeriodicReconcile), failureContext["trigger"])
	assert.Equal(t, convergenceResultFailure, failureContext["result"])
	assert.EqualValues(t, 1, failureContext["attempt"])

	recoveryContext := requireObservedLogContext(
		t,
		observed,
		"periodic node configuration reconciliation recovered",
	)
	assert.Equal(t, string(convergenceTriggerPeriodicReconcile), recoveryContext["trigger"])
	assert.Equal(t, convergenceResultSuccess, recoveryContext["result"])
	assert.EqualValues(t, 1, recoveryContext["failedAttempts"])
}

func TestPeriodicReconcileSteadyStateSuppressesVerboseLogsWithDefaultDevelopmentLogger(t *testing.T) {
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	var listCalls atomic.Int32
	reader := &stubReader{
		getFn: func(context.Context, client.ObjectKey, client.Object) error {
			return nil
		},
		listFn: func(context.Context, client.ObjectList) error {
			listCalls.Add(1)
			return nil
		},
	}
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, reader)
	var output bytes.Buffer
	options := ctrlzap.Options{Development: true}
	sidecar.log = ctrlzap.New(
		ctrlzap.UseFlagOptions(&options),
		ctrlzap.WriteTo(&output),
	)
	var delayCalls atomic.Int32
	sidecar.periodicReconcileDelayFn = func(time.Duration, float64) time.Duration {
		if delayCalls.Add(1) == 1 {
			return time.Millisecond
		}
		return time.Hour
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sidecar.periodicReconcile(ctx)
	}()
	require.Eventually(t, func() bool {
		return listCalls.Load() == 1
	}, 2*time.Second, 5*time.Millisecond)
	cancel()
	waitForSignal(t, done, "steady periodic reconciliation test shutdown")

	logOutput := output.String()
	assert.Contains(t, logOutput, "BkLogConfig environment filtering completed")
	assert.NotContains(t, logOutput, "periodic node configuration reconciliation succeeded")
	assert.NotContains(t, logOutput, "configuration apply skipped because desired state is unchanged")
	assert.NotContains(t, logOutput, "current node info")
	assert.NotContains(t, logOutput, "not have log config")
}

func TestBkLogConfigListLogsEnvironmentFilterSummary(t *testing.T) {
	originalBkEnvs := append([]string(nil), config.BkEnvs...)
	config.BkEnvs = []string{"bkop", ""}
	t.Cleanup(func() {
		config.BkEnvs = originalBkEnvs
	})

	reader := &stubReader{
		listFn: func(_ context.Context, list client.ObjectList) error {
			bkLogConfigList := list.(*bluekingv1alpha1.BkLogConfigList)
			bkLogConfigList.Items = []bluekingv1alpha1.BkLogConfig{
				{ObjectMeta: metav1.ObjectMeta{
					Name:   "bkop-config",
					Labels: map[string]string{config.BkEnvLabelName: "bkop"},
				}},
				{ObjectMeta: metav1.ObjectMeta{Name: "default-config"}},
				{ObjectMeta: metav1.ObjectMeta{
					Name:   "prod-config",
					Labels: map[string]string{config.BkEnvLabelName: "prod"},
				}},
			}
			return nil
		},
	}
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, reader)
	observed := observeSidecarLogs(sidecar)

	configs, err := sidecar.bkLogConfigList(context.Background())

	require.NoError(t, err)
	require.Len(t, configs, 2)
	logContext := requireObservedLogContext(t, observed, "BkLogConfig environment filtering completed")
	assert.Equal(t, []interface{}{"bkop", ""}, logContext["allowedBkEnvs"])
	assert.EqualValues(t, 3, logContext["total"])
	assert.EqualValues(t, 2, logContext["matched"])
	assert.EqualValues(t, 1, logContext["ignored"])
}

func observeSidecarLogs(sidecar *BkLogSidecar) *observer.ObservedLogs {
	core, observed := observer.New(zapcore.DebugLevel)
	sidecar.log = zapr.NewLogger(zap.New(core))
	return observed
}

func requireObservedLogContext(
	t *testing.T,
	observed *observer.ObservedLogs,
	message string,
) map[string]interface{} {
	t.Helper()
	entries := observed.FilterMessage(message).All()
	require.NotEmpty(t, entries, "expected log message %q", message)
	return entries[len(entries)-1].ContextMap()
}
