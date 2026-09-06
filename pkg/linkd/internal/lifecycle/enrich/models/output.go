// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import "linkd/internal/domain"

// DimensionDisplay 是 display.dimensions 的单个类型化展示条目。
type DimensionDisplay struct {
	Name      string         `json:"name"`
	Value     domain.Scalar  `json:"value"`
	Key       *string        `json:"key,omitempty"`
	RealKey   *string        `json:"real_key,omitempty"`
	RealValue *domain.Scalar `json:"real_value,omitempty"`
}

// StrategyValues 定义 strategy Processor 的完整输出字段。
type StrategyValues struct {
	BKStrategyID      int64  `json:"bk_strategy_id"`
	MonitorTemplateID int64  `json:"monitor_template_id"`
	StrategyConfigID  string `json:"strategy_config_id"`
	StrategyName      string `json:"strategy_name"`
	URL               string `json:"url"`
	DataSource        string `json:"data_source"`
}

// ResourceValues 定义 resource Processor 的完整输出字段。
type ResourceValues struct {
	BKObjID         any      `json:"bk_obj_id"`
	BKInstID        any      `json:"bk_inst_id"`
	ModelID         string   `json:"model_id"`
	ModelInstID     string   `json:"model_inst_id"`
	ModelName       string   `json:"model_name"`
	BKBizID         any      `json:"bk_biz_id"`
	BKBizName       string   `json:"bk_biz_name"`
	BKSetID         any      `json:"bk_set_id"`
	BKSetName       string   `json:"bk_set_name"`
	BKModuleID      any      `json:"bk_module_id"`
	BKModuleName    string   `json:"bk_module_name"`
	BKCloudID       any      `json:"bk_cloud_id"`
	BKCloudName     string   `json:"bk_cloud_name"`
	CloudPlatformID any      `json:"cloud_plat_id"`
	DynamicGroupID  []string `json:"dynamic_group_id"`
	CWLabels        []string `json:"cw_labels"`
}

// DisplayValues 定义 display Processor 的完整输出字段。
type DisplayValues struct {
	Title         string             `json:"title"`
	Content       string             `json:"content"`
	Object        string             `json:"object"`
	Dimensions    []DimensionDisplay `json:"dimensions"`
	DimensionText string             `json:"dimension_text"`
}

// MetricValues 定义 metric Processor 的完整输出字段。
type MetricValues struct {
	DisplayName       string  `json:"display_name"`
	MetricName        string  `json:"metric_name"`
	Unit              string  `json:"unit"`
	ResultTableID     string  `json:"result_table_id"`
	MetricUniqueID    string  `json:"metric_unique_id"`
	AggregateFunc     string  `json:"aggregate_func"`
	TimeInterval      any     `json:"time_interval"`
	WhereCondition    string  `json:"where_condition"`
	MetricQueryParams any     `json:"metric_query_params"`
	AnomalyBeginTime  *string `json:"anomaly_begin_time"`
}

// LogValues 定义 log Processor 的完整输出字段。
type LogValues struct {
	LogThemeID     any    `json:"log_theme_id"`
	LogThemeName   string `json:"log_theme_name"`
	LogQueryString string `json:"log_query_string"`
	LogRelateInfo  string `json:"log_relate_info"`
}

// APMValues 定义 apm Processor 的完整输出字段。
type APMValues struct {
	APMAppID         any    `json:"apm_app_id"`
	APMAppName       string `json:"apm_app_name"`
	APMAppAlias      string `json:"apm_app_alias"`
	APMServiceName   string `json:"apm_service_name"`
	APMInstanceName  string `json:"apm_instance_name"`
	APMInterfaceName string `json:"apm_interface_name"`
	APMNetPeerName   string `json:"apm_net_peer_name"`
}

// K8sValues 定义 k8s Processor 的完整输出字段。
type K8sValues struct {
	BCSClusterID  string `json:"bcs_cluster_id"`
	ClusterName   string `json:"cluster_name"`
	Namespace     string `json:"namespace"`
	Service       string `json:"service"`
	WorkloadKind  string `json:"workload_kind"`
	WorkloadName  string `json:"workload_name"`
	PodName       string `json:"pod_name"`
	ContainerName string `json:"container_name"`
}

// SourceValues 定义 source Processor 的完整输出字段。
type SourceValues struct {
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name"`
	MetaInfo   string `json:"meta_info"`
}

// MetricLibraryQuery 描述指标库查询条件。
type MetricLibraryQuery struct {
	TenantID        string
	TableID         string
	FieldName       string
	ObjectModelCode string
	FieldTag        CWStrategyFieldTag
}

// MetricMetadata 是指标丰富和展示所需的元数据。
type MetricMetadata struct {
	FieldCNName string
	Description string
	Unit        string
	Dimensions  []MetricDimension
}

// MetricDimension 是 MonitorMetricLibrary.dimension_list 中的展示定义。
type MetricDimension struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
