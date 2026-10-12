// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
	// The clamp is the one case where the two execution-limit fields differ,
	// and both halves have to survive it: the table has to say what the process
	// runs with and still say what it derived, or a clamp that really took
	// effect is invisible to whoever reads this during an incident.
	if want.Capacity.EffectiveActiveExecutions != 3 || want.Capacity.DerivedActiveExecutions != 17 {
		t.Fatalf("clamped executions reported as derived %d / effective %d, want 17 / 3",
			want.Capacity.DerivedActiveExecutions, want.Capacity.EffectiveActiveExecutions)
	}
	cfg.Redis.Password = "DO_NOT_LOG_PASSWORD"
	cfg.PhaseTwo.Linkd.Password = "DO_NOT_LOG_LINKD_PASSWORD"
	cfg.PhaseTwo.Linkd.Username = "DO_NOT_LOG_LINKD_USER"
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
	// The budgets follow the container, so the profile reports which
	// container they were read from alongside them.
	// The UQ body follows the retained budget but has its own ceiling, so it
	// is bounded by it rather than equal to it.
	if base.Capacity.RetainedBytes == 0 || base.Capacity.UQBodyBytes <= 0 ||
		base.Capacity.UQBodyBytes > int64(base.Capacity.RetainedBytes) {
		t.Fatalf("resolved default capacity = %+v", base.Capacity)
	}
	if base.MemorySource == "" || base.MemoryLimitBytes == 0 {
		t.Fatalf("memory budget provenance missing: %+v", base)
	}
	if base.Capacity.QueryPermits <= 0 || base.Capacity.RecoveryQueryPermits <= 0 ||
		base.Capacity.ReadyQueue < base.Capacity.QueryPermits {
		t.Fatalf("derived concurrency is not usable: %+v", base.Capacity)
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
		newHTTP: func(*metric.Recorder, observability.HealthSource, httpSurface) (httpRuntime, error) {
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
		"F":      func(c *config.Config) { c.PhaseTwo.Scheduler.ActiveExecutionLimit++ },
		"P":      func(c *config.Config) { c.PhaseTwo.Scheduler.ProcessQueryPermits++ },
		"R":      func(c *config.Config) { c.PhaseTwo.Scheduler.RecoveryQueryPermits++ },
		"retry":  func(c *config.Config) { c.PhaseTwo.Scheduler.RetryMaxDelay++ },
		"replay": func(c *config.Config) { c.PhaseTwo.Scheduler.MaxReplayAge++ },
		"bytes":  func(c *config.Config) { c.PhaseTwo.Coordinator.MaxRetainedBytes++ },
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
		// And it is a source the capacity metric's closed label set names,
		// rather than one it would fold into "other".
		if !slices.Contains(observability.CPUSources, source) {
			t.Fatalf("source %q is not among the CPU sources %v", source, observability.CPUSources)
		}
	}
	// The process resolves its CPU budget once, so exercise the pinned
	// library through the same seam the startup path uses rather than
	// through the cached entry point.
	t.Setenv("GOMAXPROCS", "8")
	source, err := configurePhaseTwoCPUWith(setMaxprocsFromCPUQuota)
	if err != nil || source != "environment_override" {
		t.Fatalf("pinned library source=%q err=%v", source, err)
	}
}

// The control timeline cache budget is derived, not written, so a release
// preflight has to be able to read it off --check-config the way it reads
// every other derived budget. The running process publishes its occupancy,
// but that answers a different question and only once a Pod exists.
func TestResolvedRuntimeFactsCarryTheDerivedTimelineCacheBudget(t *testing.T) {
	cfg := config.Default()
	facts, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	derived := config.DeriveControlTimelineCache(config.DetectCapacityInputs())
	if facts.Capacity.ControlTimelineCacheBytes != derived.MaxBytes ||
		facts.Capacity.ControlTimelineCacheEntries != derived.MaxEntries {
		t.Fatalf("startup table reports %d bytes / %d entries, this container derives %d / %d",
			facts.Capacity.ControlTimelineCacheBytes, facts.Capacity.ControlTimelineCacheEntries,
			derived.MaxBytes, derived.MaxEntries)
	}
	// The budget travels with the memory limit it came from, so a table
	// printed outside a Pod says which container it describes.
	if facts.MemoryLimitBytes == 0 || facts.MemorySource == "" || derived.MaxBytes == 0 {
		t.Fatalf("budget has no container provenance: %+v", facts)
	}
	var printed bytes.Buffer
	if err := printResolvedRuntimeFacts(cfg, &printed); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"control_timeline_cache_bytes", "control_timeline_cache_entries"} {
		if !strings.Contains(printed.String(), field) {
			t.Fatalf("--check-config table has no %s:\n%s", field, printed.String())
		}
	}
}

