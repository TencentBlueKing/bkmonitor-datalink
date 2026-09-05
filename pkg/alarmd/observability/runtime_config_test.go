// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestRuntimeConfigEvidenceUsesExistingConfigLoadedLog(t *testing.T) {
	var output bytes.Buffer
	facts := &RuntimeConfigFacts{Profile: "standard-conservative-v1", CPUSource: "cpu_quota", GOMAXPROCS: 8, Digest: "digest"}
	observation := NormalizeObservation(Observation{Component: ComponentRuntime, Stage: StageConfigLoaded, Result: ResultSuccess, RuntimeConfig: facts})
	facts.GOMAXPROCS = 64
	New(ComponentRuntime, &output).logObservation(context.Background(), observation)
	var event struct {
		RuntimeConfig RuntimeConfigFacts `json:"runtime_config"`
	}
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.RuntimeConfig.GOMAXPROCS != 8 || event.RuntimeConfig.Digest != "digest" {
		t.Fatalf("evidence=%+v", event)
	}
	invalid := NormalizeObservation(Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultSuccess, RuntimeConfig: facts})
	if invalid.RuntimeConfig != nil {
		t.Fatal("runtime configuration escaped startup scope")
	}
}
