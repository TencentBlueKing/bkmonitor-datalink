// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dmesg

import (
	"fmt"
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
)

type Event struct {
	DataID     int32
	Exceptions []wrapException
}

type wrapException struct {
	Name    string
	Message string
	Time    time.Time
}

func newEvent(dataid int32, exceptions []wrapException) *Event {
	return &Event{
		DataID:     dataid,
		Exceptions: exceptions,
	}
}

func (e *Event) AsMapStr() common.MapStr {
	info, _ := gse.GetAgentInfo()
	data := make([]common.MapStr, 0, len(e.Exceptions))
	for _, exception := range e.Exceptions {
		data = append(data, common.MapStr{
			"event_name": exception.Name,
			"target":     info.IP,
			"event": common.MapStr{
				"content": exception.Message,
			},
			"dimension": common.MapStr{
				"bk_cloud_id":  strconv.Itoa(int(info.Cloudid)),
				"bk_target_ip": info.IP,
				"bk_agent_id":  info.BKAgentID,
				"bk_host_id":   strconv.Itoa(int(info.HostID)),
				"bk_biz_id":    strconv.Itoa(int(info.BKBizID)),
				"node_id":      fmt.Sprintf("%d:%s", info.Cloudid, info.IP),
				"hostname":     info.Hostname,
			},
			"timestamp": exception.Time.UnixMilli(),
		})
	}

	return common.MapStr{
		"dataid": e.DataID,
		"data":   data,
	}
}

func (e *Event) GetType() string {
	return define.ModuleDmesg
}

func (e *Event) IgnoreCMDBLevel() bool {
	return true
}
