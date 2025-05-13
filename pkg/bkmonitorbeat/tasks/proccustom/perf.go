// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proccustom

import (
	"fmt"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

type perfEvent struct {
	stat     define.ProcStat
	procName string
	username string
	dims     map[string]string
	tags     map[string]string
	labels   []map[string]string
	reported []string
}

func (e perfEvent) getDims() map[string]string {
	ret := make(map[string]string)
	for k, v := range e.tags {
		ret[k] = v
	}
	for k, v := range e.dims {
		ret[k] = v
	}
	return ret
}

func (e perfEvent) AsMapStr() []common.MapStr {
	ret := make([]common.MapStr, 0)
	for _, label := range e.labels {
		stat := make(common.MapStr)
		if e.stat.Mem != nil {
			stat["memory_size"] = e.stat.Mem.Size
			stat["memory_rss_bytes"] = e.stat.Mem.Resident
			stat["memory_rss_pct"] = e.stat.Mem.Percent
			stat["memory_share"] = e.stat.Mem.Share
		}

		if e.stat.CPU != nil {
			stat["cpu_start_time"] = e.stat.CPU.StartTime
			stat["cpu_user"] = e.stat.CPU.User
			stat["cpu_system"] = e.stat.CPU.Sys
			stat["cpu_total_ticks"] = e.stat.CPU.Total
			stat["cpu_total_pct"] = e.stat.CPU.Percent
		}

		if e.stat.Fd != nil {
			stat["fd_open"] = e.stat.Fd.Open
			stat["fd_limit_soft"] = e.stat.Fd.SoftLimit
			stat["fd_limit_hard"] = e.stat.Fd.HardLimit
		}

		if e.stat.IO != nil {
			stat["io_read_bytes"] = e.stat.IO.ReadBytes
			stat["io_write_bytes"] = e.stat.IO.WriteBytes
			stat["io_read_speed"] = e.stat.IO.ReadSpeed
			stat["io_write_speed"] = e.stat.IO.WriteSpeed
		}

		if len(e.reported) > 0 {
			for _, metric := range e.reported {
				if _, ok := stat[metric]; !ok {
					delete(stat, metric)
				}
			}
		}

		dimensions := make(common.MapStr)
		for k, v := range e.getDims() {
			dimensions[k] = v
		}
		dimensions["pid"] = fmt.Sprintf("%d", e.stat.Pid)
		dimensions["process_name"] = e.procName
		dimensions["process_username"] = e.username

		cloudID, ok := label["bk_target_cloud_id"]
		if !ok {
			continue
		}
		ip, ok := label["bk_target_ip"]
		if !ok {
			continue
		}

		for k, v := range label {
			dimensions[k] = v
		}

		ret = append(ret, common.MapStr{
			"timestamp": time.Now().Unix(),
			"target":    fmt.Sprintf("%s:%s", cloudID, ip),
			"dimension": dimensions,
			"metrics":   stat,
		})
	}
	return ret
}
