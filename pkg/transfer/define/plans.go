// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"strings"
)

// PairDispatchInfo pair从metadata分发到shadow路径的信息载体
type PairDispatchInfo struct {
	// Source metadata路径下的dataid信息
	// Target 映射到的shadow路径下的dataid信息
	Source, Target string
	// Version 对应的是consul-metadata路径的modify_index，用来确认元数据是否发生变化
	Version uint64
}

// ServiceDispatchInfo :
type ServiceDispatchInfo struct {
	Service string
}

// ServiceDispatchPlan :
type ServiceDispatchPlan struct {
	*ServiceDispatchInfo
	// 此处的key为data_id的在consul上的原路径
	Pairs map[string]*PairDispatchInfo
}

// String
func (p *ServiceDispatchPlan) String() string {
	var builder strings.Builder
	for key := range p.Pairs {
		builder.WriteString(p.Service)
		builder.WriteString(": ")
		builder.WriteString(key)
		builder.WriteString("\n")
	}
	return builder.String()
}

// NewServiceDispatchPlan :
func NewServiceDispatchPlan(service string) *ServiceDispatchPlan {
	return &ServiceDispatchPlan{
		ServiceDispatchInfo: &ServiceDispatchInfo{
			Service: service,
		},
		Pairs: make(map[string]*PairDispatchInfo),
	}
}

type PlanWithFlows struct {
	Plans map[string]*ServiceDispatchPlan // key:service
	IDers IDerMapDetailed
	Flows map[string]float64 // key:dataid; value:flow percent by service
}

type IDerMapDetailed struct {
	All      map[IDer][]IDer
	WithFlow map[int][]IDer
}

func NewIDerMapDetailed() IDerMapDetailed {
	return IDerMapDetailed{
		All:      make(map[IDer][]IDer),
		WithFlow: make(map[int][]IDer),
	}
}

func NewPlanWithFlows() PlanWithFlows {
	return PlanWithFlows{
		Plans: make(map[string]*ServiceDispatchPlan),
		IDers: NewIDerMapDetailed(),
		Flows: make(map[string]float64),
	}
}
