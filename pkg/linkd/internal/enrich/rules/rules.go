// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package rules 定义 Alert Enrichment 处理器共享的协议常量和纯判断。
package rules

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

const (
	LogProcessor      = "log"
	K8sProcessor      = "k8s"
	APMProcessor      = "apm"
	StrategyProcessor = "strategy"
	ResourceProcessor = "resource"
	DisplayProcessor  = "display"
	MetricProcessor   = "metric"
	SourceProcessor   = "source"
	// TestProcessor 是不访问外部数据源的可配置负载模拟器。
	TestProcessor = "test"
)

const (
	DependencyKingeyeStrategy = "kingeye_strategy"
	DependencyBusiness        = "business"
	DependencyMetricLibrary   = "metric_library"
	DependencyOneModel        = "onemodel"
	DependencyAlarmSource     = "alarm_source"
	DependencyLogTheme        = "log_theme"
	DependencyCloudResource   = "cloud_resource"
	DependencyAPMApplication  = "apm_application"
	DependencyCollectConfig   = "collect_config"
	DependencyCollectTopology = "collect_topology"
	DependencyUptime          = "uptime"
)

const (
	HostModelCode                  = "cw-Host"
	ServiceInstanceModelCode       = "cw-CCServiceInstance"
	LegacyServiceInstanceModelCode = "cw-ServiceInstance"
	K8sNodeModelCode               = "cw-K8s_Node"
	APMApplicationModelCode        = "cw-application"
	APMServiceModelCode            = "cw-service"
	APMServiceInstanceModelCode    = "cw-service_instance"
	UptimeModelCode                = "cw-web_service"
	OtherModelCode                 = "cw-Others"
	SystemEventTableID             = "system.event"
	APMTableMarker                 = "bkapm"
	SystemTablePrefix              = "system"
	TaskIndexPrefix                = "bk_task_index_"
	DimensionTagPrefix             = "tags."
)

const (
	FieldNoDataDimension           = "__NO_DATA_DIMENSION__"
	FieldIP                        = "ip"
	FieldModelID                   = "model_id"
	FieldModelInstID               = "model_inst_id"
	FieldModelName                 = "model_name"
	FieldBKObjID                   = "bk_obj_id"
	FieldBKCMDBObjectID            = "bk_cmdb_obj_id"
	FieldBKInstID                  = "bk_inst_id"
	FieldBKHostID                  = "bk_host_id"
	FieldBKTargetHostID            = "bk_target_host_id"
	FieldBKTargetIP                = "bk_target_ip"
	FieldBKTargetCloudID           = "bk_target_cloud_id"
	FieldBKCollectConfigID         = "bk_collect_config_id"
	FieldBKHostInnerIP             = "bk_host_innerip"
	FieldBKCloudID                 = "bk_cloud_id"
	FieldBKBizID                   = "bk_biz_id"
	FieldCWBIZID                   = "cw_biz_id"
	FieldBKTopoNode                = "bk_topo_node"
	FieldObjectModelID             = "obj_model_id"
	FieldObjectModelInstID         = "obj_model_inst_id"
	FieldObjectModelGroupID        = "object_model_group_id"
	FieldLegacyCWModelID           = "cw_object_model_id"
	FieldLegacyCWModelInstID       = "cw_object_model_inst_id"
	FieldLegacyHardwareModelID     = "object_model_id"
	FieldLegacyHardwareModelInstID = "obj_model_inst_id"
	FieldObjectModelName           = "object_model_name"
	FieldHostRelatedField          = "host_related_field"
	FieldBKObjName                 = "bk_obj_name"
	FieldBKInstName                = "bk_inst_name"
	FieldBKInstDisplayName         = "bk_inst_display_name"
	FieldBKBizName                 = "bk_biz_name"
	FieldBKSetID                   = "bk_set_id"
	FieldBKSetName                 = "bk_set_name"
	FieldBKModuleID                = "bk_module_id"
	FieldBKModuleName              = "bk_module_name"
	FieldBKCloudName               = "bk_cloud_name"
	FieldCloudPlatformID           = "cloud_plat_id"
	FieldServiceName               = "service_name"
	FieldInstance                  = "instance"
	FieldInstanceID                = "instanceid"
	FieldNode                      = "node"
	FieldNodeID                    = "node_id"
	FieldBCSClusterID              = "bcs_cluster_id"
	FieldNamespace                 = "namespace"
	FieldService                   = "service"
	FieldWorkloadKind              = "workload_kind"
	FieldWorkloadName              = "workload_name"
	FieldPodName                   = "pod_name"
	FieldContainerName             = "container_name"
	FieldURL                       = "url"
	FieldTargetHost                = "target_host"
	FieldTargetPort                = "target_port"
	FieldTarget                    = "target"
	FieldTargetType                = "target_type"
	FieldTaskID                    = "task_id"
	FieldCloudID                   = "cloud_id"
	FieldQueryString               = "query_string"
	FieldLogThemeId                = "log_theme_id"
	FieldLogThemeName              = "log_theme_name"
	FieldEventName                 = "event_name"
	FieldAnomalyBeginTime          = "anomaly_begin_time"
	FieldAPMAppID                  = "apm_app_id"
	FieldAPMInstanceID             = "bk_instance_id"
	FieldAPMSpanName               = "span_name"
	FieldAPMNetPeerName            = "net_peer_name"
)

