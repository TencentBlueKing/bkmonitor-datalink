// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import "testing"

func TestCloseBudgetsAreIndependentAndValidated(t *testing.T) {
	cfg := Config{Lifecycle: &LifecycleConfig{Concurrency: 3}, ControlPlane: &ControlPlaneConfig{Close: &CloseControlConfig{Concurrency: 2}, Merge: &MergeControlConfig{Concurrency: 4}}}
	got := cfg.CloseControlSettings()
	if got != (CloseControlConfig{Concurrency: 2}) || cfg.Lifecycle.Concurrency != 3 || cfg.ControlPlane.Merge.Concurrency != 4 {
		t.Fatal(got)
	}
	if err := cfg.ControlPlane.Validate(); err != nil {
		t.Fatal(err)
	}
	if defaults := (Config{}).CloseControlSettings(); defaults != (CloseControlConfig{Concurrency: 8}) {
		t.Fatal(defaults)
	}
	if (CloseControlConfig{Concurrency: -1}).Validate() == nil {
		t.Fatal("negative request concurrency accepted")
	}
}
