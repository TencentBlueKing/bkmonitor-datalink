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
	"testing"
)

// The collector budget is derived and unconditional, so the preflight table is
// where anyone finds out what it is. That is not a nicety: nothing outside the
// process can set these, and a build that silently stopped deriving them would
// look identical from outside without this.
//
// The assertions go through the JSON rather than the struct so that they read
// the surface a preflight actually reads.
func TestCheckConfigReportsTheDerivedCollectorBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	if err := os.WriteFile(path, []byte(validGoAccessApplicationYAML()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--check-config", "--config", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}

	var reported map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &reported); err != nil {
		t.Fatalf("resolved facts are not machine readable: %v (%q)", err, stdout.String())
	}
	capacity, ok := reported["capacity"].(map[string]any)
	if !ok {
		t.Fatalf("resolved facts carry no capacity table: %q", stdout.String())
	}
	// Presence is the assertion, not a formality. A build that stopped deriving
	// these would print a table that still validated and still looked complete.
	number := func(key string) float64 {
		value, present := capacity[key]
		if !present {
			t.Fatalf("the preflight table does not report %s at all: %v", key, capacity)
		}
		got, ok := value.(float64)
		if !ok {
			t.Fatalf("%s reported as %v, want a number", key, value)
		}
		return got
	}
	source, _ := reported["memory_source"].(string)
	container, _ := reported["memory_limit_bytes"].(float64)
	limit, percent := number("go_memory_limit_bytes"), number("go_gc_percent")

	if source == "fallback_default" {
		// No container stated a limit, so there is nothing to enforce and the
		// table has to say so rather than report a guess as a budget.
		if limit != 0 || percent != 0 {
			t.Fatalf("collector budget reported as %v / %v against a guessed container", limit, percent)
		}
	} else {
		// The table has to hold together with itself: a soft limit at or below
		// a per-Slot ceiling derived from the same container would put a Slot
		// admitted at its cap straight into the collector's limit, a target left
		// at the Go default would make the limit unreachable and therefore
		// inert, and a limit at the container leaves nothing to overshoot into.
		if limit <= number("retained_bytes") {
			t.Fatalf("soft limit %v does not clear the reported %v retained ceiling", limit, number("retained_bytes"))
		}
		if percent <= 100 {
			t.Fatalf("collector target reported as %v, want above the Go default", percent)
		}
		if container > 0 && limit >= container {
			t.Fatalf("soft limit %v leaves no reserve inside the %v container", limit, container)
		}
	}
	// Both halves of the execution limit are reported, and neither is named for
	// a setting: nothing outside the process can reach either one.
	if _, present := capacity["configured_active_executions"]; present {
		t.Fatal("the preflight table still names an execution limit after a setting nobody can set")
	}
	if number("effective_active_executions") <= number("query_permits") {
		t.Fatalf("effective executions reported as %v against %v query permits, want room above the gate they feed",
			number("effective_active_executions"), number("query_permits"))
	}
	if number("derived_active_executions") < number("effective_active_executions") {
		t.Fatalf("derived executions %v are below the effective %v, which only the ready-queue clamp can move, and only downwards",
			number("derived_active_executions"), number("effective_active_executions"))
	}
}
