// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package target

import (
	"gopkg.in/yaml.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

// EventTarget 事件采集配置
type EventTarget struct {
	DataID          int
	UpMetricsDataID int
	Labels          map[string]string
}

func (t *EventTarget) FileName() string {
	return "kubernetes-event.conf"
}

func (t *EventTarget) YamlBytes() ([]byte, error) {
	cfg := make(yaml.MapSlice, 0)

	cfg = append(cfg, yaml.MapItem{Key: "type", Value: "kubeevent"})
	cfg = append(cfg, yaml.MapItem{Key: "name", Value: "event_collect"})
	cfg = append(cfg, yaml.MapItem{Key: "version", Value: "1"})
	cfg = append(cfg, yaml.MapItem{Key: "task_id", Value: 1})
	cfg = append(cfg, yaml.MapItem{Key: "dataid", Value: t.DataID})
	cfg = append(cfg, yaml.MapItem{Key: "upmetrics_dataid", Value: t.UpMetricsDataID})
	cfg = append(cfg, yaml.MapItem{Key: "interval", Value: configs.G().Event.Interval})
	cfg = append(cfg, yaml.MapItem{Key: "tail_files", Value: configs.G().Event.TailFiles})
	cfg = append(cfg, yaml.MapItem{Key: "labels", Value: []yaml.MapSlice{sortMap(t.Labels)}})
	return yaml.Marshal(cfg)
}
