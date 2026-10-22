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
	StrategyID        int64  `json:"strategy_id"`
	StrategyVersion   int64  `json:"strategy_version"`
	MonitorTemplateID int64  `json:"monitor_template_id"`
	StrategyConfigID  string `json:"strategy_config_id"`
	StrategyName      string `json:"strategy_name"`
	// URL 仅解码和重放历史丰富结果；新处理不生成产品链接。
	URL string `json:"url,omitempty"`
	// StrategyKind 是冻结的策略资源类别，不是产品页面类型。
	StrategyKind CWStrategyKind `json:"strategy_kind,omitempty"`
	// IsDefault 的 nil 表示历史结果未保存该属性，不能当成 false。
	IsDefault *bool `json:"is_default,omitempty"`
	// StrategyInstance 保存非默认策略关联实例的冻结身份。
	StrategyInstance *StrategyInstance `json:"strategy_instance,omitempty"`
	DataSource       string            `json:"data_source"`
}

// StrategyInstance 冻结策略关联实例的身份，不携带产品路由或当前实例状态。
type StrategyInstance struct {
	// ObjectModelCode 标识关联对象模型。
	ObjectModelCode string `json:"object_model_code"`
	// BKObjID 是 KAC 场景读取的 CMDB 对象身份。
	BKObjID string `json:"bk_obj_id"`
	// BKInstID 保留不透明实例身份，不转换成数值。
	BKInstID string `json:"bk_inst_id"`
	// ObjectModelGroupID 是关联对象模型的分类身份。
	ObjectModelGroupID string `json:"object_model_group_id"`
}

// ResourceValues 定义 resource Processor 的完整输出字段。
type ResourceValues struct {
	// BKHostID 是按同租户主机地址确认的主机 ID，独立于告警对象的实例身份。
	BKHostID any `json:"bk_host_id,omitempty"`
	// BKHostName 只使用主机属性 bk_host_name，不以展示名或 IP 代替。
	BKHostName      string   `json:"bk_host_name,omitempty"`
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
	MetricQueryParams any     `json:"metric_query_params,omitempty"`
	AnomalyBeginTime  *string `json:"anomaly_begin_time"`
}

// LogTheme 是 KLC 日志主题的最小租户隔离投影。
type LogTheme struct {
	TenantID string
	ID       int64
	Name     string
}

// LogValues 定义 log Processor 的完整输出字段。
type LogValues struct {
	LogThemeID     any      `json:"log_theme_id"`
	LogThemeName   string   `json:"log_theme_name"`
	LogQueryString string   `json:"log_query_string"`
	LogRelateInfo  string   `json:"log_relate_info"`
	CWLabels       []string `json:"cw_labels"`
}

// APMApplication 是 Application Client 返回的租户内应用投影。
type APMApplication struct {
	TenantID string
	ID       int64
	Name     string
	Alias    string
	BKBizID  int64
}

// APMValues 定义 apm Processor 的完整输出字段。
type APMValues struct {
	APMAppID         any      `json:"apm_app_id"`
	APMAppName       string   `json:"apm_app_name"`
	APMAppAlias      string   `json:"apm_app_alias"`
	APMServiceName   string   `json:"apm_service_name"`
	APMInstanceName  string   `json:"apm_instance_name"`
	APMInterfaceName string   `json:"apm_interface_name"`
	APMNetPeerName   string   `json:"apm_net_peer_name"`
	ModelID          string   `json:"model_id"`
	ModelInstID      string   `json:"model_inst_id"`
	BKBizID          any      `json:"bk_biz_id"`
	CWLabels         []string `json:"cw_labels"`
}

// K8sValues 定义 k8s Processor 的完整输出字段。
type K8sValues struct {
	BCSClusterID  string   `json:"bcs_cluster_id"`
	ClusterName   string   `json:"cluster_name"`
	Namespace     string   `json:"namespace"`
	Service       string   `json:"service"`
	WorkloadKind  string   `json:"workload_kind"`
	WorkloadName  string   `json:"workload_name"`
	PodName       string   `json:"pod_name"`
	ContainerName string   `json:"container_name"`
	BKBizID       any      `json:"bk_biz_id"`
	BKBizName     string   `json:"bk_biz_name"`
	ModelID       string   `json:"model_id"`
	ModelInstID   string   `json:"model_inst_id"`
	CWLabels      []string `json:"cw_labels"`
}

// SourceValues 定义 source Processor 的完整输出字段。
type SourceValues struct {
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name"`
	MetaInfo   string `json:"meta_info"`
}

// MetricQuery 优先按指标 ID 定位；无 ID 时仅接受唯一的目录反查结果。
type MetricQuery struct {
	// MetricID 是 metric 主键，0 表示发布材料未提供目录 ID。
	MetricID int64
	// SpaceUID 仅限制无 ID 反查；业务空间同时可见平台层 *。
	SpaceUID        string
	TenantID        string
	TableID         string
	FieldName       string
	ObjectModelCode string
	// FieldTag 的 derived_metric 按目录 kind=derived 定义名反查。
	FieldTag CWStrategyFieldTag
}

// LocatorComplete 判断是否具备目录反查所需的完整定位信息。
// 只有指标 ID，或字段配结果表／衍生定义名时才可反查；缺少任一要件时
// 读取方必须先拒绝，避免用残缺参数发出无法解释身份的查询。
func (q MetricQuery) LocatorComplete() bool {
	if q.MetricID > 0 {
		return true
	}
	if q.FieldName == "" {
		return false
	}
	return q.TableID != "" || q.FieldTag == CWStrategyFieldTagDerivedMetric
}

// MetricMetadata 是指标丰富和展示所需的元数据。
type MetricMetadata struct {
	ObjectModelCode string
	// Kind 区分原生物理指标与衍生定义，禁止把衍生定义降级为普通取数。
	Kind string
	// TableID、DataLabel 和 PhysicalField 是目录持有的原生指标主引用。
	TableID       string
	DataLabel     string
	PhysicalField string
	FieldName     string
	FieldCNName   string
	Description   string
	Unit          string
	ValueMapping  []MetricValueMapping
	Dimensions    []MetricDimension
}

// MetricDimension 将指标目录 dimensions[].id/name 投影为丰富展示定义。
type MetricDimension struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// MetricValueMapping 定义指标枚举原始值到展示名称的映射。
type MetricValueMapping struct {
	OriginalValue string `json:"original_value"`
	MappedValue   string `json:"mapped_value"`
}
