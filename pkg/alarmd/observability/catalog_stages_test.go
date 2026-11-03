// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// The catalog's stages keep their names: a catalog write that failed is a
// line saying object_catalog, not _other, and it is on the same footing as
// every other phase-two stage. The read is the exception in the other
// direction -- one observation per read, hit included, three hundred a second
// on a live replica -- and having a name does not make it a line.
func TestCatalogStagesAreNamedAndTheReadIsNeverALine(t *testing.T) {
	t.Parallel()

	for _, stage := range []Stage{StageObjectCatalog, StageObjectRead, StageScheduleCutover} {
		if component, got := NormalizeComponentStage(ComponentControlPlane, stage); component != ComponentControlPlane || got != stage {
			t.Fatalf("%s normalized to (%s, %s), want its own name", stage, component, got)
		}
		if IsGenericMetricComponentStage(ComponentControlPlane, stage) {
			t.Fatalf("%s entered the generic metric cross product", stage)
		}
	}

	var output bytes.Buffer
	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 600, MaxScopes: 16}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	ctx := context.Background()
	for i := 0; i < 300; i++ {
		observer.Observe(ctx, Observation{
			Component: ComponentControlPlane, Stage: StageObjectRead, Result: ResultSuccess,
			ObjectRead: &ObjectReadFacts{Kind: "query_group", Result: "hit"},
		})
	}
	if output.Len() != 0 {
		t.Fatalf("object reads wrote lines:\n%s", output.String())
	}
	observer.Observe(ctx, Observation{
		Component: ComponentControlPlane, Stage: StageObjectCatalog, Result: Result(ResultFailed),
		ReasonCode: ReasonInternalUnknown, Err: errors.New("catalog write refused"),
	})
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("lines=%d, want the one failed catalog write:\n%s", len(lines), output.String())
	}
	var line map[string]any
	if err := json.Unmarshal(lines[0], &line); err != nil {
		t.Fatal(err)
	}
	if line["component"] != string(ComponentControlPlane) || line["stage"] != string(StageObjectCatalog) {
		t.Fatalf("catalog failure logged as component=%v stage=%v, want its own name", line["component"], line["stage"])
	}
}
