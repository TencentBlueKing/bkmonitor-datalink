// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"fmt"
	"sort"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
)

type Recorder struct {
	mut              sync.Mutex
	activeConfigFile map[string]ConfigFileRecord
	seenMetaID       map[string]struct{}
}

func newRecorder() *Recorder {
	return &Recorder{
		activeConfigFile: make(map[string]ConfigFileRecord),
		seenMetaID:       make(map[string]struct{}),
	}
}

type ConfigFileRecord struct {
	Service  string             `json:"service"`
	DataID   int                `json:"dataid"`
	FileName string             `json:"filename"`
	Node     string             `json:"node"`
	Meta     define.MonitorMeta `json:"-"`
	Address  string             `json:"-"`
	Target   string             `json:"-"`
}

type MonitorResourceRecord struct {
	Kind      string                  `json:"kind"`
	Namespace string                  `json:"namespace"`
	Name      string                  `json:"name"`
	Index     int                     `json:"index"`
	Count     int                     `json:"count"`
	Location  []MonitorLocationRecord `json:"location"`
}

type MonitorLocationRecord struct {
	Address string `json:"address"`
	Node    string `json:"node"`
	Target  string `json:"target"`
	DataID  int    `json:"dataid"`
}

func newConfigFileRecord(dis discover.Discover, cfg *discover.ChildConfig) ConfigFileRecord {
	return ConfigFileRecord{
		Service:  dis.MonitorMeta().ID(),
		Meta:     dis.MonitorMeta(),
		DataID:   dis.DataID().Spec.DataID,
		FileName: cfg.FileName,
		Node:     cfg.Node,
		Address:  cfg.Address,
		Target:   fmt.Sprintf("%s://%s%s", cfg.Scheme, cfg.Address, cfg.Path),
	}
}

func (r *Recorder) updateConfigFiles(cfgs []ConfigFileRecord) {
	r.mut.Lock()
	defer r.mut.Unlock()

	cfgMap := make(map[string]ConfigFileRecord)
	for _, cfg := range cfgs {
		cfgMap[cfg.FileName] = cfg
		r.seenMetaID[cfg.Meta.ID()] = struct{}{}
	}
	r.activeConfigFile = cfgMap
}

func (r *Recorder) updateConfigNode(filename, node string) {
	r.mut.Lock()
	defer r.mut.Unlock()

	cfg, ok := r.activeConfigFile[filename]
	if !ok || cfg.Node != define.UnknownNode {
		return
	}
	cfg.Node = node
	r.activeConfigFile[filename] = cfg
}

func (r *Recorder) getActiveConfigFiles() []ConfigFileRecord {
	r.mut.Lock()
	defer r.mut.Unlock()

	cfgs := make([]ConfigFileRecord, 0, len(r.activeConfigFile))
	for _, cfg := range r.activeConfigFile {
		cfgs = append(cfgs, cfg)
	}

	sort.Slice(cfgs, func(i, j int) bool {
		return cfgs[i].Meta.ID() < cfgs[j].Meta.ID()
	})
	return cfgs
}

func (r *Recorder) getEndpoints(active bool) map[string]int {
	r.mut.Lock()
	defer r.mut.Unlock()

	ret := make(map[string]int)
	for _, cfg := range r.activeConfigFile {
		ret[cfg.Meta.ID()]++
	}

	// active 只返回现在正在监听的 discover
	if active {
		return ret
	}

	// 曾经记录过的 id 如果已经被删除则记为 0 避免自监控数据只增不减的情况
	dropped := make(map[string]struct{})
	for id := range r.seenMetaID {
		if _, ok := ret[id]; !ok {
			dropped[id] = struct{}{}
		}
	}
	for dropID := range dropped {
		ret[dropID] = 0
	}

	return ret
}

func (r *Recorder) getMonitorResources() []MonitorResourceRecord {
	r.mut.Lock()
	defer r.mut.Unlock()

	type identify struct {
		kind, namespace, name string
		index                 int
	}
	set := make(map[identify]map[MonitorLocationRecord]struct{})
	for _, cfg := range r.activeConfigFile {
		k := identify{
			kind:      cfg.Meta.Kind,
			namespace: cfg.Meta.Namespace,
			name:      cfg.Meta.Name,
			index:     cfg.Meta.Index,
		}
		if _, ok := set[k]; !ok {
			set[k] = map[MonitorLocationRecord]struct{}{}
		}
		set[k][MonitorLocationRecord{
			Address: cfg.Address,
			Node:    cfg.Node,
			Target:  cfg.Target,
			DataID:  cfg.DataID,
		}] = struct{}{}
	}

	ret := make([]MonitorResourceRecord, 0)
	for k, location := range set {
		mr := MonitorResourceRecord{
			Kind:      k.kind,
			Namespace: k.namespace,
			Name:      k.name,
			Index:     k.index,
			Count:     len(location),
		}
		for l := range location {
			mr.Location = append(mr.Location, MonitorLocationRecord{
				Address: l.Address,
				Node:    l.Node,
				Target:  l.Target,
				DataID:  l.DataID,
			})
		}
		ret = append(ret, mr)
	}
	return ret
}