// The alert link's settings are on the profile, the Console as configured
// or not and never its address: whether the alert closes run at all is
// otherwise readable only from the file the process started with.
func TestThePhaseTwoRuntimeProfileSaysWhetherTheLinkIsConfigured(t *testing.T) {
	cfg := config.Default()
	off, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	if off.Linkd != (observability.RuntimeLinkdFacts{}) {
		t.Fatalf("default linkd facts = %+v, want nothing configured", off.Linkd)
	}
	cfg.PhaseTwo.Linkd.ConsoleURL = "http://DO_NOT_LOG_CONSOLE:8080"
	on, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	want := observability.RuntimeLinkdFacts{ConsoleConfigured: true}
	encoded, _ := json.Marshal(on)
	if on.Linkd != want || strings.Contains(string(encoded), "DO_NOT_LOG") || on.Digest == off.Digest {
		t.Fatalf("linkd facts = %+v (want %+v), digest moved %v: %s", on.Linkd, want, on.Digest != off.Digest, encoded)
	}
}

// The runtime facts name the retention the deployment runs on, beside every
// input, and the lengths agree with their inputs as reported: a reader checks
// them from the facts alone, without the configuration file or the functions.
// Both sides of each larger-of: the catalog TTL over and under what the
// recovery contract needs, and the state store's maximum TTL over and under
// the catalog retention.
func TestTheRuntimeProfileNamesTheRetentionAndItsInputs(t *testing.T) {
	for _, arm := range []struct {
		name               string
		catalogTTL, maxTTL time.Duration
		catalogIsTTL       bool
		limitIsMaxTTL      bool
	}{
		{name: "the catalog TTL and the state ceiling decide", catalogTTL: 96 * time.Hour, maxTTL: 720 * time.Hour, catalogIsTTL: true, limitIsMaxTTL: true},
		{name: "the recovery contract and the catalog decide", catalogTTL: time.Hour, maxTTL: 2 * time.Hour},
	} {
		t.Run(arm.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.PhaseTwo.Control.CatalogTTL = config.Duration(arm.catalogTTL)
			cfg.Redis.MaxTTL = config.Duration(arm.maxTTL)
			facts, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
			if err != nil {
				t.Fatal(err)
			}
			r := facts.Retention
			if r.CatalogTTLSeconds != int64(arm.catalogTTL/time.Second) || r.RedisMaxTTLSeconds != int64(arm.maxTTL/time.Second) ||
				r.CatalogKeyCadenceSeconds != 24*3600 || r.DownstreamExecutionReserveSeconds <= 0 || r.MaxReplayAgeSeconds <= 0 ||
				r.PublicationDelayAllowanceSeconds <= 0 || r.PostRecoveryTerminalDelaySeconds <= 0 || r.RedisRestartMarginSeconds <= 0 {
				t.Fatalf("inputs = %+v, want the configured and derived values", r)
			}
			minimum := r.PublicationDelayAllowanceSeconds + r.CatalogKeyCadenceSeconds - r.DownstreamExecutionReserveSeconds +
				r.MaxReplayAgeSeconds + r.PostRecoveryTerminalDelaySeconds
			if r.SnapshotMinimumSeconds != minimum {
				t.Fatalf("snapshot minimum = %d, want %d from the inputs as reported", r.SnapshotMinimumSeconds, minimum)
			}
			if wantCatalog := max(r.CatalogTTLSeconds, minimum); r.CatalogSeconds != wantCatalog || (r.CatalogSeconds == r.CatalogTTLSeconds) != arm.catalogIsTTL {
				t.Fatalf("catalog = %d, want %d (the TTL deciding: %v)", r.CatalogSeconds, wantCatalog, arm.catalogIsTTL)
			}
			if wantLimit := max(r.RedisMaxTTLSeconds, r.CatalogSeconds); r.ObjectLimitSeconds != wantLimit || (r.ObjectLimitSeconds == r.RedisMaxTTLSeconds) != arm.limitIsMaxTTL {
				t.Fatalf("object limit = %d, want %d (the state ceiling deciding: %v)", r.ObjectLimitSeconds, wantLimit, arm.limitIsMaxTTL)
			}
		})
	}
}
