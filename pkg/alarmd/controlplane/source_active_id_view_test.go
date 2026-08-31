// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplane_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func TestActiveIDViewReadsCanonicalObjectsBeforeNarrowingExecution(t *testing.T) {
	source := &recordingStrategySource{active: []string{"1003", "1001", "1002"}}
	view, err := controlplane.NewActiveIDStrategySourceView(source, []string{"1003", "1001"})
	if err != nil {
		t.Fatalf("NewActiveIDStrategySourceView() error = %v", err)
	}

	ids, err := view.ActiveStrategyIDs(context.Background())
	if err != nil {
		t.Fatalf("ActiveStrategyIDs() error = %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"1001", "1002", "1003"}) {
		t.Fatalf("active IDs = %v, want complete canonical set", ids)
	}
	if source.activeReads != 1 {
		t.Fatalf("canonical active-set reads = %d, want 1", source.activeReads)
	}

	strategies, err := view.Strategies(context.Background(), ids)
	if err != nil {
		t.Fatalf("Strategies() error = %v", err)
	}
	if !reflect.DeepEqual(source.requested, []string{"1001", "1002", "1003"}) || len(strategies) != 3 {
		t.Fatalf("underlying strategy request = %v, strategies = %v", source.requested, strategies)
	}
	selected, audit, err := view.SelectExecutionStrategies(strategies)
	if err != nil {
		t.Fatalf("SelectExecutionStrategies() error = %v", err)
	}
	if len(selected) != 2 || selected[0].SourceID != "1001" || selected[1].SourceID != "1003" {
		t.Fatalf("selected strategies = %#v", selected)
	}
	if len(audit) != 0 {
		t.Fatalf("unselected audit = %#v", audit)
	}
}

func TestActiveIDViewRejectsMissingSelectedIdentity(t *testing.T) {
	source := &recordingStrategySource{active: []string{"1001", "1002"}}
	view, err := controlplane.NewActiveIDStrategySourceView(source, []string{"1001", "1003"})
	if err != nil {
		t.Fatalf("NewActiveIDStrategySourceView() error = %v", err)
	}

	if _, err := view.ActiveStrategyIDs(context.Background()); !errors.Is(err, controlplane.ErrSelectedStrategyUnavailable) {
		t.Fatalf("ActiveStrategyIDs() error = %v, want selected identity unavailable", err)
	}
}

func TestActiveIDViewRejectsInvalidCanonicalSetBeforeSelection(t *testing.T) {
	source := &recordingStrategySource{active: []string{"1001", "1001"}}
	view, err := controlplane.NewActiveIDStrategySourceView(source, []string{"1001"})
	if err != nil {
		t.Fatalf("NewActiveIDStrategySourceView() error = %v", err)
	}

	if _, err := view.ActiveStrategyIDs(context.Background()); err == nil {
		t.Fatal("ActiveStrategyIDs() accepted an invalid canonical active set")
	}
}

func TestActiveIDViewRejectsIncompleteSelectedObject(t *testing.T) {
	source := &recordingStrategySource{active: []string{"1001", "1002"}}
	view, err := controlplane.NewActiveIDStrategySourceView(source, []string{"1001"})
	if err != nil {
		t.Fatalf("NewActiveIDStrategySourceView() error = %v", err)
	}

	strategies := []controlplane.SourceStrategy{
		{SourceID: "1001", SourceDisposition: &controlplane.ObjectDisposition{
			SourceID: "1001", Scope: "STRATEGY", Disposition: controlplane.DispositionSourceIncomplete,
			Reason: "SOURCE_OBJECT_INCOMPLETE",
		}},
		{SourceID: "1002", Document: []byte(`{"id":1002}`)},
	}
	if _, _, err := view.SelectExecutionStrategies(strategies); !errors.Is(err, controlplane.ErrSelectedStrategyUnavailable) {
		t.Fatalf("SelectExecutionStrategies() error = %v, want selected object rejection", err)
	}
}

func TestActiveIDViewAuditsIncompleteUnselectedObjectWithoutCompilingIt(t *testing.T) {
	source := &recordingStrategySource{active: []string{"1001", "1002"}}
	view, err := controlplane.NewActiveIDStrategySourceView(source, []string{"1001"})
	if err != nil {
		t.Fatalf("NewActiveIDStrategySourceView() error = %v", err)
	}
	strategies := []controlplane.SourceStrategy{
		{SourceID: "1001", Document: []byte(`{"id":1001}`)},
		{SourceID: "1002", SourceDisposition: &controlplane.ObjectDisposition{
			SourceID: "1002", Scope: "STRATEGY", Disposition: controlplane.DispositionSourceIncomplete,
			Reason: "SOURCE_OBJECT_INCOMPLETE",
		}},
	}
	selected, audit, err := view.SelectExecutionStrategies(strategies)
	if err != nil {
		t.Fatalf("SelectExecutionStrategies() error = %v", err)
	}
	if len(selected) != 1 || selected[0].SourceID != "1001" {
		t.Fatalf("selected strategies = %#v", selected)
	}
	if len(audit) != 1 || audit[0].SourceID != "1002" ||
		audit[0].Disposition != controlplane.DispositionSourceIncomplete {
		t.Fatalf("unselected source audit = %#v", audit)
	}
}

type recordingStrategySource struct {
	active      []string
	activeReads int
	requested   []string
}

func (source *recordingStrategySource) ActiveStrategyIDs(context.Context) ([]string, error) {
	source.activeReads++
	return append([]string(nil), source.active...), nil
}

func (source *recordingStrategySource) Strategies(_ context.Context, ids []string) ([]controlplane.SourceStrategy, error) {
	source.requested = append([]string(nil), ids...)
	result := make([]controlplane.SourceStrategy, 0, len(ids))
	for _, id := range ids {
		result = append(result, controlplane.SourceStrategy{SourceID: id, Document: []byte(`{"id":1}`)})
	}
	return result, nil
}
