// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

type Filter map[string]string

// TsDBV2 适配查询语句的结构体，以 TableID + MetricName 为条件，检索出 RT 基本信息和存储信息
type TsDBV2 struct {
	TableID         string   `json:"table_id"`
	Field           []string `json:"field"`
	MeasurementType string   `json:"measurement_type,omitempty"`
	Filters         []Filter `json:"filters"`
	SegmentedEnable bool     `json:"segmented_enable,omitempty"`
	DataLabel       string   `json:"data_label,omitempty"`
	// 将存储信息合并在 TsDB 中
	StorageID   string   `json:"storage_id,omitempty"`
	ClusterName string   `json:"cluster_name"`
	TagsKey     []string `json:"tags_key"`
	DB          string   `json:"db"`
	Measurement string   `json:"measurement"`
	VmRt        string   `json:"vm_rt,omitempty"`
	// 补充检索的元信息
	MetricName        string   `json:"metric_name"`
	ExpandMetricNames []string `json:"expand_metric_names"`
}

func (z *TsDBV2) IsSplit() bool {
	return z.MeasurementType == redis.BkSplitMeasurement
}

func (z *TsDBV2) String() string {
	return fmt.Sprintf("dataLabel:%v,tableID:%v,field:%s,measurementType:%s,segmentedEnable:%v,filter:%+v",
		z.DataLabel, z.TableID, z.Field, z.MeasurementType, z.SegmentedEnable, z.Filters,
	)
}
