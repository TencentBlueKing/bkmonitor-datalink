// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/podterminatingreporter"
)

func TestPodTerminatingReporterCommandHelpAndFlags(t *testing.T) {
	var output bytes.Buffer
	runCalled := false
	command := newPodTerminatingReporterCommand(func(_ context.Context, _ podterminatingreporter.ReporterOptions) error {
		runCalled = true
		return nil
	})
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})
	require.NoError(t, command.Execute())
	require.False(t, runCalled)
	for _, flag := range []string{
		"--listen-address",
		"--namespace",
		"--state-configmap-name",
		"--refresh-interval",
		"--page-limit",
		"--request-timeout",
		"--recovery-hold",
		"--stale-after",
		"--state-max-bytes",
	} {
		require.Contains(t, output.String(), flag)
	}
	require.NotContains(t, output.String(), "--recovery-threshold")

	var got podterminatingreporter.ReporterOptions
	command = newPodTerminatingReporterCommand(func(_ context.Context, options podterminatingreporter.ReporterOptions) error {
		got = options
		return nil
	})
	command.SetArgs([]string{
		"--listen-address=:9111",
		"--namespace=test-ns",
		"--state-configmap-name=test-state",
		"--refresh-interval=30s",
		"--page-limit=123",
		"--request-timeout=5s",
		"--recovery-hold=5m",
		"--stale-after=2m",
		"--state-max-bytes=800000",
	})
	require.NoError(t, command.Execute())
	require.Equal(t, podterminatingreporter.ReporterOptions{
		ListenAddress:      ":9111",
		Namespace:          "test-ns",
		StateConfigMapName: "test-state",
		RefreshInterval:    30 * time.Second,
		PageLimit:          123,
		RequestTimeout:     5 * time.Second,
		RecoveryHold:       5 * time.Minute,
		StaleAfter:         2 * time.Minute,
		StateMaxBytes:      800000,
	}, got)
}

func TestPodTerminatingReporterCommandRejectsInvalidFlagsBeforeRun(t *testing.T) {
	runCalled := false
	command := newPodTerminatingReporterCommand(func(_ context.Context, _ podterminatingreporter.ReporterOptions) error {
		runCalled = true
		return nil
	})
	command.SilenceUsage = true
	command.SetArgs([]string{"--namespace=test-ns", "--state-max-bytes=900001"})

	err := command.Execute()

	require.ErrorContains(t, err, "hard limit")
	require.False(t, runCalled)
}

func TestPodTerminatingStateInitCommandHelpFlagsAndValidation(t *testing.T) {
	var output bytes.Buffer
	runCalled := false
	command := newPodTerminatingStateInitCommand(func(_ context.Context, _ podterminatingreporter.StateInitOptions) error {
		runCalled = true
		return nil
	})
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})
	require.NoError(t, command.Execute())
	require.False(t, runCalled)
	for _, flag := range []string{
		"--namespace",
		"--state-configmap-name",
		"--request-timeout",
		"--state-max-bytes",
	} {
		require.Contains(t, output.String(), flag)
	}

	var got podterminatingreporter.StateInitOptions
	command = newPodTerminatingStateInitCommand(func(_ context.Context, options podterminatingreporter.StateInitOptions) error {
		got = options
		return nil
	})
	command.SetArgs([]string{
		"--namespace=test-ns",
		"--state-configmap-name=test-state",
		"--request-timeout=5s",
		"--state-max-bytes=800000",
	})
	require.NoError(t, command.Execute())
	require.Equal(t, podterminatingreporter.StateInitOptions{
		Namespace:          "test-ns",
		StateConfigMapName: "test-state",
		RequestTimeout:     5 * time.Second,
		StateMaxBytes:      800000,
	}, got)

	runCalled = false
	command = newPodTerminatingStateInitCommand(func(_ context.Context, _ podterminatingreporter.StateInitOptions) error {
		runCalled = true
		return nil
	})
	command.SilenceUsage = true
	command.SetArgs([]string{"--namespace=test-ns", "--request-timeout=0s"})
	err := command.Execute()
	require.ErrorContains(t, err, "request timeout")
	require.False(t, runCalled)

	expected := errors.New("unsupported persisted state")
	command = newPodTerminatingStateInitCommand(func(_ context.Context, _ podterminatingreporter.StateInitOptions) error {
		return expected
	})
	command.SilenceUsage = true
	command.SetArgs([]string{"--namespace=test-ns"})
	require.ErrorIs(t, command.Execute(), expected)
}
