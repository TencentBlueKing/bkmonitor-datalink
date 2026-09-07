// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestPhaseTwoRuntimeProfileUsesEffectiveCapacityAndExcludesSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 17
	cfg.PhaseTwo.Scheduler.ReadyQueueCapacity = 3
	want, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	if want.Capacity.ActiveExecutions != 3 {
		t.Fatalf("effective F = %d", want.Capacity.ActiveExecutions)
	}
	cfg.Redis.Password = "DO_NOT_LOG_PASSWORD"
	cfg.PhaseTwo.Access.UQEndpoint = "DO_NOT_LOG_ENDPOINT"
	cfg.PhaseTwo.Worker.ID = "DO_NOT_LOG_WORKER"
	got, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	if got.Digest != want.Digest || strings.Contains(string(encoded), "DO_NOT_LOG") {
		t.Fatalf("unsafe profile: %s", encoded)
	}
}

func TestPhaseTwoRetainedBudgetProfileUsesResolvedDefaultAndOverride(t *testing.T) {
	cfg := config.Default()
	base, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	if base.Capacity.RetainedBytes != 96<<20 || base.Capacity.UQBodyBytes != 96<<20 {
		t.Fatalf("resolved default capacity = %+v", base.Capacity)
	}
	if base.Capacity.ActiveExecutions != 0 || base.Capacity.QueryPermits != 2 || base.Capacity.RecoveryQueryPermits != 1 {
		t.Fatalf("concurrency changed: %+v", base.Capacity)
	}
	cfg.PhaseTwo.Coordinator.MaxRetainedBytes = 64 << 20
	legacy, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Digest == base.Digest || legacy.Capacity.RetainedBytes != 64<<20 || legacy.Capacity.UQBodyBytes != 64<<20 {
		t.Fatalf("explicit override missing from runtime facts: %+v", legacy)
	}
	// Retained bytes also bounds the existing UQ body limit; all other limits stay fixed.
	legacy.Capacity.RetainedBytes = base.Capacity.RetainedBytes
	legacy.Capacity.UQBodyBytes = base.Capacity.UQBodyBytes
	if legacy.Capacity != base.Capacity {
		t.Fatalf("unrelated capacity changed: before=%+v after=%+v", base.Capacity, legacy.Capacity)
	}
}

func TestPhaseTwoCPUInitializationFailsBeforeOpeningServices(t *testing.T) {
	want := errors.New("CPU quota unavailable")
	dependencies := phaseTwoApplicationDependencies{
		configureCPU: func() (string, error) { return "", want },
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger, *phaseTwoApplicationHealth) (*phaseTwoWorkerBundle, error) {
			t.Fatal("opened bundle after CPU failure")
			return nil, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
			t.Fatal("opened HTTP after CPU failure")
			return nil, nil
		},
	}
	err := runPhaseTwoApplicationWithDependencies(context.Background(), validGoAccessRuntimeConfig(), metric.NewRecorder(metric.BuildInfo{}), nil, dependencies)
	if !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
}

func TestPhaseTwoRuntimeProfileTracksSchedulerFieldsAndCPU(t *testing.T) {
	cfg := config.Default()
	base, _ := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	for name, change := range map[string]func(*config.Config){
		"expired_range": func(c *config.Config) { c.PhaseTwo.Scheduler.ExpiredRangeEnabled = true },
		"F":             func(c *config.Config) { c.PhaseTwo.Scheduler.ActiveExecutionLimit++ },
		"P":             func(c *config.Config) { c.PhaseTwo.Scheduler.ProcessQueryPermits++ },
		"R":             func(c *config.Config) { c.PhaseTwo.Scheduler.RecoveryQueryPermits++ },
		"retry":         func(c *config.Config) { c.PhaseTwo.Scheduler.RetryMaxDelay++ },
		"replay":        func(c *config.Config) { c.PhaseTwo.Scheduler.MaxReplayAge++ },
		"bytes":         func(c *config.Config) { c.PhaseTwo.Coordinator.MaxRetainedBytes++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := cfg
			change(&changed)
			got, _ := phaseTwoRuntimeProfile(changed, "cpu_quota", 8)
			if got.Digest == base.Digest {
				t.Fatal("capacity change lost from digest")
			}
		})
	}
	got, _ := phaseTwoRuntimeProfile(cfg, "cpu_quota", 4)
	if got.Digest == base.Digest {
		t.Fatal("actual CPU change lost from digest")
	}
}

func TestPhaseTwoCPUFailureIsNotReportedAsConfigured(t *testing.T) {
	errQuota := errors.New("quota unreadable")
	source, err := configurePhaseTwoCPUWith(func(log func(string, ...interface{})) error { return errQuota })
	if !errors.Is(err, errQuota) || source != "" {
		t.Fatalf("source=%q err=%v", source, err)
	}
}

func TestPhaseTwoRuntimeProfileIncludesEverySchedulerField(t *testing.T) {
	cfg := config.Default()
	base, _ := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	for i := 0; i < reflect.TypeOf(cfg.PhaseTwo.Scheduler).NumField(); i++ {
		changed := cfg
		field := reflect.ValueOf(&changed.PhaseTwo.Scheduler).Elem().Field(i)
		if field.Kind() == reflect.Bool {
			field.SetBool(!field.Bool())
		} else if field.Kind() == reflect.Uint32 {
			field.SetUint(field.Uint() + 1)
		} else {
			field.SetInt(field.Int() + 1)
		}
		got, err := phaseTwoRuntimeProfile(changed, "cpu_quota", 8)
		if err != nil || got.Digest == base.Digest {
			t.Fatalf("scheduler field %s absent: %v", reflect.TypeOf(cfg.PhaseTwo.Scheduler).Field(i).Name, err)
		}
	}
}

func TestPhaseTwoCPURecordsPinnedLibraryDecisionWithoutRawEnvironment(t *testing.T) {
	for format, want := range map[string]string{
		"maxprocs: Honoring GOMAXPROCS=%q as set in environment":             "environment_override",
		"maxprocs: Updating GOMAXPROCS=%v: determined from CPU quota":        "cpu_quota",
		"maxprocs: Updating GOMAXPROCS=%v: using minimum allowed GOMAXPROCS": "cpu_quota_minimum",
		"maxprocs: Leaving GOMAXPROCS=%v: CPU quota undefined":               "runtime_default",
	} {
		source, err := configurePhaseTwoCPUWith(func(log func(string, ...interface{})) error { log(format, "secret"); return nil })
		if err != nil || source != want {
			t.Fatalf("source=%q want=%q err=%v", source, want, err)
		}
	}
	t.Setenv("GOMAXPROCS", "8")
	source, err := configurePhaseTwoCPU()
	if err != nil || source != "environment_override" {
		t.Fatalf("pinned library source=%q err=%v", source, err)
	}
}
