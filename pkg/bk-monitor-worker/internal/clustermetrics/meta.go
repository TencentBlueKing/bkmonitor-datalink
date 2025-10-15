// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package clustermetrics

import (
	"context"
	"embed"
	fs2 "io/fs"
	"strings"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

type ClusterMetricConfig struct {
	SQL string `yaml:"sql"`
}

type ClusterMetric struct {
	MetricName  string              `yaml:"metric_name"`
	Tags        []string            `yaml:"tags"`
	ClusterType string              `yaml:"cluster_type"`
	Config      ClusterMetricConfig `yaml:"config"`
}

type MetaClusterMetrics struct {
	Metrics []ClusterMetric `yaml:"metrics"`
}

type EsMetric struct {
	Metrics   map[string]float64 `json:"metrics"`
	Target    string             `json:"target"`
	Dimension map[string]any     `json:"dimension"`
	Timestamp int64              `json:"timestamp"`
}

type CustomReportData struct {
	DataId      int         `json:"data_id"`
	AccessToken string      `json:"access_token"`
	Data        []*EsMetric `json:"data"`
}

func (cm *ClusterMetric) GetBkmTags() []string {
	var arr []string
	for _, t := range cm.Tags {
		if strings.HasPrefix(t, "bkm_") {
			arr = append(arr, t)
		}
	}
	return arr
}

func (cm *ClusterMetric) IsInTags(tTag string) bool {
	for _, tag := range cm.Tags {
		if tag == tTag {
			return true
		}
	}
	return false
}

func (cm *ClusterMetric) GetNonBkmTags() []string {
	var arr []string
	for _, t := range cm.Tags {
		if !strings.HasPrefix(t, "bkm_") {
			arr = append(arr, t)
		}
	}
	return arr
}

//go:embed meta.yaml
var configFS embed.FS

func QueryInfluxdbMetrics(ctx context.Context) ([]ClusterMetric, error) {
	data, err := fs2.ReadFile(configFS, "meta.yaml")
	if err != nil {
		return nil, errors.Errorf("Fail to load cluster metrics from meta.yaml, %+v", err)
	}
	var metaCfg MetaClusterMetrics
	err = yaml.Unmarshal(data, &metaCfg)
	if err != nil {
		return nil, errors.Errorf("meta.confg is not yaml format, %+v", err)
	}
	return metaCfg.Metrics, nil
}
