// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type LabelsHash uint64

type Label struct {
	Key   int64
	Value int64
}

type Labels struct {
	Items []*Label
}

func (l Labels) Len() int { return len(l.Items) }

func (l Labels) Less(i, j int) bool { return l.Items[i].Key < l.Items[j].Key }

func (l Labels) Swap(i, j int) { l.Items[i], l.Items[j] = l.Items[j], l.Items[i] }

func (l Labels) Hash() LabelsHash {
	hasher := fnv.New64a()
	sort.Sort(l)
	for _, x := range l.Items {
		if x.Value == 0 {
			continue
		}
		pair := fmt.Sprintf("%d:%d", x.Key, x.Value)
		if _, err := hasher.Write([]byte(pair)); err != nil {
			logger.Infof("write pair(%d:%d) to hasher failed, error: %s", x.Key, x.Value, err)
		}
	}

	return LabelsHash(hasher.Sum64())
}

func NewLabels(items []*Label) Labels {
	i := items
	if i == nil {
		i = make([]*Label, 0)
	}

	return Labels{
		Items: i,
	}
}
