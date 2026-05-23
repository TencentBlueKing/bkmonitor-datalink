// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rabbitmq

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	defaultManagementSchema = "http"
	defaultHTTPPort         = 15672
	defaultAMQPPort         = 5672

	metricUp                          = "rabbitmq_up"
	metricQueueTotal                  = "rabbitmq_queue_total"
	metricConsumerTotal               = "rabbitmq_consumer_total"
	metricConnectionTotal             = "rabbitmq_connection_total"
	metricChannelTotal                = "rabbitmq_channel_total"
	metricMessagesTotal               = "rabbitmq_messages_total"
	metricMessagesReady               = "rabbitmq_messages_ready"
	metricMessagesUnacknowledged      = "rabbitmq_messages_unacknowledged"
	metricPublishTotal                = "rabbitmq_publish_total"
	metricPublishRate                 = "rabbitmq_publish_rate"
	metricDeliverGetTotal             = "rabbitmq_deliver_get_total"
	metricDeliverGetRate              = "rabbitmq_deliver_get_rate"
	metricAckTotal                    = "rabbitmq_ack_total"
	metricAckRate                     = "rabbitmq_ack_rate"
	metricRedeliverTotal              = "rabbitmq_redeliver_total"
	metricRedeliverRate               = "rabbitmq_redeliver_rate"
	metricMemoryAlarm                 = "rabbitmq_memory_alarm"
	metricDiskFreeAlarm               = "rabbitmq_disk_free_alarm"
	metricQueueMessages               = "rabbitmq_queue_messages"
	metricQueueMessagesReady          = "rabbitmq_queue_messages_ready"
	metricQueueMessagesUnacknowledged = "rabbitmq_queue_messages_unacknowledged"
	metricQueueConsumers              = "rabbitmq_queue_consumers"
	metricQueueConsumerUtilisation    = "rabbitmq_queue_consumer_utilisation"
	metricQueueMemory                 = "rabbitmq_queue_memory"
	metricQueueState                  = "rabbitmq_queue_state"
	metricQueuePublishTotal           = "rabbitmq_queue_publish_total"
	metricQueuePublishRate            = "rabbitmq_queue_publish_rate"
	metricQueueDeliverGetTotal        = "rabbitmq_queue_deliver_get_total"
	metricQueueDeliverGetRate         = "rabbitmq_queue_deliver_get_rate"
	metricQueueAckTotal               = "rabbitmq_queue_ack_total"
	metricQueueAckRate                = "rabbitmq_queue_ack_rate"
	metricQueueRedeliverTotal         = "rabbitmq_queue_redeliver_total"
	metricQueueRedeliverRate          = "rabbitmq_queue_redeliver_rate"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

type rateDetails struct {
	Rate float64 `json:"rate"`
}

type messageStats struct {
	Publish           float64     `json:"publish"`
	PublishDetails    rateDetails `json:"publish_details"`
	DeliverGet        float64     `json:"deliver_get"`
	DeliverGetDetails rateDetails `json:"deliver_get_details"`
	Ack               float64     `json:"ack"`
	AckDetails        rateDetails `json:"ack_details"`
	Redeliver         float64     `json:"redeliver"`
	RedeliverDetails  rateDetails `json:"redeliver_details"`
}

type overviewResponse struct {
	ObjectTotals struct {
		Connections float64 `json:"connections"`
		Channels    float64 `json:"channels"`
		Queues      float64 `json:"queues"`
		Consumers   float64 `json:"consumers"`
	} `json:"object_totals"`
	QueueTotals struct {
		Messages               float64 `json:"messages"`
		MessagesReady          float64 `json:"messages_ready"`
		MessagesUnacknowledged float64 `json:"messages_unacknowledged"`
	} `json:"queue_totals"`
	MessageStats messageStats `json:"message_stats"`
}

type queueResponse struct {
	Name                   string       `json:"name"`
	Vhost                  string       `json:"vhost"`
	State                  string       `json:"state"`
	Messages               float64      `json:"messages"`
	MessagesReady          float64      `json:"messages_ready"`
	MessagesUnacknowledged float64      `json:"messages_unacknowledged"`
	Consumers              float64      `json:"consumers"`
	ConsumerUtilisation    *float64     `json:"consumer_utilisation"`
	Memory                 float64      `json:"memory"`
	MessageStats           messageStats `json:"message_stats"`
}

type nodeResponse struct {
	MemAlarm      bool `json:"mem_alarm"`
	DiskFreeAlarm bool `json:"disk_free_alarm"`
}

type apiClient struct {
	instance cfg.RabbitMQClusterMetricInstance
	baseURL  string
	client   *http.Client
}

