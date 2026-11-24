// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A query that feeds only a dependency hands its series on without their
// dimensions: its digest, time and value as answered, and no dimension nor
// identity field. The PRIMARY query beside it, answered the same series,
// hands it on with every dimension, since the target filter, the alert and
// its grouping read them there.
func TestADependencysSeriesIsHandedOnWithoutItsDimensionsAndThePrimarysWithThem(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	withYesterdayDependency(t, &frozen)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	slotEnd := int64(contractRef.Slot.EvaluationTime)

	source, err := NewSource(staticFrozenPlan{plan: frozen}, &windowedHostProvider{hosts: []string{"192.0.2.10"}},
		&recordingQueryPermits{}, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.Unix(slotEnd, 0) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	consumer := &recordingConsumer{}
	if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}, consumer); err != nil {
		t.Fatal(err)
	}

	byRole := map[execution.InputRole]execution.SeriesExecutionBatch{}
	for _, batch := range consumer.batches {
		for _, binding := range batch.Inputs {
			byRole[binding.Role] = batch
		}
	}
	primary, primaryOK := byRole[execution.InputRolePrimary]
	dependency, dependencyOK := byRole[execution.InputRoleAlgorithmDependency]
	if !primaryOK || !dependencyOK || len(consumer.batches) != 2 {
		t.Fatalf("batches = %d (primary %t, dependency %t), want the series once from each query",
			len(consumer.batches), primaryOK, dependencyOK)
	}
	kept, _ := primary.Dataset.Record(0)
	if dimensions := kept.Dimensions(); len(dimensions) != 3 || len(kept.DimensionIdentity().Fields) != 1 {
		t.Fatalf("the PRIMARY series kept dimensions %v and identity %+v, want every dimension and its field",
			dimensions, kept.DimensionIdentity())
	}
	slim, _ := dependency.Dataset.Record(0)
	if dimensions := slim.Dimensions(); len(dimensions) != 0 || len(slim.DimensionIdentity().Fields) != 0 {
		t.Fatalf("the dependency's series kept dimensions %v and identity fields %+v, want neither",
			dimensions, slim.DimensionIdentity().Fields)
	}
	value, found := slim.Value("value")
	if slim.DimensionIdentityDigest() != kept.DimensionIdentityDigest() || slim.RecordID() == "" ||
		!found || string(value) != "60" || slim.SourceTime() == 0 {
		t.Fatalf("the dependency's series reads digest %s id %q value %s time %d, want the series' digest, its id, "+
			"its value and its time", slim.DimensionIdentityDigest(), slim.RecordID(), value, slim.SourceTime())
	}
	for _, binding := range dependency.Inputs {
		if binding.Dataset != dependency.Dataset || binding.View == nil || binding.View.Len() != 1 {
			t.Fatalf("the dependency's binding = %+v, want it on the Dataset handed on, with its view", binding)
		}
	}
}
