// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package loginlog

import (
	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

type Record struct {
	Source string       `json:"source"`
	Logs   []UtmpEntity `json:"logs"`
}

type Event struct {
	DataID  int32
	Records []Record
}

func (e *Event) AsMapStr() common.MapStr {
	return common.MapStr{
		"data":   e.Records,
		"dataid": e.DataID,
	}
}

func (e *Event) IgnoreCMDBLevel() bool {
	return true
}

func (e *Event) GetType() string {
	return define.ModuleLoginLog
}
