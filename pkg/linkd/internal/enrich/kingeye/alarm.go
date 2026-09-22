// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kingeye

import (
	"fmt"
	"reflect"
	"regexp"

	"linkd/internal/jsonpath"
)

// AlarmMessage 是发送给 KAC alarm_collect_topic 的扁平 Alarm JSON。
type AlarmMessage struct {
	AlarmID           string         `json:"alarm_id"`
	SourceID          string         `json:"source_id"`
	SourceName        string         `json:"source_name"`
	Item              string         `json:"item"`
	MetricName        string         `json:"metric_name"`
	Name              string         `json:"name"`
	EventID           string         `json:"event_id"`
	AlarmTime         string         `json:"alarm_time"`
	Content           string         `json:"content"`
	Action            string         `json:"action"`
	Level             string         `json:"level"`
	Object            string         `json:"object"`
	BKTenantID        string         `json:"bk_tenant_id"`
	BKBizID           string         `json:"bk_biz_id"`
	BKBizName         string         `json:"bk_biz_name"`
	BKSetID           string         `json:"bk_set_id"`
	BKSetName         string         `json:"bk_set_name"`
	BKModuleID        string         `json:"bk_module_id"`
	BKModuleName      string         `json:"bk_module_name"`
	BKCloudID         string         `json:"bk_cloud_id"`
	BKCloudName       string         `json:"bk_cloud_name"`
	BKObjID           string         `json:"bk_obj_id"`
	BKInstID          string         `json:"bk_inst_id"`
	BKServiceID       string         `json:"bk_service_id"`
	MetaInfo          string         `json:"meta_info"`
	StrategyID        string         `json:"strategy_id"`
	StrategyName      string         `json:"strategy_name"`
	DimensionInfo     string         `json:"dimension_info"`
	ModelInstID       string         `json:"model_inst_id"`
	ModelID           string         `json:"model_id"`
	ModelName         string         `json:"model_name"`
	AnomalyBeginTime  *string        `json:"anomaly_begin_time"`
	CloseTime         *string        `json:"close_time,omitempty"`
	CloseReason       *string        `json:"close_reason,omitempty"`
	MetricUniqueID    string         `json:"metric_unique_id"`
	ResultTableID     string         `json:"result_table_id"`
	Namespace         string         `json:"Namespace"`
	TimeInterval      string         `json:"time_interval"`
	AggregateFunc     string         `json:"aggregate_func"`
	WhereCondition    string         `json:"where_condition"`
	Unit              string         `json:"unit"`
	DataSource        string         `json:"data_source"`
	FieldExtraInfo    FieldExtraInfo `json:"field_extra_info"`
	MetricQueryParams string         `json:"metric_query_params"`
	DynamicGroupID    []string       `json:"dynamic_group_id"`
	CWLabels          []string       `json:"cw_labels"`
	LogThemeID        int64          `json:"log_theme_id"`
	LogThemeName      string         `json:"log_theme_name"`
	LogQueryString    string         `json:"log_query_string"`
	LogRelateInfo     string         `json:"log_relate_info"`
	APMAppID          int64          `json:"apm_app_id"`
	APMAppName        string         `json:"apm_app_name"`
	APMAppAlias       string         `json:"apm_app_alias"`
	APMServiceName    string         `json:"apm_service_name"`
	APMInstanceName   string         `json:"apm_instance_name"`
	APMInterfaceName  string         `json:"apm_interface_name"`
	APMNetPeerName    string         `json:"apm_net_peer_name"`
	BCSClusterID      string         `json:"bcs_cluster_id"`
	ClusterName       string         `json:"cluster_name"`
	K8sNamespace      string         `json:"namespace"`
	Service           string         `json:"service"`
	WorkloadKind      string         `json:"workload_kind"`
	WorkloadName      string         `json:"workload_name"`
	PodName           string         `json:"pod_name"`
	ContainerName     string         `json:"container_name"`
	CloudPlatformID   string         `json:"cloud_plat_id"`
}

// FieldExtraInfo 保存 KAC 固定字段的附加展示信息。
type FieldExtraInfo struct {
	StrategyName StrategyExtraInfo `json:"strategy_name"`
}

// StrategyExtraInfo 保存策略跳转信息。
type StrategyExtraInfo struct {
	URL string `json:"url"`
}

var customName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

// ValidateFieldMappings 只允许额外顶层业务字段，禁止覆盖 KAC 固定协议字段。
func ValidateFieldMappings(fields map[string]string) error {
	if len(fields) > 128 {
		return fmt.Errorf("KAC custom field limit exceeded")
	}
	reserved := map[string]bool{}
	typ := reflect.TypeFor[AlarmMessage]()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Tag.Get("json")
		for j, c := range name {
			if c == ',' {
				name = name[:j]
				break
			}
		}
		reserved[name] = true
	}
	for name, path := range fields {
		if !customName.MatchString(name) || reserved[name] {
			return fmt.Errorf("KAC field mapping cannot target reserved or invalid field %q", name)
		}
		target, err := jsonpath.ParseTarget(path)
		if err != nil {
			return err
		}
		parts := target.Parts()
		if len(parts) < 2 || (parts[0] != "labels" && parts[0] != "extra_data") {
			return fmt.Errorf("KAC custom mapping source must be labels or extra_data")
		}
	}
	return nil
}
