// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"strings"
	"testing"
)

func TestMergeControlDefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		in   MergeControlConfig
		want MergeControlConfig
	}{
		{name: "default", want: MergeControlConfig{MemberBatchSize: 128, Concurrency: 8}},
		{name: "explicit", in: MergeControlConfig{MemberBatchSize: 256, Concurrency: 16}, want: MergeControlConfig{MemberBatchSize: 256, Concurrency: 16}},
		{name: "batch only", in: MergeControlConfig{MemberBatchSize: 64}, want: MergeControlConfig{MemberBatchSize: 64, Concurrency: 8}},
		{name: "concurrency only", in: MergeControlConfig{Concurrency: 3}, want: MergeControlConfig{MemberBatchSize: 128, Concurrency: 3}},
		{name: "single member single worker", in: MergeControlConfig{MemberBatchSize: 1, Concurrency: 1}, want: MergeControlConfig{MemberBatchSize: 1, Concurrency: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			original := test.in
			if got := test.in.WithDefaults(); got != test.want {
				t.Fatalf("defaults=%+v, want %+v", got, test.want)
			}
			if err := test.in.Validate(); err != nil {
				t.Fatal(err)
			}
			if test.in != original {
				t.Fatal("defaulting or validation modified the input")
			}
		})
	}
}

func TestMergeControlSettingsDoNotModifyConfiguration(t *testing.T) {
	t.Parallel()
	for _, cp := range []*ControlPlaneConfig{nil, {}, {Merge: &MergeControlConfig{}}} {
		cfg := Config{ControlPlane: cp}
		if got := cfg.MergeControlSettings(); got != (MergeControlConfig{MemberBatchSize: 128, Concurrency: 8}) {
			t.Fatalf("omitted merge budget=%+v", got)
		}
		if cp != nil && cp.Merge != nil && *cp.Merge != (MergeControlConfig{}) {
			t.Fatal("reading effective settings modified input")
		}
	}
	original := ControlPlaneConfig{Merge: &MergeControlConfig{MemberBatchSize: 32}}
	normalized := original.WithDefaults()
	if normalized.Merge == original.Merge || *normalized.Merge != (MergeControlConfig{MemberBatchSize: 32, Concurrency: 8}) {
		t.Fatal("control-plane defaults did not clone nested merge configuration")
	}
	normalized.Merge.MemberBatchSize = 256
	normalized.Merge.Concurrency = 16
	effective := (Config{ControlPlane: &original}).MergeControlSettings()
	effective.MemberBatchSize = 512
	if *original.Merge != (MergeControlConfig{MemberBatchSize: 32}) {
		t.Fatal("derived configuration shares mutable input")
	}
}

func TestControlPlaneMergeValidationRejectsNegativeBudgets(t *testing.T) {
	t.Parallel()
	for _, settings := range []MergeControlConfig{
		{MemberBatchSize: -1},
		{Concurrency: -1},
		{MemberBatchSize: 128, Concurrency: -2},
		{MemberBatchSize: -2, Concurrency: 8},
	} {
		original := settings
		cp := ControlPlaneConfig{Merge: &settings}
		cfg := Default()
		cfg.ControlPlane = &cp
		for _, err := range []error{settings.Validate(), cp.Validate(), cfg.Validate()} {
			if err == nil || !strings.Contains(err.Error(), "control_plane.merge") {
				t.Fatalf("invalid merge settings %+v were accepted: %v", settings, err)
			}
		}
		if settings != original {
			t.Fatal("invalid input was silently defaulted")
		}
	}
	if err := (ControlPlaneConfig{Merge: &MergeControlConfig{}}).Validate(); err != nil {
		t.Fatal("merge configuration alone should identify a control-plane task", err)
	}
}

func TestLoadControlPlaneMergeBudgets(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		yaml string
		want MergeControlConfig
		bad  bool
	}{
		{name: "omitted", yaml: "{}\n", want: MergeControlConfig{MemberBatchSize: 128, Concurrency: 8}},
		{name: "empty merge", yaml: "control_plane:\n  merge: {}\n", want: MergeControlConfig{MemberBatchSize: 128, Concurrency: 8}},
		{name: "explicit", yaml: "control_plane:\n  merge:\n    member_batch_size: 256\n    concurrency: 16\n", want: MergeControlConfig{MemberBatchSize: 256, Concurrency: 16}},
		{name: "zero means default", yaml: "control_plane:\n  merge:\n    member_batch_size: 0\n    concurrency: 0\n", want: MergeControlConfig{MemberBatchSize: 128, Concurrency: 8}},
		{name: "negative batch", yaml: "control_plane:\n  merge:\n    member_batch_size: -1\n", bad: true},
		{name: "negative concurrency", yaml: "control_plane:\n  merge:\n    concurrency: -1\n", bad: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := load(writeConfig(t, test.yaml), Overrides{}, mapLookup(nil))
			if test.bad {
				if err == nil || !strings.Contains(err.Error(), "control_plane.merge") {
					t.Fatalf("invalid YAML budget accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.MergeControlSettings(); got != test.want {
				t.Fatalf("loaded budget=%+v, want %+v", got, test.want)
			}
		})
	}
}
