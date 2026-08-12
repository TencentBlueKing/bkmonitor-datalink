// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"fmt"
	"strconv"

	gsetype "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
)

func HostDimension(info gsetype.AgentInfo) map[string]string {
	cloudID := strconv.Itoa(int(info.Cloudid))
	return map[string]string{
		"bk_cloud_id":        cloudID,
		"bk_target_cloud_id": cloudID,
		"bk_target_ip":       info.IP,
		"bk_agent_id":        info.BKAgentID,
		"bk_host_id":         strconv.Itoa(int(info.HostID)),
		"bk_biz_id":          strconv.Itoa(int(info.BKBizID)),
		"node_id":            fmt.Sprintf("%d:%s", info.Cloudid, info.IP),
		"hostname":           info.Hostname,
	}
}
