// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutableExitCodes(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "bkmonitor-operator")
	build := exec.Command(
		"go",
		"build",
		"-ldflags",
		"-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=ignore",
		"-o",
		binary,
		".",
	)
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))

	t.Run("help succeeds", func(t *testing.T) {
		output, err := exec.Command(binary, "--help").CombinedOutput()
		require.NoError(t, err, string(output))
	})

	for name, arguments := range map[string][]string{
		"unknown command": {"definitely-unknown"},
		"invalid state": {
			"pod-terminating-state-init",
			"--namespace=test",
			"--state-max-bytes=900001",
		},
	} {
		t.Run(name, func(t *testing.T) {
			output, err := exec.Command(binary, arguments...).CombinedOutput()
			var exitError *exec.ExitError
			require.True(t, errors.As(err, &exitError), string(output))
			require.NotZero(t, exitError.ExitCode(), string(output))
		})
	}
}
