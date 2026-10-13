// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The drain deadline is what fits in the grace the chart passes: two of it
// and the stop's overhead within the grace. Without a grace it is the
// default; a grace that is not a whole positive number of seconds, or that
// leaves nothing to drain in, is refused; and the deadline is not a values
// key any more.
func TestTheShutdownTimeoutIsDerivedFromTheGraceTheChartPasses(t *testing.T) {
	load := func(t *testing.T, contents string) (Config, error) {
		t.Helper()
		return LoadWithRoles(writeConfig(t, validGoAccessRuntimeConfigYAML("grace-worker")+contents), nil)
	}
	for grace, want := range map[string]time.Duration{"30": 13500 * time.Millisecond, "5": time.Second, " 60 ": 28500 * time.Millisecond} {
		t.Setenv(TerminationGraceEnvironment, grace)
		cfg, err := load(t, "")
		if err != nil || cfg.ShutdownTimeout.Duration() != want {
			t.Fatalf("grace %q: shutdown timeout = %s, %v; want %s", grace, cfg.ShutdownTimeout.Duration(), err, want)
		}
		if stop := 2*cfg.ShutdownTimeout.Duration() + stopOverhead; stop > 60*time.Second || (grace == "30" && stop != 30*time.Second) {
			t.Fatalf("grace %q: a stop takes %s", grace, stop)
		}
	}
	for _, grace := range []string{"0", "-5", "3", "thirty", "1.5"} {
		t.Setenv(TerminationGraceEnvironment, grace)
		if _, err := load(t, ""); err == nil || !strings.Contains(err.Error(), TerminationGraceEnvironment) {
			t.Fatalf("grace %q: error = %v, want it refused by the variable's name", grace, err)
		}
	}
	t.Run("no grace passed", func(t *testing.T) {
		t.Setenv(TerminationGraceEnvironment, "")
		if err := os.Unsetenv(TerminationGraceEnvironment); err != nil {
			t.Fatal(err)
		}
		cfg, err := load(t, "")
		if err != nil || cfg.ShutdownTimeout.Duration() != DefaultShutdownTimeout {
			t.Fatalf("no grace: shutdown timeout = %s, %v; want %s", cfg.ShutdownTimeout.Duration(), err, DefaultShutdownTimeout)
		}
	})
	if _, err := load(t, "shutdown_timeout: 10s\n"); err == nil || !strings.Contains(err.Error(), "shutdown_timeout") {
		t.Fatalf("shutdown_timeout in a values file: error = %v, want it refused by name", err)
	}
}
