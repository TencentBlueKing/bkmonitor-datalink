// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package onemodel

import (
	"context"
	"errors"
	"testing"
)

type liveTargetsFake struct {
	rows                 []Instance
	present              bool
	err                  error
	services, topologies int
}

func (f *liveTargetsFake) ServiceInstances(context.Context, string, int64, string, []string) ([]Instance, error) {
	f.services++
	return f.rows, f.err
}

func (f *liveTargetsFake) TopologyInstances(context.Context, string, int64, ModelDefinition, string) ([]Instance, bool, error) {
	f.topologies++
	return f.rows, f.present, f.err
}

func TestExplicitServiceTargetsRequireLiveConcreteBusiness(t *testing.T) {
	d, p, desc := targetFixtures()
	d.model.ModelID = "services"
	d.model.CMDBObjectID = "service_instance"
	desc.ModelID = "services"
	desc.Selectors[0].Instances = []InstanceRef{{ModelID: "services", InstanceID: "1", EntityUID: "services|1"}}
	f := &liveTargetsFake{rows: []Instance{targetInstance("services", "1", 2)}}
	r := NewTargetResolver(d, p, nil, WithLiveTargetReader(f))
	got, err := r.Resolve(t.Context(), "t", "bkcc__2", desc)
	if err != nil || len(got.Instances) != 1 || f.services != 1 || len(p.queries) != 0 {
		t.Fatal("service lookup used ES or lost live result", err)
	}
	f.rows[0].TenantID = "other"
	if _, err := r.Resolve(t.Context(), "t", "bkcc__2", desc); err == nil {
		t.Fatal("cross-tenant live service accepted")
	}
	if _, err := NewTargetResolver(d, p, nil).Resolve(t.Context(), "t", "bkcc__2", desc); err == nil {
		t.Fatal("missing live reader treated as empty")
	}
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-biz", "2", 2)}}}
	before := f.services
	if _, err := r.Resolve(t.Context(), "t", "bkcc__99", desc); err == nil || f.services != before {
		t.Fatal("global service lookup guessed a concrete business")
	}
}

func TestLiveTopologyFallbackOnCacheEmptyOrError(t *testing.T) {
	for _, state := range []string{"hit", "empty", "missing", "error", "instance-missing"} {
		t.Run(state, func(t *testing.T) {
			d, p, desc := targetFixtures()
			desc.Selectors = []TargetSelector{{Type: "topo_node", Provider: "cmdb_mainline", TopologyNodeID: "t_module_8"}}
			p.pages = []Page{{Instances: []Instance{targetInstance("cw-Host", "cached", 2)}}}
			cached := targetTopologyFunc(func(context.Context, string, int64, string, string) ([]InstanceRef, bool, error) {
				switch state {
				case "empty":
					return []InstanceRef{}, true, nil
				case "missing":
					return nil, false, nil
				case "error":
					return nil, false, errors.New("projection down")
				case "instance-missing":
					p.pages = []Page{{Instances: []Instance{}}}
				}
				return []InstanceRef{targetRef("cached")}, true, nil
			})
			live := &liveTargetsFake{rows: []Instance{targetInstance("cw-Host", "live", 2)}, present: true}
			r := NewTargetResolver(d, p, cached, WithLiveTargetReader(live))
			got, err := r.Resolve(t.Context(), "t", "bkcc__2", desc)
			if err != nil || len(got.Instances) != 1 {
				t.Fatal(err)
			}
			want := "live"
			calls := 1
			if state == "hit" {
				want = "cached"
				calls = 0
			}
			if got.Instances[0].InstanceID != want || live.topologies != calls {
				t.Fatal("wrong cache/fallback selection")
			}
		})
	}
}

func TestLiveTopologyRejectsPartialAndKeepsValidEmpty(t *testing.T) {
	for _, kind := range []string{"empty", "not-found", "error", "foreign", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			d, p, desc := targetFixtures()
			desc.Selectors = []TargetSelector{{Type: "topo_node", Provider: "cmdb_mainline", TopologyNodeID: "t_module_8"}}
			live := &liveTargetsFake{rows: []Instance{}, present: true}
			switch kind {
			case "not-found":
				live.present = false
			case "error":
				live.err = errors.New("incomplete page")
			case "foreign":
				live.rows = []Instance{targetInstance("cw-Host", "1", 8)}
			case "duplicate":
				live.rows = []Instance{targetInstance("cw-Host", "1", 2), targetInstance("cw-Host", "1", 2)}
			}
			got, err := NewTargetResolver(d, p, nil, WithLiveTargetReader(live)).Resolve(t.Context(), "t", "bkcc__2", desc)
			if kind == "empty" {
				if err != nil || len(got.Instances) != 0 {
					t.Fatal("valid empty failed", err)
				}
			} else if err == nil {
				t.Fatal("incomplete direct result accepted")
			}
		})
	}
}
