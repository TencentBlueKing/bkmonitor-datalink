// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A release preflight has to be able to state what the build about to ship
// will run under, and the settings that matter most are the ones no file
// mentions because the process decides them. --check-config used to answer
// only "valid", which left a configuration change unreviewable until a Pod
// either came up or did not.
func TestCheckConfigReportsSettingsTheFileNeverMentions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	contents := validGoAccessApplicationYAML()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}

	var facts observability.RuntimeConfigFacts
	if err := json.Unmarshal(stdout.Bytes(), &facts); err != nil {
		t.Fatalf("resolved facts are not machine readable: %v (%q)", err, stdout.String())
	}
	defaults := config.Default()
	for name, check := range map[string]struct{ got, want any }{
		"tick_interval":      {facts.Capacity.TickNS, int64(defaults.PhaseTwo.Scheduler.TickInterval)},
		"ready_queue":        {facts.Capacity.ReadyQueue, defaults.PhaseTwo.Scheduler.ReadyQueueCapacity},
		"state_mutations":    {facts.Capacity.StateMutations, defaults.PhaseTwo.Coordinator.MaxStateMutations},
		"query permits":      {facts.Capacity.QueryPermits, defaults.PhaseTwo.Scheduler.ProcessQueryPermits},
		"active executions":  {facts.Capacity.ConfiguredActiveExecutions, defaults.PhaseTwo.Scheduler.ActiveExecutionLimit},
		"replay slots":       {facts.Capacity.ReplaySlots, defaults.PhaseTwo.Scheduler.MaxReplaySlots},
		"sequencer bookings": {facts.Capacity.SequencerReservations, defaults.PhaseTwo.Coordinator.MaxSequencerReservations},
	} {
		if check.got != check.want {
			t.Errorf("%s reported as %v, want the product default %v", name, check.got, check.want)
		}
		if strings.Contains(contents, "tick_interval") {
			t.Fatal("the fixture states these settings, so the report proves nothing")
		}
	}
	// Derived values matter more than copied ones, because no file states
	// them at all: the per-Slot cap is the smaller of the process budget and
	// what StateApplyChunks Store calls can carry, and it is the number that
	// decides whether a large Query Group can complete.
	chunked := uint64(facts.Capacity.StoreMaxItems) * uint64(facts.Capacity.StateApplyChunks)
	if want := min(facts.Capacity.StateMutations, chunked); facts.Capacity.SlotStateMutations != want {
		t.Errorf("per-Slot state cap reported as %d, want %d", facts.Capacity.SlotStateMutations, want)
	}
	// The pool size is derived, not written anywhere, and the source of the
	// CPU budget it derives from has to travel with it: a table printed
	// outside the Pod would otherwise pass the host's cores off as the
	// container's.
	if facts.Capacity.RedisPoolSize <= 0 || facts.CPUSource == "" || facts.GOMAXPROCS <= 0 {
		t.Errorf("derived pool %d, cpu source %q and GOMAXPROCS %d must all be reported",
			facts.Capacity.RedisPoolSize, facts.CPUSource, facts.GOMAXPROCS)
	}
	if facts.Digest == "" {
		t.Error("the report must carry the digest the running process logs")
	}
}

// The Ownership compatibility identity is the run mode. Keeping a second
// field for it let a deployment declare shadow in one place and something
// else in the other, with nothing in the process to notice; the field is
// gone, so a values file still carrying it fails loudly instead.
func TestDeploymentProfileIsTheModeAndIsNoLongerConfigurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	contents := strings.Replace(
		validGoAccessApplicationYAML(),
		"  worker:\n    id: alarmd-worker-0\n",
		"  worker:\n    id: alarmd-worker-0\n    deployment_profile: production\n",
		1,
	)
	if !strings.Contains(contents, "deployment_profile") {
		t.Fatal("fixture does not carry the removed field")
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("run() code = %d, want rejection; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "deployment_profile") {
		t.Fatalf("rejection does not name the retired field: %q", stderr.String())
	}

	cfg := validGoAccessRuntimeConfig()
	if cfg.DeploymentProfile() != cfg.Mode {
		t.Fatalf("deployment profile %q must be the run mode %q", cfg.DeploymentProfile(), cfg.Mode)
	}
}
