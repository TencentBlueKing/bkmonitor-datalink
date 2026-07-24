// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"flag"
	"testing"
	"time"
)

func TestPeriodicReconcileFlags(t *testing.T) {
	originalFlagSet := flag.CommandLine
	originalInterval := PeriodicReconcileInterval
	originalJitter := PeriodicReconcileJitter
	t.Cleanup(func() {
		flag.CommandLine = originalFlagSet
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
