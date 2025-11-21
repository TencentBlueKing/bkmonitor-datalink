// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"sort"
	"strings"
)

type Node struct {
	Uuid string
	Data [][2]string
}

func NewNode(info map[string]string) *Node {
	keys := make([]string, 0, len(info))
	for k := range info {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var uuid strings.Builder
	data := make([][2]string, 0, len(info))
	for _, k := range keys {
		uuid.WriteString(k + "|" + info[k] + "|")
		data = append(data, [2]string{k, info[k]})
	}
	return &Node{
		Uuid: uuid.String(),
		Data: data,
	}
}
