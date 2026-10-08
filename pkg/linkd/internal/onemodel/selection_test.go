// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import "testing"

func validDescriptor() TargetDescriptor {
	return TargetDescriptor{SchemaVersion: 1, ModelID: "cmdb.host", Selectors: []TargetSelector{{Type: "instances", Instances: []InstanceRef{{ModelID: "cmdb.host", InstanceID: "opaque:42", EntityUID: "cmdb.host|opaque:42"}}}}}
}

func TestTargetDescriptorV1(t *testing.T) {
	biz := int64(2)
	for _, selector := range []TargetSelector{validDescriptor().Selectors[0], {Type: "topo_node", Provider: "cmdb_mainline", TopologyNodeID: "module:3", BizID: &biz}, {Type: "dynamic_group", Provider: "kingeye", DynamicGroupID: "group-1"}} {
		d := validDescriptor()
		d.Selectors = []TargetSelector{selector}
		if err := d.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*TargetDescriptor){func(d *TargetDescriptor) { d.SchemaVersion = 2 }, func(d *TargetDescriptor) { d.ModelID = "wrong" }, func(d *TargetDescriptor) { d.Selectors = nil }, func(d *TargetDescriptor) { d.Selectors[0].Instances[0].EntityUID = "cmdb.host|42" }, func(d *TargetDescriptor) { d.Selectors[0].Instances[0].InstanceID = "" }, func(d *TargetDescriptor) { d.Selectors[0].Provider = "kingeye" }, func(d *TargetDescriptor) {
		d.Selectors = []TargetSelector{{Type: "dynamic_group", Provider: "cmdb", DynamicGroupID: "g"}}
	}, func(d *TargetDescriptor) {
		zero := int64(0)
		d.Selectors = []TargetSelector{{Type: "topo_node", Provider: "cmdb_mainline", TopologyNodeID: "host:1", BizID: &zero}}
	}} {
		d := validDescriptor()
		change(&d)
		if err := d.Validate(); err == nil {
			t.Fatal("invalid descriptor accepted")
		}
	}
	d := validDescriptor()
	instances := make([]InstanceRef, 2000)
	for i := range instances {
		instances[i] = d.Selectors[0].Instances[0]
	}
	d.Selectors = make([]TargetSelector, 6)
	for i := range d.Selectors {
		d.Selectors[i] = TargetSelector{Type: "instances", Instances: instances}
	}
	if err := d.Validate(); err == nil {
		t.Fatal("instance count limit ignored")
	}
}
