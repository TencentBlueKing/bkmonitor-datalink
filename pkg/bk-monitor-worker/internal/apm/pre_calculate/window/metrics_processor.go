// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"fmt"

	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
)

type MetricProcessor struct {
	bkBizId string
	appName string
	appId   string
	dataId  string
}

func (m *MetricProcessor) ToMetrics(receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph) {
	m.findSpanMetric(receiver, fullTreeGraph)
	m.findParentChildMetric(receiver, fullTreeGraph)
}

func (m *MetricProcessor) sendToSave(labels []string, metricCount map[string]int, receiver chan<- storage.SaveRequest) {
	if len(labels) > 0 {
		for k, v := range metricCount {
			metrics.RecordApmRelationMetricFindCount(m.dataId, k, v)
		}

		receiver <- storage.SaveRequest{
			Target: storage.Prometheus,
			Data:   storage.PrometheusStorageData{Value: labels},
		}
	}
}

func (m *MetricProcessor) findSpanMetric(
	receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph,
) {
	var labels []string
	metricCount := make(map[string]int)

	for _, span := range fullTreeGraph.StandardSpans() {

		// apm_service_with_apm_service_instance_relation
		serviceInstanceRelationName := "apm_service_with_apm_service_instance_relation"
		serviceInstanceRelationLabelKey := fmt.Sprintf(
			"%s=%s,%s=%s,%s=%s,%s=%s",
			"__name__", serviceInstanceRelationName,
			"apm_service_name", span.GetFieldValue(core.ServiceNameField),
			"apm_application_name", m.appName,
			"apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField),
		)
		if !slices.Contains(labels, serviceInstanceRelationLabelKey) {
			labels = append(labels, serviceInstanceRelationLabelKey)
			metricCount[serviceInstanceRelationName]++
		}

		// apm_service_instance_with_k8s_address_relation
		serviceK8sRelationName := "apm_service_instance_with_k8s_address_relation"
		serviceK8sRelationLabelKey := fmt.Sprintf(
			"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s",
			"__name__", serviceK8sRelationName,
			"apm_service_name", span.GetFieldValue(core.ServiceNameField),
			"apm_application_name", m.appName,
			"apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField),
			"address", span.GetFieldValue(core.K8sPodIp),
			"bcs_cluster_id", span.GetFieldValue(core.K8sBcsClusterId),
		)
		if !slices.Contains(labels, serviceK8sRelationLabelKey) {
			labels = append(labels, serviceK8sRelationLabelKey)
			metricCount[serviceK8sRelationName]++
		}

		// apm_service_instance_with_system_relation
		serviceSystemRelationName := "apm_service_instance_with_system_relation"
		serviceSystemRelationLabelKey := fmt.Sprintf(
			"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s",
			"__name__", serviceSystemRelationName,
			"apm_service_name", span.GetFieldValue(core.ServiceNameField),
			"apm_application_name", m.appName,
			"apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField),
			"bk_target_ip", span.GetFieldValue(core.NetHostIpField, core.HostIpField),
		)
		if !slices.Contains(labels, serviceSystemRelationLabelKey) {
			labels = append(labels, serviceSystemRelationLabelKey)
			metricCount[serviceSystemRelationName]++
		}
	}

	logger.Infof("[MetricProcessor] found %d span metric keys", len(labels))
	m.sendToSave(labels, metricCount, receiver)
}

// findParentChildMetric find the metrics from spans
func (m *MetricProcessor) findParentChildMetric(
	receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph,
) {

	var labels []string
	metricCount := make(map[string]int)

	for _, pair := range fullTreeGraph.FindParentChildPairs() {

		cService := pair[0].GetFieldValue(core.ServiceNameField)
		sService := pair[1].GetFieldValue(core.ServiceNameField)
		parentIp := pair[0].GetFieldValue(core.NetHostIpField, core.HostIpField)
		childIp := pair[1].GetFieldValue(core.NetHostIpField, core.HostIpField)

		if cService != "" && sService != "" {
			// --> Find service -> service relation
			name := "apm_service_to_apm_service_flow"
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s",
				"__name__", name,
				"from_apm_service_name", cService,
				"from_apm_application_name", m.appName,
				"to_apm_service_name", sService,
				"to_apm_application_name", m.appName,
			)
			if !slices.Contains(labels, labelKey) {
				labels = append(labels, labelKey)
				metricCount[name]++
			}
		}
		if parentIp != "" {
			// ----> Find system -> service relation
			name := "system_to_apm_service_flow"
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s",
				"__name__", name,
				"from_bk_target_ip", parentIp,
				"to_apm_service_name", sService,
				"to_apm_application_name", m.appName,
			)
			if !slices.Contains(labels, labelKey) {
				labels = append(labels, labelKey)
				metricCount[name]++
			}
		}
		if childIp != "" {
			// ----> Find service -> system relation
			name := "apm_service_to_system_flow"
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s",
				"__name__", name,
				"from_apm_service_name", cService,
				"from_apm_application_name", m.appName,
				"to_bk_target_ip", childIp,
			)
			if !slices.Contains(labels, labelKey) {
				labels = append(labels, labelKey)
				metricCount[name]++
			}
		}
		if parentIp != "" && childIp != "" {
			// ----> find system -> system relation
			name := "system_to_system_flow"
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s",
				"__name__", name,
				"from_bk_target_ip", parentIp,
				"to_bk_target_ip", childIp,
			)
			if !slices.Contains(labels, labelKey) {
				labels = append(labels, labelKey)
				metricCount[name]++
			}
		}
	}

	logger.Infof("[MetricProcessor] found %d relation metric keys", len(labels))
	m.sendToSave(labels, metricCount, receiver)
}

func newMetricProcessor(dataId string) MetricProcessor {
	logger.Infof("[RelationMetric] create metric processor, dataId: %s", dataId)
	baseInfo := core.GetMetadataCenter().GetBaseInfo(dataId)
	return MetricProcessor{
		dataId:  dataId,
		bkBizId: baseInfo.BkBizId,
		appName: baseInfo.AppName,
		appId:   baseInfo.AppId,
	}
}
