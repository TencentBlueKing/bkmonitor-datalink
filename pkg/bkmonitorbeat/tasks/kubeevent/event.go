// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kubeevent

import (
	"crypto/md5"
	"fmt"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type k8sEventSource struct {
	Component string `json:"component"`
	Host      string `json:"host"`
}

type k8sEventInvolvedObject struct {
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	ApiVersion string `json:"apiVersion"`
}

type k8sEventMetadata struct {
	Uid string `json:"uid"`
}

type k8sSeries struct {
	// count is the number of occurrences in this series up to the last heartbeat time.
	Count int `json:"count"`
	// lastObservedTime is the time when last Event from the series was seen before last heartbeat.
	LastObservedTime string `json:"lastObservedTime"`
}

type k8sEvent struct {
	Reason         string                 `json:"reason"`
	Message        string                 `json:"message"`
	Source         k8sEventSource         `json:"source"`
	Metadata       k8sEventMetadata       `json:"metadata"`
	FirstTs        string                 `json:"firstTimestamp"`
	LastTs         string                 `json:"lastTimestamp"`
	EventTs        string                 `json:"eventTime"` // eventTime is the time when this Event was first observed. It is required.
	Count          int                    `json:"count"`
	InvolvedObject k8sEventInvolvedObject `json:"involvedObject"`
	Type           string                 `json:"type"`
	Series         *k8sSeries             `json:"series"`

	// 事件存在一个聚合时间窗口 windowL 为窗口左边的 Count windowR 为窗口右边的 Count
	windowL int
	windowR int
}

func (e *k8sEvent) Clone() *k8sEvent {
	cloned := *e
	return &cloned
}

func (e *k8sEvent) Hash() string {
	s := fmt.Sprintf("%s/%s/%s/%s/%s",
		e.Reason,
		e.InvolvedObject.Kind,
		e.InvolvedObject.Namespace,
		e.InvolvedObject.Name,
		e.Metadata.Uid,
	)

	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

func (e *k8sEvent) GetTarget() string {
	if e.Source.Component != "" {
		return e.Source.Component
	}
	return "kubelet"
}

func parseTimeLayout(s string) int64 {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		logger.Errorf("failed to parse Ts: %s, err: %v", s, err)
		return time.Now().Unix()
	}

	return t.Unix()
}

func (e *k8sEvent) GetFirstTime() int64 {
	if e.FirstTs != "" {
		return parseTimeLayout(e.FirstTs)
	}
	return parseTimeLayout(e.EventTs)
}

func (e *k8sEvent) GetLastTime() int64 {
	if e.LastTs != "" {
		return parseTimeLayout(e.LastTs)
	}
	if e.Series != nil {
		return parseTimeLayout(e.Series.LastObservedTime)
	}
	return time.Now().Unix()
}

func (e *k8sEvent) GetCount() int {
	if e.Count > 0 {
		return e.Count
	}
	if e.Series != nil {
		return e.Series.Count
	}
	return 1
}

func (e *k8sEvent) IsZeroTime() bool {
	return e.FirstTs == "" && e.LastTs == "" && e.EventTs == ""
}

type wrapEvent struct {
	k8sEvent
	dataID         int32
	externalLabels []map[string]string
}

func newWrapEvent(dataID int32, externalLabels []map[string]string, e k8sEvent) *wrapEvent {
	return &wrapEvent{
		k8sEvent:       e,
		dataID:         dataID,
		externalLabels: externalLabels,
	}
}

func (e *wrapEvent) AsMapStr() common.MapStr {
	dimensions := common.MapStr{
		"kind":       e.InvolvedObject.Kind,
		"namespace":  e.InvolvedObject.Namespace,
		"name":       e.InvolvedObject.Name,
		"apiVersion": e.InvolvedObject.ApiVersion,
		"uid":        e.Metadata.Uid,
		"host":       e.Source.Host,
		"type":       e.Type,
	}
	for i := 0; i < len(e.externalLabels); i++ {
		for k, v := range e.externalLabels[i] {
			dimensions[k] = v
		}
	}

	data := common.MapStr{
		"event_name": e.Reason,
		"target":     e.GetTarget(),
		"event": common.MapStr{
			"content": e.Message,
			"count":   e.Count,
		},
		"dimension": dimensions,
		"timestamp": e.GetLastTime() * 1000, // ms
	}

	return common.MapStr{
		"dataid": e.dataID,
		"data":   []common.MapStr{data},
	}
}

func (e *wrapEvent) IgnoreCMDBLevel() bool {
	return true
}

func (e *wrapEvent) GetType() string {
	return define.ModuleKubeevent
}
