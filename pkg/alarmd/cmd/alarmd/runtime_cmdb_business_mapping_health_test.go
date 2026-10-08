// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// businessMappingCells reads the business mapping gauge as mapping/state
// cells.
func businessMappingCells(t *testing.T, recorder *metric.Recorder) map[string]float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	cells := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_cmdb_index_business_mappings" {
			continue
		}
		for _, sample := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range sample.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			cells[labels["mapping"]+"/"+labels["state"]] = sample.GetGauge().GetValue()
		}
	}
	return cells
}

func assertBusinessMappingCells(t *testing.T, got, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("gauge = %v, want exactly %v", got, want)
	}
	for cell, value := range want {
		if got[cell] != value {
			t.Fatalf("gauge = %v, want %v", got, want)
		}
	}
}

// Each mapping's health reaches the gauge under its own name and each count
// in its own state: every count below is distinct, so a mapping published
// under the other's name, or two counts passed in each other's place, reads
// as a different cell.
func TestEachBusinessMappingIsPublishedUnderItsOwnName(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	publishCMDBBusinessMappings(recorder, cmdbcache.Health{
		ClusterBusinessMapping:   cmdbcache.MappingStats{Held: 11, Refused: 12, Truncated: 13},
		NamespaceBusinessMapping: cmdbcache.MappingStats{Held: 21, Refused: 22, Truncated: 23, ReadFailed: true, Emptied: true},
	})
	assertBusinessMappingCells(t, businessMappingCells(t, recorder), map[string]float64{
		"bcs_cluster/held": 11, "bcs_cluster/refused": 12, "bcs_cluster/truncated": 13, "bcs_cluster/read_failed": 0,
		"bcs_cluster/emptied": 0,
		"bcs_namespace/held":  21, "bcs_namespace/refused": 22, "bcs_namespace/truncated": 23, "bcs_namespace/read_failed": 1,
		"bcs_namespace/emptied": 1,
	})
}

type emptyIndexLoader struct{}

func (emptyIndexLoader) Load(context.Context, time.Time) (*cmdbcache.Index, error) {
	return &cmdbcache.Index{}, nil
}

// The index health published after every refresh carries the mappings: a
// load that holds neither mapping clears the cells an earlier one set.
func TestTheIndexHealthPublishedAfterARefreshCarriesTheMappings(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	recorder.SetCMDBBusinessMapping("bcs_cluster", 9, 9, 9, true, true)
	recorder.SetCMDBBusinessMapping("bcs_namespace", 9, 9, 9, true, true)
	store, err := cmdbcache.NewStore(emptyIndexLoader{}, cmdbcache.StoreOptions{RefreshInterval: time.Minute, MaxAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	publishCMDBIndexHealth(recorder, store)
	assertBusinessMappingCells(t, businessMappingCells(t, recorder), map[string]float64{
		"bcs_cluster/held": 0, "bcs_cluster/refused": 0, "bcs_cluster/truncated": 0, "bcs_cluster/read_failed": 0,
		"bcs_cluster/emptied": 0,
		"bcs_namespace/held":  0, "bcs_namespace/refused": 0, "bcs_namespace/truncated": 0, "bcs_namespace/read_failed": 0,
		"bcs_namespace/emptied": 0,
	})
}