func ReportRabbitMQClusterMetrics(ctx context.Context, currentTask *t.Task) error {
	if !cfg.RabbitMQClusterMetricEnabled {
		logger.Infof("rabbitmq cluster metric report is disabled.")
		return nil
	}
	if len(cfg.RabbitMQClusterMetricInstances) == 0 {
		logger.Infof("no rabbitmq cluster metric instances need to report.")
		return nil
	}
	if cfg.RabbitMQClusterMetricReportUrl == "" {
		return errors.New("rabbitmq cluster metric report url is empty")
	}
	if cfg.RabbitMQClusterMetricReportDataId == 0 {
		return errors.New("rabbitmq cluster metric report data id is empty")
	}

	wg := &sync.WaitGroup{}
	limit := clustermetrics.GetGoroutineLimit("report_rabbitmq")
	if limit <= 0 {
		limit = 1
	}
	ch := make(chan struct{}, limit)
	for _, instance := range cfg.RabbitMQClusterMetricInstances {
		ch <- struct{}{}
		wg.Add(1)
		go func(inst cfg.RabbitMQClusterMetricInstance) {
			defer func() {
				<-ch
				wg.Done()
			}()
			if err := CollectAndReportMetrics(ctx, inst); err != nil {
				logger.Errorf("rabbitmq instance [%s] collect and report metrics failed: %v", inst.Name, err)
				return
			}
			logger.Infof("rabbitmq instance [%s] collect and report metrics success", inst.Name)
		}(instance)
	}
	wg.Wait()

	return nil
}

func CollectAndReportMetrics(ctx context.Context, instance cfg.RabbitMQClusterMetricInstance) error {
	if err := validateInstance(instance); err != nil {
		return err
	}

	metrics, collectErr := collectMetrics(ctx, instance)
	if len(metrics) > 0 {
		if err := reportMetrics(ctx, metrics); err != nil {
			return err
		}
	}
	return collectErr
}

func collectMetrics(ctx context.Context, instance cfg.RabbitMQClusterMetricInstance) ([]*clustermetrics.MetricData, error) {
	client := newAPIClient(instance)
	timestamp := time.Now().UnixMilli()

	overview, err := client.getOverview(ctx)
	if err != nil {
		return []*clustermetrics.MetricData{newMetricData(instance, timestamp, map[string]float64{metricUp: 0}, nil)}, err
	}

	nodes, err := client.getNodes(ctx)
	if err != nil {
		logger.Warnf("rabbitmq instance [%s] get nodes failed: %v", instance.Name, err)
	}

	result := []*clustermetrics.MetricData{
		newMetricData(instance, timestamp, buildOverviewMetrics(overview, nodes), nil),
	}

	queues, err := client.getQueues(ctx)
	if err != nil {
		return result, err
	}

	filter, err := newQueueFilter(instance)
	if err != nil {
		return result, err
	}
	for _, queue := range queues {
		if !filter.match(queue) {
			continue
		}
		result = append(result, newMetricData(instance, timestamp, buildQueueMetrics(queue), map[string]any{
			"vhost": queue.Vhost,
			"queue": queue.Name,
			"state": queue.State,
		}))
	}

	return result, nil
}

func validateInstance(instance cfg.RabbitMQClusterMetricInstance) error {
	if instance.Name == "" {
		return errors.New("rabbitmq instance name is empty")
	}
	if instance.DomainName == "" {
		return errors.Errorf("rabbitmq instance [%s] domainName is empty", instance.Name)
	}
	return nil
}

func newAPIClient(instance cfg.RabbitMQClusterMetricInstance) *apiClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: instance.TLSInsecureSkipVerify}
	return &apiClient{
		instance: instance,
		baseURL:  managementBaseURL(instance),
		client: &http.Client{
			Timeout:   instanceTimeout(instance),
			Transport: transport,
		},
	}
}

func instanceTimeout(instance cfg.RabbitMQClusterMetricInstance) time.Duration {
	if instance.TimeoutSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(instance.TimeoutSeconds) * time.Second
}

func (c *apiClient) getOverview(ctx context.Context) (overviewResponse, error) {
	var overview overviewResponse
	err := c.getJSON(ctx, apiURL(c.baseURL, "/api/overview"), &overview)
	return overview, err
}

func (c *apiClient) getNodes(ctx context.Context) ([]nodeResponse, error) {
	var nodes []nodeResponse
	err := c.getJSON(ctx, apiURL(c.baseURL, "/api/nodes"), &nodes)
	return nodes, err
}

func (c *apiClient) getQueues(ctx context.Context) ([]queueResponse, error) {
	if len(c.instance.Vhosts) == 0 {
		var queues []queueResponse
		err := c.getJSON(ctx, apiURL(c.baseURL, "/api/queues"), &queues)
		return queues, err
	}

	queues := make([]queueResponse, 0)
	for _, vhost := range c.instance.Vhosts {
		var vhostQueues []queueResponse
		err := c.getJSON(ctx, queueAPIURL(c.baseURL, vhost), &vhostQueues)
		if err != nil {
			return queues, err
		}
		queues = append(queues, vhostQueues...)
	}
	return queues, nil
}

