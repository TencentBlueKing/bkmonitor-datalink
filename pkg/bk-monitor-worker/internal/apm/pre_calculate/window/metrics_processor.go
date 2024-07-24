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

const (
	KindClient   = 3
	KindProducer = 4
	KindServer   = 2
	KindConsumer = 5
)

type MetricProcessor struct {
	bkBizId string
	appName string
	appId   string
	dataId  string

	enabledLayer4Report bool
}

func (m *MetricProcessor) ToMetrics(receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph) {
	m.findSpanMetric(receiver, fullTreeGraph)
	m.findParentChildMetric(receiver, fullTreeGraph)
}

func (m *MetricProcessor) sendToSave(data storage.PrometheusStorageData, metricCount map[string]int, receiver chan<- storage.SaveRequest) {
	for k, v := range metricCount {
		metrics.RecordApmRelationMetricFindCount(m.dataId, k, v)
	}

	receiver <- storage.SaveRequest{
		Target: storage.Prometheus,
		Data:   data,
	}
}

func (m *MetricProcessor) findSpanMetric(
	receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph,
) {
	var labels []string
	metricCount := make(map[string]int)

	flowMetricCount := make(map[string]int)
	flowMetricRecordMapping := make(map[string]*storage.FlowMetricRecordStats)
	for _, span := range fullTreeGraph.StandardSpans() {

		// apm_service_with_apm_service_instance_relation
		serviceInstanceRelationLabelKey := fmt.Sprintf(
			"%s=%s,%s=%s,%s=%s,%s=%s",
			"__name__", storage.ApmServiceInstanceRelation,
			"apm_service_name", span.GetFieldValue(core.ServiceNameField),
			"apm_application_name", m.appName,
			"apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField),
		)
		if !slices.Contains(labels, serviceInstanceRelationLabelKey) {
			labels = append(labels, serviceInstanceRelationLabelKey)
			metricCount[storage.ApmServiceInstanceRelation]++
		}

		// apm_service_instance_with_k8s_address_relation
		serviceK8sRelationLabelKey := fmt.Sprintf(
			"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s",
			"__name__", storage.ApmServiceK8sRelation,
			"apm_service_name", span.GetFieldValue(core.ServiceNameField),
			"apm_application_name", m.appName,
			"apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField),
			"address", span.GetFieldValue(core.K8sPodIp),
			"bcs_cluster_id", span.GetFieldValue(core.K8sBcsClusterId),
		)
		if !slices.Contains(labels, serviceK8sRelationLabelKey) {
			labels = append(labels, serviceK8sRelationLabelKey)
			metricCount[storage.ApmServiceK8sRelation]++
		}

		// apm_service_instance_with_system_relation
		serviceSystemRelationLabelKey := fmt.Sprintf(
			"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s",
			"__name__", storage.ApmServiceSystemRelation,
			"apm_service_name", span.GetFieldValue(core.ServiceNameField),
			"apm_application_name", m.appName,
			"apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField),
			"bk_target_ip", span.GetFieldValue(core.NetHostIpField, core.HostIpField),
		)
		if !slices.Contains(labels, serviceSystemRelationLabelKey) {
			labels = append(labels, serviceSystemRelationLabelKey)
			metricCount[storage.ApmServiceSystemRelation]++
		}

		m.findComponentFlow(span, flowMetricRecordMapping, flowMetricCount)
	}

	if len(labels) > 0 {
		m.sendToSave(storage.PrometheusStorageData{Kind: storage.PromRelationMetric, Value: labels}, metricCount, receiver)
	}
	if len(flowMetricRecordMapping) > 0 {
		m.sendToSave(storage.PrometheusStorageData{Kind: storage.PromFlowMetric, Value: flowMetricRecordMapping}, flowMetricCount, receiver)
	}
}

// findParentChildMetric find the metrics from spans
func (m *MetricProcessor) findParentChildMetric(
	receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph,
) {

	metricRecordMapping := make(map[string]*storage.FlowMetricRecordStats)
	metricCount := make(map[string]int)

	for _, pair := range fullTreeGraph.FindDirectFilterParentChildPairs([]int{KindClient, KindProducer}, []int{KindServer, KindConsumer}) {

		cService := pair[0].GetFieldValue(core.ServiceNameField)
		sService := pair[1].GetFieldValue(core.ServiceNameField)
		duration := pair[0].EndTime - pair[1].StartTime

		if cService != "" && sService != "" {
			// --> Find service -> service relation
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%v,%s=%v",
				"__name__", storage.ApmServiceFlow,
				"from_apm_service_name", cService,
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryHttp,
				"from_apm_service_kind", storage.KindService,
				"to_apm_service_name", sService,
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryHttp,
				"to_apm_service_kind", storage.KindService,
				"from_span_error", pair[0].IsError(),
				"to_span_error", pair[1].IsError(),
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
		}

		if !m.enabledLayer4Report {
			continue
		}
		parentIp := pair[0].GetFieldValue(core.NetHostIpField, core.HostIpField)
		childIp := pair[1].GetFieldValue(core.NetHostIpField, core.HostIpField)
		if parentIp != "" {
			// ----> Find system -> service relation
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%v,%s=%v",
				"__name__", storage.SystemApmServiceFlow,
				"from_bk_target_ip", parentIp,
				"to_apm_service_name", sService,
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryHttp,
				"to_apm_service_kind", storage.KindService,
				"from_span_error", pair[0].IsError(),
				"to_span_error", pair[1].IsError(),
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.SystemApmServiceFlow]++
		}
		if childIp != "" {
			// ----> Find service -> system relation
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%v,%s=%v",
				"__name__", storage.ApmServiceSystemFlow,
				"from_apm_service_name", cService,
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryHttp,
				"from_apm_service_kind", storage.KindService,
				"to_bk_target_ip", childIp,
				"from_span_error", pair[0].IsError(),
				"to_span_error", pair[1].IsError(),
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.ApmServiceSystemFlow]++
		}
		if parentIp != "" && childIp != "" {
			// ----> find system -> system relation
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%v,%s=%v",
				"__name__", storage.SystemFlow,
				"from_bk_target_ip", parentIp,
				"to_bk_target_ip", childIp,
				"from_span_error", pair[0].IsError(),
				"to_span_error", pair[1].IsError(),
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.SystemFlow]++
		}
	}

	if len(metricRecordMapping) > 0 {
		m.sendToSave(storage.PrometheusStorageData{Kind: storage.PromFlowMetric, Value: metricRecordMapping}, metricCount, receiver)
	}
}

