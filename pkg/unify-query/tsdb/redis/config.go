// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"github.com/go-gota/gota/dataframe"
	"github.com/go-gota/gota/series"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// QueryAggreToDataframeMapping DirectQuery 结构体与 goDataframe 聚合方法映射关系
var QueryAggreToDataframeMapping = map[string]dataframe.AggregationType{
	// 序列聚合方法
	structured.MaxAggName:    dataframe.Aggregation_MAX,
	structured.MinAggName:    dataframe.Aggregation_MIN,
	structured.MeanAggName:   dataframe.Aggregation_MEAN,
	structured.StddevAggName: dataframe.Aggregation_STD,
	structured.SumAggName:    dataframe.Aggregation_SUM,
	structured.CountAggName:  dataframe.Aggregation_COUNT,
	// 时间聚合方法
	structured.MaxOT:   dataframe.Aggregation_MAX,
	structured.MinOT:   dataframe.Aggregation_MIN,
	structured.SumOT:   dataframe.Aggregation_SUM,
	structured.CountOT: dataframe.Aggregation_COUNT,
	structured.AvgOT:   dataframe.Aggregation_MEAN,
}

// QueryConditionToDataframeComparator DirectQuery 结构体与 goDataframe 过滤条件映射关系
var QueryConditionToDataframeComparator = map[string]series.Comparator{
	structured.ConditionEqual: series.In,
}

const (
	ClusterMetricKey              = "cluster_metrics"
	ClusterMetricMetaKey          = "cluster_metrics_meta"
	ClusterMetricFieldPattern     = "{bkm_metric_name}|bkm_cluster={bkm_cluster}"
	ClusterMetricFieldClusterName = "bkm_cluster"
	ClusterMetricFieldMetricName  = "bkm_metric_name"
	ClusterMetricFieldValName     = "value"
	ClusterMetricFieldTimeName    = "time"
)
