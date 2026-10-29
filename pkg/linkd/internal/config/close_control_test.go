// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import "testing"

func TestCloseBudgetsAreIndependentAndValidated(t *testing.T) {
	cfg := Config{Lifecycle: &LifecycleConfig{Concurrency: 3}, ControlPlane: &ControlPlaneConfig{Close: &CloseControlConfig{BatchSize: 5000, Concurrency: 2, MemberConcurrency: 7}, Merge: &MergeControlConfig{Concurrency: 4}}}
	got := cfg.CloseControlSettings()
	if got != (CloseControlConfig{BatchSize: 5000, Concurrency: 2, MemberConcurrency: 7}) || cfg.Lifecycle.Concurrency != 3 || cfg.ControlPlane.Merge.Concurrency != 4 {
		t.Fatal(got)
	}
	if err := cfg.ControlPlane.Validate(); err != nil {
		t.Fatal(err)
	}
	if defaults := (Config{}).CloseControlSettings(); defaults != (CloseControlConfig{BatchSize: 1000, Concurrency: 8, MemberConcurrency: 32}) {
		t.Fatal(defaults)
	}
	for _, c := range []CloseControlConfig{{BatchSize: -1}, {BatchSize: 5001}, {Concurrency: -1}, {MemberConcurrency: -1}} {
		if c.Validate() == nil {
			t.Fatal("invalid close budget accepted", c)
		}
	}
}