func (m *MetricProcessor) addToStats(labelKey string, duration int, metricRecordMapping map[string]*storage.FlowMetricRecordStats) {
	c, exist := metricRecordMapping[labelKey]
	if !exist {
		metricRecordMapping[labelKey] = &storage.FlowMetricRecordStats{DurationValues: []float64{float64(duration)}}
	} else {
		c.DurationValues = append(c.DurationValues, float64(duration))
	}
}

func (m *MetricProcessor) findComponentFlow(
	span StandardSpan, metricRecordMapping map[string]*storage.FlowMetricRecordStats, metricCount map[string]int,
) {
	// Only support discover db or messaging component
	dbSystem := span.GetFieldValue(core.DbSystemField)
	messageSystem := span.GetFieldValue(core.MessagingSystemField)
	if dbSystem == "" && messageSystem == "" {
		return
	}

	serviceName := span.GetFieldValue(core.ServiceNameField)

	if dbSystem != "" && slices.Contains([]int{KindClient, KindProducer}, span.Kind) {
		// service (caller) -> db (callee)
		dbFlowLabelKey := fmt.Sprintf(
			"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%v,%s=%v",
			"__name__", storage.ApmServiceFlow,
			"from_apm_service_name", serviceName,
			"from_apm_application_name", m.appName,
			"from_apm_service_category", storage.CategoryHttp,
			"from_apm_service_kind", storage.KindService,
			"to_apm_service_name", fmt.Sprintf("%s-%s", serviceName, dbSystem),
			"to_apm_application_name", m.appName,
			"to_apm_service_category", storage.CategoryDb,
			"to_apm_service_kind", storage.KindComponent,
			"from_span_error", span.IsError(),
			"to_span_error", span.IsError(),
		)
		m.addToStats(dbFlowLabelKey, span.ElapsedTime, metricRecordMapping)
		metricCount[storage.ApmServiceFlow]++
		return
	}

	if messageSystem != "" {
		if slices.Contains([]int{KindClient, KindProducer}, span.Kind) {
			// service (caller) -> messageQueue (callee)
			messageCalleeFlowLabelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%v,%s=%v",
				"__name__", storage.ApmServiceFlow,
				"from_apm_service_name", serviceName,
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryHttp,
				"from_apm_service_kind", storage.KindService,
				"to_apm_service_name", fmt.Sprintf("%s-%s", serviceName, messageSystem),
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryMessaging,
				"to_apm_service_kind", storage.KindComponent,
				"from_span_error", span.IsError(),
				"to_span_error", span.IsError(),
			)
			m.addToStats(messageCalleeFlowLabelKey, span.ElapsedTime, metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
			return
		}
		if slices.Contains([]int{KindServer, KindConsumer}, span.Kind) {
			messageCallerFlowLabelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%v,%s=%v",
				"__name__", storage.ApmServiceFlow,
				"from_apm_service_name", fmt.Sprintf("%s-%s", serviceName, messageSystem),
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryMessaging,
				"from_apm_service_kind", storage.KindComponent,
				"to_apm_service_name", serviceName,
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryHttp,
				"to_apm_service_kind", storage.KindService,
				"from_span_error", span.IsError(),
				"to_span_error", span.IsError(),
			)
			// For ot-kafka SDK generation span,
			// the time consumption is the time spent receiving the message,
			// and does not include the subsequent processing time of the message,
			// so we get the elapsedTime
			m.addToStats(messageCallerFlowLabelKey, span.ElapsedTime, metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
			return
		}
	}
}

func newMetricProcessor(dataId string, enabledLayer4Metric bool) MetricProcessor {
	logger.Infof("[RelationMetric] create metric processor, dataId: %s", dataId)
	baseInfo := core.GetMetadataCenter().GetBaseInfo(dataId)
	return MetricProcessor{
		dataId:              dataId,
		bkBizId:             baseInfo.BkBizId,
		appName:             baseInfo.AppName,
		appId:               baseInfo.AppId,
		enabledLayer4Report: enabledLayer4Metric,
	}
}