func (c *apiClient) getJSON(ctx context.Context, reqURL string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.instance.Username != "" {
		req.SetBasicAuth(c.instance.Username, c.instance.Password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return errors.Errorf("request rabbitmq api [%s] failed, status: %d, body: %s", reqURL, resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return errors.Wrapf(err, "decode rabbitmq api response failed, url: %s", reqURL)
	}
	return nil
}

func buildOverviewMetrics(overview overviewResponse, nodes []nodeResponse) map[string]float64 {
	metrics := map[string]float64{
		metricUp:                     1,
		metricQueueTotal:             overview.ObjectTotals.Queues,
		metricConsumerTotal:          overview.ObjectTotals.Consumers,
		metricConnectionTotal:        overview.ObjectTotals.Connections,
		metricChannelTotal:           overview.ObjectTotals.Channels,
		metricMessagesTotal:          overview.QueueTotals.Messages,
		metricMessagesReady:          overview.QueueTotals.MessagesReady,
		metricMessagesUnacknowledged: overview.QueueTotals.MessagesUnacknowledged,
		metricPublishTotal:           overview.MessageStats.Publish,
		metricPublishRate:            overview.MessageStats.PublishDetails.Rate,
		metricDeliverGetTotal:        overview.MessageStats.DeliverGet,
		metricDeliverGetRate:         overview.MessageStats.DeliverGetDetails.Rate,
		metricAckTotal:               overview.MessageStats.Ack,
		metricAckRate:                overview.MessageStats.AckDetails.Rate,
		metricRedeliverTotal:         overview.MessageStats.Redeliver,
		metricRedeliverRate:          overview.MessageStats.RedeliverDetails.Rate,
	}

	for _, node := range nodes {
		if node.MemAlarm {
			metrics[metricMemoryAlarm] = 1
		}
		if node.DiskFreeAlarm {
			metrics[metricDiskFreeAlarm] = 1
		}
	}
	if _, ok := metrics[metricMemoryAlarm]; !ok {
		metrics[metricMemoryAlarm] = 0
	}
	if _, ok := metrics[metricDiskFreeAlarm]; !ok {
		metrics[metricDiskFreeAlarm] = 0
	}

	return metrics
}

func buildQueueMetrics(queue queueResponse) map[string]float64 {
	metrics := map[string]float64{
		metricQueueMessages:               queue.Messages,
		metricQueueMessagesReady:          queue.MessagesReady,
		metricQueueMessagesUnacknowledged: queue.MessagesUnacknowledged,
		metricQueueConsumers:              queue.Consumers,
		metricQueueMemory:                 queue.Memory,
		metricQueueState:                  queueStateValue(queue.State),
		metricQueuePublishTotal:           queue.MessageStats.Publish,
		metricQueuePublishRate:            queue.MessageStats.PublishDetails.Rate,
		metricQueueDeliverGetTotal:        queue.MessageStats.DeliverGet,
		metricQueueDeliverGetRate:         queue.MessageStats.DeliverGetDetails.Rate,
		metricQueueAckTotal:               queue.MessageStats.Ack,
		metricQueueAckRate:                queue.MessageStats.AckDetails.Rate,
		metricQueueRedeliverTotal:         queue.MessageStats.Redeliver,
		metricQueueRedeliverRate:          queue.MessageStats.RedeliverDetails.Rate,
	}
	if queue.ConsumerUtilisation != nil {
		metrics[metricQueueConsumerUtilisation] = *queue.ConsumerUtilisation
	}
	return metrics
}

func queueStateValue(state string) float64 {
	if strings.EqualFold(state, "running") {
		return 1
	}
	return 0
}

func newMetricData(
	instance cfg.RabbitMQClusterMetricInstance,
	timestamp int64,
	metrics map[string]float64,
	extraDimension map[string]any,
) *clustermetrics.MetricData {
	dimension := map[string]any{
		"rabbitmq_name": instance.Name,
		"endpoint":      managementBaseURL(instance),
		"domain_name":   instance.DomainName,
		"http_port":     instanceHTTPPort(instance),
		"amqp_port":     instanceAMQPPort(instance),
		"bk_biz_id":     instance.BkBizID,
		"bk_tenant_id":  tenantID(instance),
	}
	for k, v := range extraDimension {
		dimension[k] = v
	}

	return &clustermetrics.MetricData{
		Metrics:   metrics,
		Target:    cfg.RabbitMQClusterMetricTarget,
		Dimension: dimension,
		Timestamp: timestamp,
	}
}

func tenantID(instance cfg.RabbitMQClusterMetricInstance) string {
	if instance.BkTenantID != "" {
		return instance.BkTenantID
	}
	return tenant.DefaultTenantId
}

func reportMetrics(ctx context.Context, metrics []*clustermetrics.MetricData) error {
	reportData := clustermetrics.CustomReportData{
		DataId:      cfg.RabbitMQClusterMetricReportDataId,
		AccessToken: cfg.RabbitMQClusterMetricReportAccessToken,
		Data:        metrics,
	}
	body, err := jsonx.Marshal(reportData)
	if err != nil {
		return errors.Wrap(err, "marshal rabbitmq metric report data failed")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.RabbitMQClusterMetricReportUrl, bytes.NewBuffer(body))
	if err != nil {
		return errors.Wrap(err, "create rabbitmq metric report request failed")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "report rabbitmq metrics failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return errors.Errorf("report rabbitmq metrics failed, status: %d, body: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func apiURL(baseURL string, path string) string {
	return strings.TrimRight(baseURL, "/") + path
}

func queueAPIURL(baseURL string, vhost string) string {
	return fmt.Sprintf("%s/api/queues/%s", strings.TrimRight(baseURL, "/"), url.PathEscape(vhost))
}

func managementBaseURL(instance cfg.RabbitMQClusterMetricInstance) string {
	return fmt.Sprintf("%s://%s", instanceSchema(instance), net.JoinHostPort(instance.DomainName, strconv.Itoa(instanceHTTPPort(instance))))
}

func instanceSchema(instance cfg.RabbitMQClusterMetricInstance) string {
	if instance.Schema == "" {
		return defaultManagementSchema
	}
	return instance.Schema
}

func instanceHTTPPort(instance cfg.RabbitMQClusterMetricInstance) int {
	if instance.HTTPPort <= 0 {
		return defaultHTTPPort
	}
	return instance.HTTPPort
}

func instanceAMQPPort(instance cfg.RabbitMQClusterMetricInstance) int {
	if instance.AMQPPort <= 0 {
		return defaultAMQPPort
	}
	return instance.AMQPPort
}

type queueFilter struct {
	vhosts         map[string]struct{}
	includes       []*regexp.Regexp
	excludes       []*regexp.Regexp
	includeRegexes []*regexp.Regexp
	excludeRegexes []*regexp.Regexp
}

func newQueueFilter(instance cfg.RabbitMQClusterMetricInstance) (queueFilter, error) {
	vhosts := make(map[string]struct{}, len(instance.Vhosts))
	for _, vhost := range instance.Vhosts {
		vhosts[vhost] = struct{}{}
	}

	includeRegexes, err := compileRegexPatterns(instance.QueueIncludeRegexes)
	if err != nil {
		return queueFilter{}, err
	}
	excludeRegexes, err := compileRegexPatterns(instance.QueueExcludeRegexes)
	if err != nil {
		return queueFilter{}, err
	}

	return queueFilter{
		vhosts:         vhosts,
		includes:       compileWildcardPatterns(instance.QueueIncludes),
		excludes:       compileWildcardPatterns(instance.QueueExcludes),
		includeRegexes: includeRegexes,
		excludeRegexes: excludeRegexes,
	}, nil
}

func (f queueFilter) match(queue queueResponse) bool {
	if len(f.vhosts) > 0 {
		if _, ok := f.vhosts[queue.Vhost]; !ok {
			return false
		}
	}

	if f.hasIncludes() && !f.matchIncludes(queue.Name) {
		return false
	}

	return !matchRegexps(queue.Name, f.excludes) && !matchRegexps(queue.Name, f.excludeRegexes)
}

func (f queueFilter) hasIncludes() bool {
	return len(f.includes) > 0 || len(f.includeRegexes) > 0
}

func (f queueFilter) matchIncludes(queue string) bool {
	return matchRegexps(queue, f.includes) || matchRegexps(queue, f.includeRegexes)
}

func compileWildcardPatterns(patterns []string) []*regexp.Regexp {
	regexps := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		regexps = append(regexps, regexp.MustCompile(wildcardPattern(pattern)))
	}
	return regexps
}

func compileRegexPatterns(patterns []string) ([]*regexp.Regexp, error) {
	regexps := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, errors.Wrapf(err, "compile rabbitmq queue regex pattern [%s] failed", pattern)
		}
		regexps = append(regexps, re)
	}
	return regexps, nil
}

func wildcardPattern(pattern string) string {
	var builder strings.Builder
	builder.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			builder.WriteString(".*")
		case '?':
			builder.WriteString(".")
		default:
			builder.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	builder.WriteString("$")
	return builder.String()
}

func matchRegexps(value string, regexps []*regexp.Regexp) bool {
	for _, re := range regexps {
		if re.MatchString(value) {
			return true
		}
	}
	return false
}
