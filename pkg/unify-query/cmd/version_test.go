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
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

func captureVersionOutput(t *testing.T, detail bool) string {
	t.Helper()
	require.NoError(t, versionCmd.Flags().Set("detail", "false"))
	versionCmd.Flags().Lookup("detail").Changed = false
	if detail {
		require.NoError(t, versionCmd.Flags().Set("detail", "true"))
	}
	t.Cleanup(func() {
		_ = versionCmd.Flags().Set("detail", "false")
		versionCmd.Flags().Lookup("detail").Changed = false
	})

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	versionCmd.Run(versionCmd, nil)
	os.Stdout = stdout
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestVersionPlainOutputIsTheVersionOnly(t *testing.T) {
	version, tag := config.Version, config.GitTag
	t.Cleanup(func() { config.Version, config.GitTag = version, tag })
	config.Version, config.GitTag = "3.9.1", "v3.9.1"

	require.Equal(t, "3.9.1\n", captureVersionOutput(t, false))
}

func TestVersionDetailKeepsTheVersionAsFirstLine(t *testing.T) {
	version, commit, tag := config.Version, config.CommitHash, config.GitTag
	t.Cleanup(func() { config.Version, config.CommitHash, config.GitTag = version, commit, tag })
	config.Version, config.CommitHash, config.GitTag = "3.9.1", "abc123", "v3.9.1"

	lines := strings.Split(strings.TrimSuffix(captureVersionOutput(t, true), "\n"), "\n")
	require.Equal(t, "3.9.1", lines[0])
	require.Contains(t, lines, "CommitId: abc123")
	require.Contains(t, lines, "Tag: v3.9.1")
	require.Contains(t, lines, "Release: ")
}