const (
	DataSourceBKFTA       = "bk_fta"
	DataSourceBKMonitor   = "bk_monitor"
	DataSourcePrometheus  = "prometheus"
	DataSourceBKLogSearch = "bk_log_search"
	DataSourceCustom      = "custom"
	DataTypeLog           = "log"
	DataTypeEvent         = "event"
	DataTypeAlert         = "alert"
	MetricFieldIndex      = "_index"
	MetricFieldEventCount = "event.count"
	ResultTableEvent      = "event"
	ResultTableAlert      = "alert"
	ConditionAnd          = "and"
	ConditionEqual        = "eq"
)

const (
	DefaultAggregateFunc = "avg"
	DefaultTimeInterval  = "60"
	DefaultUptimeUnit    = "ms"
	LogMetricFallback    = "--"
)

const (
	StrategyManagePath = "/#/kmc/manage/monitorStrategy/monitorStrategyDetails?"
	StrategyScenePath  = "/#/kmc/scene/monitorViewDetails?"
)

// Classification 是一次丰富调用共享的主分类和 BaseTarget 二级分支结果。
type Classification struct {
	Main       DisplayClassification
	BaseTarget BaseTargetBranch
}

// BaseTargetBranch 是目标类告警内部的二级分支。
type BaseTargetBranch string

const (
	BaseTargetMonitorSource BaseTargetBranch = "monitor_source"
	BaseTargetCollectTask   BaseTargetBranch = "collect_task"
	BaseTargetNoData        BaseTargetBranch = "no_data"
	BaseTargetSystemMetric  BaseTargetBranch = "system_metric"
	BaseTargetUptimeCheck   BaseTargetBranch = "uptime_check"
	BaseTargetBasic         BaseTargetBranch = "basic"
)

// Classify 统一计算各 Processor 共享的分类结果。
func Classify(strategy models.CWStrategy, dimensions domain.DimensionMap) Classification {
	classification := Classification{Main: ClassifyDisplay(strategy)}
	if strategy.ObjectModelCode == nil {
		return classification
	}
	if *strategy.ObjectModelCode == UptimeModelCode && strategy.Spec.ConfigType != models.CWStrategyConfigTypeData {
		classification.Main = MainUptimeCheck
		classification.BaseTarget = BaseTargetUptimeCheck
		return classification
	}
	if classification.Main != DisplayBase {
		return classification
	}
	classification.BaseTarget = ClassifyBaseTarget(*strategy.ObjectModelCode, dimensions)
	return classification
}

// ClassifyBaseTarget 根据目标类告警的优先级选择二级分支。
func ClassifyBaseTarget(modelCode string, dimensions domain.DimensionMap) BaseTargetBranch {
	if ScalarProvided(dimensions[FieldBKInstID]) ||
		ScalarProvided(dimensions[FieldObjectModelID]) && ScalarProvided(dimensions[FieldObjectModelInstID]) {
		return BaseTargetMonitorSource
	}
	if ScalarProvided(dimensions[FieldBKCollectConfigID]) {
		return BaseTargetCollectTask
	}
	if ScalarProvided(dimensions[FieldNoDataDimension]) {
		return BaseTargetNoData
	}
	if modelCode == HostModelCode {
		return BaseTargetSystemMetric
	}
	return BaseTargetBasic
}

// IsServiceInstanceModel 判断模型是否使用 CMDB 服务实例的业务限定查询。
func IsServiceInstanceModel(modelCode string) bool {
	return modelCode == ServiceInstanceModelCode || modelCode == LegacyServiceInstanceModelCode
}

// DisplayClassification 是旧 KAC 告警展示分类。
type DisplayClassification string

// MainClassification 是场景主分类的别名。
type MainClassification = DisplayClassification

const (
	DisplayBase       DisplayClassification = "base_collect"
	DisplayData       DisplayClassification = "data"
	DisplayLogMetric  DisplayClassification = "log_metric"
	DisplayLogKeyword DisplayClassification = "log_keyword"
	DisplayCloud      DisplayClassification = "cloud"

	MainTarget                            = DisplayBase
	MainData                              = DisplayData
	MainLogMetric                         = DisplayLogMetric
	MainLogKeyword                        = DisplayLogKeyword
	MainUptimeCheck DisplayClassification = "uptime_check"
	MainCloud                             = DisplayCloud
)

