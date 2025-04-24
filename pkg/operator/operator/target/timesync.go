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
	"fmt"
	"hash/fnv"
	"sort"

	"gopkg.in/yaml.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

// TimeSyncTarget 时间同步采集项
type TimeSyncTarget struct {
	DataID int
	Labels map[string]string
}

func (t *TimeSyncTarget) FileName() string {
	b, _ := t.YamlBytes()
	return fmt.Sprintf("timesync-%d.conf", fnvHash(b))
}

func (t *TimeSyncTarget) YamlBytes() ([]byte, error) {
	timesync := configs.G().TimeSync

	cfg := make(yaml.MapSlice, 0)
	cfg = append(cfg, yaml.MapItem{Key: "type", Value: "timesync"})
	cfg = append(cfg, yaml.MapItem{Key: "name", Value: "timesync_collect"})
	cfg = append(cfg, yaml.MapItem{Key: "version", Value: "1"})
	cfg = append(cfg, yaml.MapItem{Key: "task_id", Value: t.generateTaskID()})
	cfg = append(cfg, yaml.MapItem{Key: "dataid", Value: t.DataID})
	cfg = append(cfg, yaml.MapItem{Key: "period", Value: "1m"})
	cfg = append(cfg, yaml.MapItem{Key: "metric_prefix", Value: "kube"})
	cfg = append(cfg, yaml.MapItem{Key: "ntpd_path", Value: timesync.NtpdPath})
	cfg = append(cfg, yaml.MapItem{Key: "query_timeout", Value: timesync.QueryTimeout})
	cfg = append(cfg, yaml.MapItem{Key: "chrony_address", Value: timesync.ChronyAddress})
	cfg = append(cfg, yaml.MapItem{Key: "labels", Value: []yaml.MapSlice{sortMap(t.Labels)}})
	return yaml.Marshal(cfg)
}

func (t *TimeSyncTarget) generateTaskID() uint64 {
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("%d", t.DataID)))

	timesync := configs.G().TimeSync
	h.Write([]byte(timesync.NtpdPath))
	h.Write([]byte(timesync.QueryTimeout))
	h.Write([]byte(timesync.ChronyAddress))

	keys := make([]string, 0, len(t.Labels))
	for key := range t.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		h.Write([]byte(key))
		h.Write([]byte(t.Labels[key]))
	}
	return avoidOverflow(h.Sum64())
}
