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
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

const (
	VirtualSvrCallee = "bk_vServiceCallee"
	VirtualSvrCaller = "bk_vServiceCaller"

	VirtualSpan = "bk_vSpan"
)

var (
	CallerKinds = []int{int(core.KindClient), int(core.KindProducer)}
	CalleeKinds = []int{int(core.KindServer), int(core.KindConsumer)}

	// MatchFromDb 从 DB 中获取自定义服务规则进行匹配
	MatchFromDb = "db"
	// MatchFromSpan 从 Span 中获取字段进行匹配
	MatchFromSpan = "span"
)

type MetricProcessor struct {
	ctx context.Context

	bkBizId string
	appName string
	appId   string
	dataId  string

	enabledLayer4Report bool

	customServiceDiscoverType string
	customServiceRules        []models.CustomServiceRule
}

func (m *MetricProcessor) ToMetrics(receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph) {
	flowIgnoreSpanIds := m.findSpanMetric(receiver, fullTreeGraph)
	m.findParentChildAndAloneFlowMetric(receiver, fullTreeGraph, flowIgnoreSpanIds)

}

func (m *MetricProcessor) findSpanMetric(
	receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph,
) []string {
	var labels []string
	metricCount := make(map[string]int)

	var discoverSpanIds []string
	flowMetricCount := make(map[string]int)
	flowMetricRecordMapping := make(map[string]*storage.FlowMetricRecordStats)
	for _, span := range fullTreeGraph.StandardSpans() {

		// apm_service_with_apm_service_instance_relation
		serviceInstanceRelationLabelKey := strings.Join(
			[]string{
				pair("__name__", storage.ApmServiceInstanceRelation),
				pair("apm_service_name", span.GetFieldValue(core.ServiceNameField)),
				pair("apm_application_name", m.appName),
				pair("apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField)),
			},
			",",
		)

		if !slices.Contains(labels, serviceInstanceRelationLabelKey) {
			labels = append(labels, serviceInstanceRelationLabelKey)
			metricCount[storage.ApmServiceInstanceRelation]++
		}

		bcsClusterId := span.GetFieldValue(core.K8sBcsClusterId)
		if bcsClusterId != "" {
			podName := span.GetFieldValue(core.K8sPodName)
			if podName == "" {
				podName = span.GetFieldValue(core.NetHostnameField)
			}
			// apm_service_instance_with_pod_address_relation
			servicePodRelationLabelKey := strings.Join(
				[]string{
					pair("__name__", storage.ApmServicePodRelation),
					pair("apm_service_name", span.GetFieldValue(core.ServiceNameField)),
					pair("apm_application_name", m.appName),
					pair("apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField)),
					pair("bcs_cluster_id", bcsClusterId),
					pair("namespace", span.GetFieldValue(core.K8sNamespace)),
					pair("pod", podName),
				},
				",",
			)
			if !slices.Contains(labels, servicePodRelationLabelKey) {
				labels = append(labels, servicePodRelationLabelKey)
				metricCount[storage.ApmServicePodRelation]++
			}
		} else {
			// apm_service_instance_with_system_relation
			serviceSystemRelationLabelKey := strings.Join(
				[]string{
					pair("__name__", storage.ApmServiceSystemRelation),
					pair("apm_service_name", span.GetFieldValue(core.ServiceNameField)),
					pair("apm_application_name", m.appName),
					pair("apm_service_instance_name", span.GetFieldValue(core.BkInstanceIdField)),
					pair("bk_target_ip", span.GetFieldValue(core.NetHostIpField, core.HostIpField)),
				},
				",",
			)

			if !slices.Contains(labels, serviceSystemRelationLabelKey) {
				labels = append(labels, serviceSystemRelationLabelKey)
				metricCount[storage.ApmServiceSystemRelation]++
			}
		}

		discoverSpanIds = append(discoverSpanIds,
			m.findComponentFlowMetric(span, flowMetricRecordMapping, flowMetricCount)...,
		)
		discoverSpanIds = append(discoverSpanIds,
			m.findCustomServiceFlowMetric(span, flowMetricRecordMapping, flowMetricCount)...,
		)
	}

	if len(labels) > 0 {
		m.sendToSave(storage.PrometheusStorageData{Kind: storage.PromRelationMetric, Value: labels}, metricCount, receiver)
	}
	if len(flowMetricRecordMapping) > 0 {
		m.sendToSave(storage.PrometheusStorageData{Kind: storage.PromFlowMetric, Value: flowMetricRecordMapping}, flowMetricCount, receiver)
	}

	return discoverSpanIds
}

// findParentChildAndAloneFlowMetric find the metrics from spans
func (m *MetricProcessor) findParentChildAndAloneFlowMetric(
	receiver chan<- storage.SaveRequest, fullTreeGraph DiGraph,
	ignoreSpanIds []string,
) {

	metricRecordMapping := make(map[string]*storage.FlowMetricRecordStats)
	metricCount := make(map[string]int)

	parentChildPairs, aloneNodes := fullTreeGraph.FindDirectParentChildParisAndAloneNodes(CallerKinds, CalleeKinds)
	for _, pairs := range parentChildPairs {
		cService := pairs[0].GetFieldValue(core.ServiceNameField)
		sService := pairs[1].GetFieldValue(core.ServiceNameField)

		cServiceSpanName := pairs[0].GetFieldValue(core.SpanNameField)
		sServiceSpanName := pairs[1].GetFieldValue(core.SpanNameField)

		cSpanKind := pairs[0].Kind
		sSpanKind := pairs[1].Kind
		// unit: μs
		var duration int
		if cSpanKind == int(core.KindClient) {
			// sync
			duration = pairs[0].EndTime - pairs[0].StartTime
		} else {
			// async
			duration = int(math.Abs(float64(pairs[1].StartTime - pairs[0].StartTime)))
		}

		if cService != "" && sService != "" {
			// --> Find service -> service relation
			labelKey := strings.Join(
				[]string{
					pair("__name__", storage.ApmServiceFlow),
					pair("from_span_name", cServiceSpanName),
					pair("from_apm_service_name", cService),
					pair("from_apm_application_name", m.appName),
					pair("from_apm_service_category", storage.CategoryHttp),
					pair("from_apm_service_kind", storage.KindService),
					pair("from_apm_service_span_kind", strconv.Itoa(cSpanKind)),
					pair("to_span_name", sServiceSpanName),
					pair("to_apm_service_name", sService),
					pair("to_apm_application_name", m.appName),
					pair("to_apm_service_category", storage.CategoryHttp),
					pair("to_apm_service_kind", storage.KindService),
					pair("to_apm_service_span_kind", strconv.Itoa(sSpanKind)),
					pair("from_span_error", strconv.FormatBool(pairs[0].IsError())),
					pair("to_span_error", strconv.FormatBool(pairs[1].IsError())),
				},
				",",
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
		}

		if !m.enabledLayer4Report {
			continue
		}
		parentIp := pairs[0].GetFieldValue(core.NetHostIpField, core.HostIpField)
		childIp := pairs[1].GetFieldValue(core.NetHostIpField, core.HostIpField)
		if parentIp != "" {
			// ----> Find system -> service relation
			labelKey := strings.Join(
				[]string{
					pair("__name__", storage.SystemApmServiceFlow),
					pair("from_bk_target_ip", parentIp),
					pair("to_span_name", sServiceSpanName),
					pair("to_apm_service_name", sService),
					pair("to_apm_application_name", m.appName),
					pair("to_apm_service_category", storage.CategoryHttp),
					pair("to_apm_service_kind", storage.KindService),
					pair("to_apm_service_span_kind", strconv.Itoa(sSpanKind)),
					pair("from_span_error", strconv.FormatBool(pairs[0].IsError())),
					pair("to_span_error", strconv.FormatBool(pairs[1].IsError())),
				},
				",",
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.SystemApmServiceFlow]++
		}
		if childIp != "" {
			// ----> Find service -> system relation
			labelKey := strings.Join(
				[]string{
					pair("__name__", storage.ApmServiceSystemFlow),
					pair("from_span_name", cServiceSpanName),
					pair("from_apm_service_name", cService),
					pair("from_apm_application_name", m.appName),
					pair("from_apm_service_category", storage.CategoryHttp),
					pair("from_apm_service_kind", storage.KindService),
					pair("from_apm_service_span_kind", strconv.Itoa(cSpanKind)),
					pair("to_bk_target_ip", childIp),
					pair("from_span_error", strconv.FormatBool(pairs[0].IsError())),
					pair("to_span_error", strconv.FormatBool(pairs[1].IsError())),
				},
				",",
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.ApmServiceSystemFlow]++
		}
		if parentIp != "" && childIp != "" {
			// ----> find system -> system relation
			labelKey := strings.Join(
				[]string{
					pair("__name__", storage.SystemFlow),
					pair("from_bk_target_ip", parentIp),
					pair("to_bk_target_ip", childIp),
					pair("from_span_error", strconv.FormatBool(pairs[0].IsError())),
					pair("to_span_error", strconv.FormatBool(pairs[1].IsError())),
				},
				",",
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.SystemFlow]++
		}
	}

	for _, aloneNode := range aloneNodes {
		if slices.Contains(ignoreSpanIds, aloneNode.SpanId) {
			continue
		}
		// 在这个 trace 里面它是孤独节点 此次调用就需要记录而不需要理会这个节点是否发生了调用关系
		serviceName := aloneNode.GetFieldValue(core.ServiceNameField)
		spanName := aloneNode.GetFieldValue(core.SpanNameField)

		if slices.Contains(CallerKinds, aloneNode.Kind) {
			// fill callee
			labelKey := strings.Join(
				[]string{
					pair("__name__", storage.ApmServiceFlow),
					pair("from_span_name", spanName),
					pair("from_apm_service_name", serviceName),
					pair("from_apm_application_name", m.appName),
					pair("from_apm_service_category", storage.CategoryHttp),
					pair("from_apm_service_kind", storage.KindService),
					pair("from_apm_service_span_kind", strconv.Itoa(aloneNode.Kind)),
					pair("to_span_name", fmt.Sprintf("%s-%s", spanName, VirtualSpan)),
					pair("to_apm_service_name", fmt.Sprintf("%s-%s", serviceName, VirtualSvrCallee)),
					pair("to_apm_application_name", m.appName),
					pair("to_apm_service_category", storage.CategoryHttp),
					pair("to_apm_service_kind", storage.KindVirtualService),
					pair("to_apm_service_span_kind", strconv.Itoa(m.getOppositeSpanKind(aloneNode.Kind))),
					pair("from_span_error", strconv.FormatBool(aloneNode.IsError())),
					pair("to_span_error", strconv.FormatBool(aloneNode.IsError())),
				},
				",",
			)
			m.addToStats(labelKey, aloneNode.Elapsed(), metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
			continue
		}
		if slices.Contains(CalleeKinds, aloneNode.Kind) {
			labelKey := strings.Join(
				[]string{
					pair("__name__", storage.ApmServiceFlow),
					pair("from_span_name", fmt.Sprintf("%s-%s", spanName, VirtualSpan)),
					pair("from_apm_service_name", fmt.Sprintf("%s-%s", serviceName, VirtualSvrCaller)),
					pair("from_apm_application_name", m.appName),
					pair("from_apm_service_category", storage.CategoryHttp),
					pair("from_apm_service_kind", storage.KindVirtualService),
					pair("from_apm_service_span_kind", strconv.Itoa(m.getOppositeSpanKind(aloneNode.Kind))),
					pair("to_span_name", spanName),
					pair("to_apm_service_name", serviceName),
					pair("to_apm_application_name", m.appName),
					pair("to_apm_service_category", storage.CategoryHttp),
					pair("to_apm_service_kind", storage.KindService),
					pair("to_apm_service_span_kind", strconv.Itoa(aloneNode.Kind)),
					pair("from_span_error", strconv.FormatBool(aloneNode.IsError())),
					pair("to_apm_service_span_kind", strconv.FormatBool(aloneNode.IsError())),
				},
				",",
			)
			m.addToStats(labelKey, aloneNode.Elapsed(), metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
			continue
		}
	}

	if len(metricRecordMapping) > 0 {
		m.sendToSave(storage.PrometheusStorageData{Kind: storage.PromFlowMetric, Value: metricRecordMapping}, metricCount, receiver)
	}
}

func (m *MetricProcessor) getOppositeSpanKind(kind int) int {
	if slices.Contains(CallerKinds, kind) {
		if kind == int(core.KindClient) {
			return int(core.KindServer)
		} else {
			return int(core.KindConsumer)
		}
	} else {
		if kind == int(core.KindServer) {
			return int(core.KindClient)
		} else {
			return int(core.KindProducer)
		}
	}
}

func (m *MetricProcessor) findComponentFlowMetric(
	span StandardSpan,
	metricRecordMapping map[string]*storage.FlowMetricRecordStats,
	metricCount map[string]int,
) (discoverSpanIds []string) {
	// Only support discover db or messaging component
	dbSystem := span.GetFieldValue(core.DbSystemField)
	messageSystem := span.GetFieldValue(core.MessagingSystemField)
	if dbSystem == "" && messageSystem == "" {
		return
	}
	discoverSpanIds = append(discoverSpanIds, span.SpanId)
	serviceName := span.GetFieldValue(core.ServiceNameField)
	spanName := span.GetFieldValue(core.SpanNameField)

	if dbSystem != "" && slices.Contains(CallerKinds, span.Kind) {
		// service (caller) -> db (callee)
		dbFlowLabelKey := strings.Join(
			[]string{
				pair("__name__", storage.ApmServiceFlow),
				pair("from_span_name", spanName),
				pair("from_apm_service_name", serviceName),
				pair("from_apm_application_name", m.appName),
				pair("from_apm_service_category", storage.CategoryHttp),
				pair("from_apm_service_kind", storage.KindService),
				pair("from_apm_service_span_kind", strconv.Itoa(span.Kind)),
				pair("to_span_name", fmt.Sprintf("%s-%s", spanName, VirtualSpan)),
				pair("to_apm_service_name", fmt.Sprintf("%s-%s", serviceName, dbSystem)),
				pair("to_apm_application_name", m.appName),
				pair("to_apm_service_category", storage.CategoryDb),
				pair("to_apm_service_kind", storage.KindComponent),
				pair("to_apm_service_span_kind", strconv.Itoa(m.getOppositeSpanKind(span.Kind))),
				pair("from_span_error", strconv.FormatBool(span.IsError())),
				pair("to_span_error", strconv.FormatBool(span.IsError())),
			},
			",",
		)
		m.addToStats(dbFlowLabelKey, span.Elapsed(), metricRecordMapping)
		metricCount[storage.ApmServiceFlow]++
		return
	}

	if messageSystem != "" {
		if slices.Contains(CallerKinds, span.Kind) {
			// service (caller) -> messageQueue (callee)
			messageCalleeFlowLabelKey := strings.Join(
				[]string{
					pair("__name__", storage.ApmServiceFlow),
					pair("from_span_name", spanName),
					pair("from_apm_service_name", serviceName),
					pair("from_apm_application_name", m.appName),
					pair("from_apm_service_category", storage.CategoryHttp),
					pair("from_apm_service_kind", storage.KindService),
					pair("from_apm_service_span_kind", strconv.Itoa(span.Kind)),
					pair("to_span_name", fmt.Sprintf("%s-%s", spanName, VirtualSpan)),
					pair("to_apm_service_name", fmt.Sprintf("%s-%s", serviceName, messageSystem)),
					pair("to_apm_application_name", m.appName),
					pair("to_apm_service_category", storage.CategoryMessaging),
					pair("to_apm_service_kind", storage.KindComponent),
					pair("to_apm_service_span_kind", strconv.Itoa(m.getOppositeSpanKind(span.Kind))),
					pair("from_span_error", strconv.FormatBool(span.IsError())),
					pair("to_span_error", strconv.FormatBool(span.IsError())),
				},
				",",
			)
			m.addToStats(messageCalleeFlowLabelKey, span.Elapsed(), metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
			return
		}
		if slices.Contains(CalleeKinds, span.Kind) {
			messageCallerFlowLabelKey := strings.Join(
				[]string{
					pair("__name__", storage.ApmServiceFlow),
					pair("from_span_name", fmt.Sprintf("%s-%s", spanName, VirtualSpan)),
					pair("from_apm_service_name", fmt.Sprintf("%s-%s", serviceName, messageSystem)),
					pair("from_apm_application_name", m.appName),
					pair("from_apm_service_category", storage.CategoryMessaging),
					pair("from_apm_service_kind", storage.KindComponent),
					pair("from_apm_service_span_kind", strconv.Itoa(m.getOppositeSpanKind(span.Kind))),
					pair("to_span_name", spanName),
					pair("to_apm_service_name", serviceName),
					pair("to_apm_application_name", m.appName),
					pair("to_apm_service_category", storage.CategoryHttp),
					pair("to_apm_service_kind", storage.KindService),
					pair("to_apm_service_span_kind", strconv.Itoa(span.Kind)),
					pair("from_span_error", strconv.FormatBool(span.IsError())),
					pair("to_span_error", strconv.FormatBool(span.IsError())),
				},
				",",
			)
			// For ot-kafka SDK generation span,
			// the time consumption is the time spent receiving the message,
			// and does not include the subsequent processing time of the message,
			// so we get the elapsedTime
			m.addToStats(messageCallerFlowLabelKey, span.Elapsed(), metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
			return
		}
	}

	return
}

func (m *MetricProcessor) findCustomServiceFlowMetric(
	span StandardSpan,
	metricRecordMapping map[string]*storage.FlowMetricRecordStats,
	metricCount map[string]int,
) (discoverSpanIds []string) {
	serviceName := span.GetFieldValue(core.ServiceNameField)
	spanName := span.GetFieldValue(core.SpanNameField)
	var peerService string
	var peerServiceType string
	if m.customServiceDiscoverType == MatchFromSpan {
		peerService = span.GetFieldValue(core.PeerServiceField)
		peerServiceType = "http"
	} else {
		for _, rule := range m.customServiceRules {
			predicateKeyValue := span.GetFieldValue(rule.PredicateKey)
			if predicateKeyValue == "" {
				continue
			}
			val := span.GetFieldValue(rule.MatchKey)
			if val == "" {
				continue
			}
			mappings, matched, matchType := rule.Match(val)
			logger.Debugf("Matcher: mappings=%v, matched=%v, matchType=%v", mappings, matched, matchType)
			if !matched {
				continue
			}
			peerService = mappings["peerService"]
			if peerService == "" {
				continue
			}
			peerServiceType = rule.Type
			break
		}
	}
	if peerService == "" {
		return
	}
	customServiceLabelKey := strings.Join(
		[]string{
			pair("__name__", storage.ApmServiceFlow),
			pair("from_span_name", spanName),
			pair("from_apm_service_name", serviceName),
			pair("from_apm_application_name", m.appName),
			pair("from_apm_service_category", storage.CategoryHttp),
			pair("from_apm_service_kind", storage.KindService),
			pair("from_apm_service_span_kind", strconv.Itoa(span.Kind)),
			// 自定义服务 flow span_name 两边都一致
			pair("to_span_name", spanName),
			pair("to_apm_service_name", fmt.Sprintf("%s:%s", peerServiceType, peerService)),
			pair("to_apm_application_name", m.appName),
			pair("to_apm_service_category", storage.CategoryHttp),
			pair("to_apm_service_kind", storage.KindCustomService),
			pair("to_apm_service_span_kind", strconv.Itoa(m.getOppositeSpanKind(span.Kind))),
			pair("from_span_error", strconv.FormatBool(span.IsError())),
			pair("to_span_error", strconv.FormatBool(span.IsError())),
		},
		",",
	)
	m.addToStats(customServiceLabelKey, span.Elapsed(), metricRecordMapping)
	metricCount[storage.ApmServiceFlow]++
	discoverSpanIds = append(discoverSpanIds, span.SpanId)
	return
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

func (m *MetricProcessor) addToStats(labelKey string, duration int, metricRecordMapping map[string]*storage.FlowMetricRecordStats) {
	c, exist := metricRecordMapping[labelKey]
	if !exist {
		metricRecordMapping[labelKey] = &storage.FlowMetricRecordStats{DurationValues: []float64{float64(duration)}}
	} else {
		c.DurationValues = append(c.DurationValues, float64(duration))
	}
}

// refreshCustomService refresh custom service config from db, expire in 10min.
func (m *MetricProcessor) refreshCustomService() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	intBizId, _ := strconv.Atoi(m.bkBizId)
	refreshFromDb := func() {
		var result []models.CustomServiceConfig
		err := models.NewCustomServiceConfigQuerySet(
			mysql.GetDBSession().DB,
		).BkBizIdEq(intBizId).AppNameEq(m.appName).ConfigLevelEq("app_level").ConfigKeyEq(m.appName).TypeEq("http").All(&result)
		if err != nil {
			logger.Warnf("Something got error during query custom service! BkBizId: %s AppName: %s exception: %s", m.bkBizId, m.appName, err)
			return
		}
		var rules []models.CustomServiceRule
		for _, item := range result {
			rules = append(rules, item.ToRule())
		}
		m.customServiceRules = rules
		logger.Debugf("Refresh custom service successfully, length: %d", len(rules))
	}

	refreshFromDb()
	for {
		select {
		case <-ticker.C:
			refreshFromDb()
		case <-m.ctx.Done():
			return
		}
	}
}

func pair(k, v string) string {
	return k + "=" + v
}

func newMetricProcessor(ctx context.Context, dataId string, enabledLayer4Metric bool) *MetricProcessor {
	logger.Infof("[RelationMetric] create metric processor, dataId: %s", dataId)
	baseInfo := core.GetMetadataCenter().GetBaseInfo(dataId)
	p := MetricProcessor{
		ctx:                 ctx,
		dataId:              dataId,
		bkBizId:             baseInfo.BkBizId,
		appName:             baseInfo.AppName,
		appId:               baseInfo.AppId,
		enabledLayer4Report: enabledLayer4Metric,
		// 自定义服务的发现 目前统一为从 Span 的字段中匹配
		customServiceDiscoverType: MatchFromSpan,
	}

	if p.customServiceDiscoverType == MatchFromDb {
		go p.refreshCustomService()
	}

	return &p
}
