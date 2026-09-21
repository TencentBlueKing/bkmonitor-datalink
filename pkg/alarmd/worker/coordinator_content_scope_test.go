// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// decision-016 batch 2: the content a Slot declares reaches every fenced
// write the Slot makes, unchanged -- the Progress it begins, the State it
// applies under the fence, the Progress it commits -- so the store's fence
// can refuse each of them by name once the Query Group has moved to other
// content. A scope that reached one write and not another would leave that
// write authorized by the lease alone.
func TestTheDeclaredContentScopeReachesEveryFencedWriteOfTheSlot(t *testing.T) {
	fixture, fenced := newFencedFixture(t, false)
	request := slotRequest(execution.OperationNormal)
	request.ContentScope = "qg-object-a"
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if fixture.ports.lastBegin.ContentScope != "qg-object-a" {
		t.Fatalf("BeginSlot declared %q, want the Slot's content scope", fixture.ports.lastBegin.ContentScope)
	}
	if fixture.ports.lastBegin.Projection.ContentScope != "qg-object-a" {
		t.Fatalf("the begun projection carries %q, want the content scope so a retry declares the same", fixture.ports.lastBegin.Projection.ContentScope)
	}
	if len(fenced.fences) != 1 || fenced.fences[0].ContentScope != "qg-object-a" {
		t.Fatalf("fenced State apply declared %+v, want the Slot's content scope on the fence", fenced.fences)
	}
	if fixture.ports.lastProgress.ContentScope != "qg-object-a" {
		t.Fatalf("CommitProgress declared %q, want the Slot's content scope", fixture.ports.lastProgress.ContentScope)
	}
}

// A Slot from a Segment that names no content declares nothing anywhere: the
// fence then compares what it always compared. Nothing invents a scope.
func TestAnUndeclaredContentScopeStaysUndeclaredOnEveryWrite(t *testing.T) {
	fixture, fenced := newFencedFixture(t, false)
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if fixture.ports.lastBegin.ContentScope != "" || fixture.ports.lastProgress.ContentScope != "" ||
		len(fenced.fences) != 1 || fenced.fences[0].ContentScope != "" {
		t.Fatalf("an undeclared Slot declared something: begin=%q apply=%+v commit=%q",
			fixture.ports.lastBegin.ContentScope, fenced.fences, fixture.ports.lastProgress.ContentScope)
	}
}
