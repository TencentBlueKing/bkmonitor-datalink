// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestCanonicalModeDefaultsToTheEstablishedForm(t *testing.T) {
	cfg := Default().PhaseTwo.Canonical
	if cfg.SelectedMode() != contract.CanonicalModeEstablished {
		t.Fatalf("a deployment that says nothing must keep the established encoder, got %q", cfg.SelectedMode())
	}
	if cfg.Stride() != 0 {
		t.Fatalf("no comparison is running, so nothing should be paying for one; stride %d", cfg.Stride())
	}
}

// A leftover stride must not keep costing anything once the mode stops
// comparing. The setting outliving its mode is the ordinary way a rollout
// leaves a process paying for work nobody reads.
func TestCanonicalStrideIsZeroWhenNothingCompares(t *testing.T) {
	for _, mode := range []string{contract.CanonicalModeEstablished, contract.CanonicalModeStream} {
		cfg := PhaseTwoCanonicalConfig{Mode: mode, ShadowSampleStride: 4}
		if cfg.Stride() != 0 {
			t.Fatalf("mode %s still samples at %d", mode, cfg.Stride())
		}
	}
	for _, mode := range []string{contract.CanonicalModeShadow, contract.CanonicalModeStreamShadow} {
		cfg := PhaseTwoCanonicalConfig{Mode: mode}
		if cfg.Stride() != defaultCanonicalShadowStride {
			t.Fatalf("mode %s without a stride must derive one, got %d", mode, cfg.Stride())
		}
		explicit := PhaseTwoCanonicalConfig{Mode: mode, ShadowSampleStride: 4}
		if explicit.Stride() != 4 {
			t.Fatalf("mode %s ignored the configured stride", mode)
		}
	}
}

func TestCanonicalModeIsValidatedAgainstTheContract(t *testing.T) {
	for _, mode := range contract.CanonicalModeNames() {
		text := validGoAccessRuntimeConfigYAML("canonical-worker") +
			"  canonical:\n    mode: " + mode + "\n"
		cfg, err := Load(writeConfig(t, text))
		if err != nil {
			t.Fatalf("mode %s rejected: %v", mode, err)
		}
		if cfg.PhaseTwo.Canonical.SelectedMode() != mode {
			t.Fatalf("mode %s did not survive loading", mode)
		}
	}
	text := validGoAccessRuntimeConfigYAML("canonical-worker") + "  canonical:\n    mode: streem\n"
	if _, err := Load(writeConfig(t, text)); err == nil {
		t.Fatal("a misspelled mode was accepted; the process would have run in a position nobody asked for")
	}
}
