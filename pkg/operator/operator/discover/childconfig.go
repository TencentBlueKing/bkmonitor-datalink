// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	"fmt"
	"hash/fnv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

// ChildConfig 子任务配置文件信息
type ChildConfig struct {
	Meta         define.MonitorMeta
	Node         string
	FileName     string
	Address      string
	Data         []byte
	Scheme       string
	Path         string
	Mask         string
	TaskType     string
	Namespace    string
	AntiAffinity bool
}

func (c ChildConfig) String() string {
	return fmt.Sprintf("Node=%s, FileName=%s, Address=%s", c.Node, c.FileName, c.Address)
}

func (c ChildConfig) Hash() uint64 {
	h := fnv.New64a()
	h.Write([]byte(c.Node))
	h.Write(c.Data)
	h.Write([]byte(c.Mask))
	return h.Sum64()
}
