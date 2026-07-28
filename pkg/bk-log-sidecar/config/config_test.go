// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package config

import (
	"flag"
	"reflect"
	"testing"
	"time"
)

func TestPeriodicReconcileFlags(t *testing.T) {
	originalFlagSet := flag.CommandLine
	originalBkEnvs := append([]string(nil), BkEnvs...)
	originalInterval := PeriodicReconcileInterval
	originalJitter := PeriodicReconcileJitter
	t.Cleanup(func() {
		flag.CommandLine = originalFlagSet
		BkEnvs = originalBkEnvs
		PeriodicReconcileInterval = originalInterval
		PeriodicReconcileJitter = originalJitter
	})

	flag.CommandLine = flag.NewFlagSet("periodic-reconcile-test", flag.ContinueOnError)
	FlagInit()
	if PeriodicReconcileInterval != DefaultPeriodicReconcileInterval {
		t.Fatalf("unexpected default interval: %s", PeriodicReconcileInterval)
	}
	if PeriodicReconcileJitter != DefaultPeriodicReconcileJitter {
		t.Fatalf("unexpected default jitter: %v", PeriodicReconcileJitter)
	}
	if err := flag.CommandLine.Parse([]string{
		"--periodic-reconcile-interval=2m",
		"--periodic-reconcile-jitter=0.35",
	}); err != nil {
		t.Fatalf("parse periodic reconcile flags: %v", err)
	}

	if PeriodicReconcileInterval != 2*time.Minute {
		t.Fatalf("unexpected interval: %s", PeriodicReconcileInterval)
	}
	if PeriodicReconcileJitter != 0.35 {
		t.Fatalf("unexpected jitter: %v", PeriodicReconcileJitter)
	}
}

func TestBkEnvFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "default matches empty environment",
			expected: []string{""},
		},
		{
			name:     "single environment remains compatible",
			args:     []string{"--bk-env=bkop"},
			expected: []string{"bkop"},
		},
		{
			name:     "multiple environments preserve explicit empty value",
			args:     []string{"--bk-env=bkop", "--bk-env="},
			expected: []string{"bkop", ""},
		},
		{
			name:     "duplicate environments are ignored",
			args:     []string{"--bk-env=bkop", "--bk-env=bkop", "--bk-env=prod"},
			expected: []string{"bkop", "prod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalFlagSet := flag.CommandLine
			originalBkEnvs := append([]string(nil), BkEnvs...)
			t.Cleanup(func() {
				flag.CommandLine = originalFlagSet
				BkEnvs = originalBkEnvs
			})

			flag.CommandLine = flag.NewFlagSet(tt.name, flag.ContinueOnError)
			FlagInit()
			if err := flag.CommandLine.Parse(tt.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}
			if !reflect.DeepEqual(BkEnvs, tt.expected) {
				t.Fatalf("unexpected bk envs: got %q, want %q", BkEnvs, tt.expected)
			}
		})
	}
}
