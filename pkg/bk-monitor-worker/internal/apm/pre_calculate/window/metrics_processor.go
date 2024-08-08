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
	for _, pair := range parentChildPairs {
		cService := pair[0].GetFieldValue(core.ServiceNameField)
		sService := pair[1].GetFieldValue(core.ServiceNameField)

		cSpanKind := pair[0].Kind
		sSpanKind := pair[1].Kind
		// unit: μs
		var duration int
		if cSpanKind == int(core.KindClient) {
			// sync
			duration = pair[0].EndTime - pair[0].StartTime
		} else {
			// async
			duration = int(math.Abs(float64(pair[1].StartTime - pair[0].StartTime)))
		}

		if cService != "" && sService != "" {
			// --> Find service -> service relation
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s%s=%s,%s=%d,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%v,%s=%v",
				"__name__", storage.ApmServiceFlow,
				"from_apm_service_name", cService,
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryHttp,
				"from_apm_service_kind", storage.KindService,
				"from_apm_service_span_kind", cSpanKind,
				"to_apm_service_name", sService,
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryHttp,
				"to_apm_service_kind", storage.KindService,
				"to_apm_service_span_kind", sSpanKind,
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
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%v,%s=%v",
				"__name__", storage.SystemApmServiceFlow,
				"from_bk_target_ip", parentIp,
				"to_apm_service_name", sService,
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryHttp,
				"to_apm_service_kind", storage.KindService,
				"to_apm_service_span_kind", sSpanKind,
				"from_span_error", pair[0].IsError(),
				"to_span_error", pair[1].IsError(),
			)
			m.addToStats(labelKey, duration, metricRecordMapping)
			metricCount[storage.SystemApmServiceFlow]++
		}
		if childIp != "" {
			// ----> Find service -> system relation
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%s,%s=%v,%s=%v",
				"__name__", storage.ApmServiceSystemFlow,
				"from_apm_service_name", cService,
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryHttp,
				"from_apm_service_kind", storage.KindService,
				"from_apm_service_span_kind", cSpanKind,
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

	for _, aloneNode := range aloneNodes {
		if slices.Contains(ignoreSpanIds, aloneNode.SpanId) {
			continue
		}
		// 在这个 trace 里面它是孤独节点 此次调用就需要记录而不需要理会这个节点是否发生了调用关系
		serviceName := aloneNode.GetFieldValue(core.ServiceNameField)
		if slices.Contains(CallerKinds, aloneNode.Kind) {
			// fill callee
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%v,%s=%v",
				"__name__", storage.ApmServiceFlow,
				"from_apm_service_name", serviceName,
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryHttp,
				"from_apm_service_kind", storage.KindService,
				"from_apm_service_span_kind", aloneNode.Kind,
				"to_apm_service_name", fmt.Sprintf("%s-%s", serviceName, VirtualSvrCallee),
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryHttp,
				"to_apm_service_kind", storage.KindVirtualService,
				"to_apm_service_span_kind", m.getOppositeSpanKind(aloneNode.Kind),
				"from_span_error", aloneNode.IsError(),
				"to_span_error", aloneNode.IsError(),
			)
			m.addToStats(labelKey, aloneNode.Elapsed(), metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
			continue
		}
		if slices.Contains(CalleeKinds, aloneNode.Kind) {
			labelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%v,%s=%v",
				"__name__", storage.ApmServiceFlow,
				"from_apm_service_name", fmt.Sprintf("%s-%s", serviceName, VirtualSvrCaller),
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryHttp,
				"from_apm_service_kind", storage.KindVirtualService,
				"from_apm_service_span_kind", m.getOppositeSpanKind(aloneNode.Kind),
				"to_apm_service_name", serviceName,
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryHttp,
				"to_apm_service_kind", storage.KindService,
				"to_apm_service_span_kind", aloneNode.Kind,
				"from_span_error", aloneNode.IsError(),
				"to_span_error", aloneNode.IsError(),
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

	if dbSystem != "" && slices.Contains(CallerKinds, span.Kind) {
		// service (caller) -> db (callee)
		dbFlowLabelKey := fmt.Sprintf(
			"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%v,%s=%v",
			"__name__", storage.ApmServiceFlow,
			"from_apm_service_name", serviceName,
			"from_apm_application_name", m.appName,
			"from_apm_service_category", storage.CategoryHttp,
			"from_apm_service_kind", storage.KindService,
			"from_apm_service_span_kind", span.Kind,
			"to_apm_service_name", fmt.Sprintf("%s-%s", serviceName, dbSystem),
			"to_apm_application_name", m.appName,
			"to_apm_service_category", storage.CategoryDb,
			"to_apm_service_kind", storage.KindComponent,
			"to_apm_service_span_kind", m.getOppositeSpanKind(span.Kind),
			"from_span_error", span.IsError(),
			"to_span_error", span.IsError(),
		)
		m.addToStats(dbFlowLabelKey, span.Elapsed(), metricRecordMapping)
		metricCount[storage.ApmServiceFlow]++
		return
	}

	if messageSystem != "" {
		if slices.Contains(CallerKinds, span.Kind) {
			// service (caller) -> messageQueue (callee)
			messageCalleeFlowLabelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%v,%s=%v",
				"__name__", storage.ApmServiceFlow,
				"from_apm_service_name", serviceName,
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryHttp,
				"from_apm_service_kind", storage.KindService,
				"from_apm_service_span_kind", span.Kind,
				"to_apm_service_name", fmt.Sprintf("%s-%s", serviceName, messageSystem),
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryMessaging,
				"to_apm_service_kind", storage.KindComponent,
				"to_apm_service_span_kind", m.getOppositeSpanKind(span.Kind),
				"from_span_error", span.IsError(),
				"to_span_error", span.IsError(),
			)
			m.addToStats(messageCalleeFlowLabelKey, span.Elapsed(), metricRecordMapping)
			metricCount[storage.ApmServiceFlow]++
			return
		}
		if slices.Contains(CalleeKinds, span.Kind) {
			messageCallerFlowLabelKey := fmt.Sprintf(
				"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%v,%s=%v",
				"__name__", storage.ApmServiceFlow,
				"from_apm_service_name", fmt.Sprintf("%s-%s", serviceName, messageSystem),
				"from_apm_application_name", m.appName,
				"from_apm_service_category", storage.CategoryMessaging,
				"from_apm_service_kind", storage.KindComponent,
				"from_apm_service_span_kind", m.getOppositeSpanKind(span.Kind),
				"to_apm_service_name", serviceName,
				"to_apm_application_name", m.appName,
				"to_apm_service_category", storage.CategoryHttp,
				"to_apm_service_kind", storage.KindService,
				"to_apm_service_span_kind", span.Kind,
				"from_span_error", span.IsError(),
				"to_span_error", span.IsError(),
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
	customServiceLabelKey := fmt.Sprintf(
		"%s=%s,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%s,%s=%s,%s=%s,%s=%s,%s=%d,%s=%v,%s=%v",
		"__name__", storage.ApmServiceFlow,
		"from_apm_service_name", serviceName,
		"from_apm_application_name", m.appName,
		"from_apm_service_category", storage.CategoryHttp,
		"from_apm_service_kind", storage.KindService,
		"from_apm_service_span_kind", span.Kind,
		"to_apm_service_name", fmt.Sprintf("%s:%s", peerServiceType, peerService),
		"to_apm_application_name", m.appName,
		"to_apm_service_category", storage.CategoryHttp,
		"to_apm_service_kind", storage.KindCustomService,
		"to_apm_service_span_kind", m.getOppositeSpanKind(span.Kind),
		"from_span_error", span.IsError(),
		"to_span_error", span.IsError(),
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
