// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"sync"
)

// 设置特殊 pipeline 的流量缩放比例
var pipelineWeight = map[string]float64{
	TypeSystemBasereport: 1.5,
}

type pipelineMeta struct {
	TypeLabel string
	ETL       string
}

var (
	metaset = map[int]pipelineMeta{}
	metamut = sync.Mutex{}
)

func SetPipelineMeta(dataid int, typeLabel, etl string) {
	metamut.Lock()
	defer metamut.Unlock()

	metaset[dataid] = pipelineMeta{TypeLabel: typeLabel, ETL: etl}
}

func GetPipelineMeta(dataid int) (string, string) {
	metamut.Lock()
	defer metamut.Unlock()

	v, ok := metaset[dataid]
	if !ok {
		return "", ""
	}
	return v.TypeLabel, v.ETL
}

// GetPipelineWeight 支持对应不同的 pipeline 进行流量放大或者缩小
func GetPipelineWeight(k string) float64 {
	v, ok := pipelineWeight[k]
	if ok {
		return v
	}

	return 1.0 // 默认比例为 1
}