// ClassifyDisplay 根据鲸眼策略选择展示规则。
func ClassifyDisplay(strategy models.CWStrategy) DisplayClassification {
	if strategy.Kind == models.CWStrategyKindCloud {
		return DisplayCloud
	}
	if strategy.Spec.ConfigType != models.CWStrategyConfigTypeData {
		return DisplayBase
	}
	if strategy.Spec.MonitorItemType == models.CWMonitorItemTypeLogKeyword {
		return DisplayLogKeyword
	}
	if strategy.Spec.MetricSource == models.CWMetricSourceKLC {
		return DisplayLogMetric
	}
	return DisplayData
}

// ScalarValue 返回 Scalar 保存的原始 Go 值。
func ScalarValue(value domain.Scalar) any {
	switch value.Kind() {
	case domain.ScalarKindString:
		result, _ := value.StringValue()
		return result
	case domain.ScalarKindNumber:
		result, _ := value.NumberValue()
		return result
	case domain.ScalarKindBool:
		result, _ := value.BoolValue()
		return result
	default:
		return nil
	}
}

// ScalarProvided 判断标量是否提供了可用于旧真值判断的值。
func ScalarProvided(value domain.Scalar) bool {
	switch value.Kind() {
	case domain.ScalarKindString:
		text, _ := value.StringValue()
		return text != ""
	case domain.ScalarKindNumber:
		number, _ := value.NumberValue()
		return number != 0
	case domain.ScalarKindBool:
		flag, _ := value.BoolValue()
		return flag
	default:
		return false
	}
}

// ScalarIdentity 将标量转换为外部实例查询使用的稳定文本。
func ScalarIdentity(value domain.Scalar) string {
	switch value.Kind() {
	case domain.ScalarKindString:
		result, _ := value.StringValue()
		return result
	case domain.ScalarKindNumber:
		result, _ := value.NumberValue()
		return strconv.FormatFloat(result, 'f', -1, 64)
	case domain.ScalarKindBool:
		result, _ := value.BoolValue()
		return strconv.FormatBool(result)
	default:
		return ""
	}
}

// DimensionValue 读取维度并保留其标量类型。
func DimensionValue(dimensions domain.DimensionMap, field string) (any, bool) {
	value, exists := dimensions[field]
	if !exists {
		return nil, false
	}
	return ScalarValue(value), true
}

// DimensionText 读取维度的文本表示。
func DimensionText(dimensions domain.DimensionMap, field string) string {
	value, exists := dimensions[field]
	if !exists {
		return ""
	}
	return fmt.Sprint(ScalarValue(value))
}

// IsIdentityDimension 判断摘要是否需要同时展示真实值和翻译值。
func IsIdentityDimension(field string) bool {
	switch field {
	case FieldBKBizID, FieldBKCloudID, FieldBKTargetCloudID,
		FieldInstanceID, FieldCloudID, FieldNodeID, FieldTaskID,
		FieldModelID, FieldModelInstID,
		FieldObjectModelID, FieldObjectModelInstID:
		return true
	default:
		return false
	}
}

// IsAPMDimension 判断字段是否迁入 APM 输出分组。
func IsAPMDimension(field string) bool {
	switch field {
	case FieldServiceName, FieldAPMInstanceID, FieldAPMSpanName, FieldAPMNetPeerName:
		return true
	default:
		return false
	}
}

// IsLogDisplay 判断分类是否使用日志专用展示规则。
func IsLogDisplay(classification DisplayClassification) bool {
	return classification == DisplayLogMetric || classification == DisplayLogKeyword
}

// FirstStringField 返回第一个非空字符串字段。
func FirstStringField(fields map[string]any, candidates ...string) (string, bool) {
	for _, candidate := range candidates {
		if value, ok := fields[candidate].(string); ok && value != "" {
			return value, true
		}
	}
	return "", false
}

// FirstStringFieldValue 将第一个存在的标量字段转换为稳定文本。
func FirstStringFieldValue(fields map[string]any, candidates ...string) string {
	for _, candidate := range candidates {
		value, exists := fields[candidate]
		if !exists || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			return typed
		case json.Number:
			return typed.String()
		case int64:
			return strconv.FormatInt(typed, 10)
		case int:
			return strconv.Itoa(typed)
		case float64:
			if math.Trunc(typed) == typed {
				return strconv.FormatInt(int64(typed), 10)
			}
		}
	}
	return ""
}

// HasTextField 返回 map 中指定字段可作为非空文本使用的值。
func HasTextField(fields map[string]any, name string) (string, bool) {
	value, ok := fields[name].(string)
	return value, ok && value != ""
}

// FirstField 返回第一个存在且值非 nil 的候选字段。
func FirstField(fields map[string]any, candidates ...string) (any, bool) {
	for _, candidate := range candidates {
		if value, exists := fields[candidate]; exists && value != nil {
			return value, true
		}
	}
	return nil, false
}

// IsAPMTable 判断结果表是否属于 APM。
func IsAPMTable(tableID string) bool {
	return strings.Contains(tableID, APMTableMarker)
}
