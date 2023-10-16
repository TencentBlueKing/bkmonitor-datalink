// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"encoding/json"
	"strings"

	"github.com/jinzhu/gorm"
)

const (
	BkmSuffixKey     = "bkm_suffix"
	BkmMetricNameKey = "bkm_metric_name"
	BkmClusterKey    = "bkm_cluster"
	BkmHostnameKey   = "bkm_hostname"
)

//go:generate goqueryset -in clustermetric.go -out qs_clustermetric_gen.go

// ClusterMetric 集群状态指标配置表
// gen:qs
type ClusterMetric struct {
	MetricName string   `json:"metric_name" gorm:"size:512"`
	Tags       string   `json:"tags" gorm:"column:tags;type:json"`
	TagsParsed []string `json:"-" gorm:"-"`
}

func (cm *ClusterMetric) AfterFind(tx *gorm.DB) error {
	err := json.Unmarshal([]byte(cm.Tags), &cm.TagsParsed)
	return err
}

// TableName  用于设置表的别名
func (*ClusterMetric) TableName() string {
	return "metadata_clustermetric"
}

func (qs ClusterMetricQuerySet) InfluxdbQS() ClusterMetricQuerySet {
	return qs.MetricNameLike("influxdb.%")
}

func (qs ClusterMetricQuerySet) InfluxdbProxyQS() ClusterMetricQuerySet {
	return qs.MetricNameLike("influxdb_proxy.%")
}

func (cm *ClusterMetric) GetBkmTags() []string {
	var arr []string
	for _, t := range cm.TagsParsed {
		if strings.HasPrefix(t, "bkm_") {
			arr = append(arr, t)
		}
	}
	return arr
}

func (cm *ClusterMetric) IsInTags(tTag string) bool {
	for _, tag := range cm.TagsParsed {
		if tag == tTag {
			return true
		}
	}
	return false
}

func (cm *ClusterMetric) GetNonBkmTags() []string {
	var arr []string
	for _, t := range cm.TagsParsed {
		if !strings.HasPrefix(t, "bkm_") {
			arr = append(arr, t)
		}
	}
	return arr
}
