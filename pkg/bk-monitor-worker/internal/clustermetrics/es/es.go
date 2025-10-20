// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/pkg/errors"
	"github.com/prometheus-community/elasticsearch_exporter/collector"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_model/go"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/worker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// GetMetricValue 根据指标类型获取值
func GetMetricValue(metricType io_prometheus_client.MetricType, metric *io_prometheus_client.Metric) float64 {
	switch metricType {
	case io_prometheus_client.MetricType_GAUGE:
		return metric.GetGauge().GetValue()
	case io_prometheus_client.MetricType_COUNTER:
		return metric.GetCounter().GetValue()
	case io_prometheus_client.MetricType_UNTYPED:
		return metric.GetUntyped().GetValue()
	}
	return 0
}

// CollectAndReportMetrics 采集&上报ES集群指标
func CollectAndReportMetrics(c storage.ClusterInfo) error {
	logger.Infof("CollectAndReportMetrics:start to collect es cluster metrics, es cluster name [%s].", c.ClusterName)
	// 从custom option中获取集群业务id
	var bkBizID float64
	var customOption map[string]any
	err := jsonx.Unmarshal([]byte(c.CustomOption), &customOption)
	if err != nil {
		return errors.WithMessage(err, "CollectAndReportMetrics:failed to unmarshal custom option")
	}
	if bkBizIDVal, ok := customOption["bk_biz_id"].(float64); ok {
		bkBizID = bkBizIDVal
	}

	// 解析ES集群URL、用户名、密码信息
	var schema string
	if c.Schema != nil {
		schema = *c.Schema
	} else {
		schema = "http"
	}
	esURLs := elasticsearch.ComposeESHosts(schema, c.DomainName, c.Port)
	esUsername := c.Username
	esPassword, err := cipher.GetDBAESCipher().AESDecrypt(c.Password)
	if err != nil {
		return errors.WithMessage(err, "CollectAndReportMetrics:failed to decrypt es cluster password")
	}
	esURL, err := url.Parse(esURLs[0])
	if err != nil {
		return errors.WithMessage(err, "CollectAndReportMetrics:failed to parse es cluster url")
	}
	esURL.User = url.UserPassword(esUsername, esPassword)
	esURI := fmt.Sprintf("%s:%v", c.DomainName, c.Port)

	_, err = net.DialTimeout("tcp", esURI, 5*time.Second)
	if err != nil {
		return errors.WithMessage(err, "CollectAndReportMetrics:esURL unreachable")
	}

	// 注册es指标收集器
	collectorLogger := log.NewNopLogger()
	exporterCollector, err := collector.NewElasticsearchCollector(
		collectorLogger,
		[]string{},
		collector.WithElasticsearchURL(esURL),
		collector.WithHTTPClient(httpClient),
	)
	if err != nil {
		return errors.WithMessage(err, "CollectAndReportMetrics:failed to create elasticsearch collector")
	}
	indicesCollector := collector.NewIndices(collectorLogger, httpClient, esURL, true, true)
	shardsCollector := collector.NewShards(collectorLogger, httpClient, esURL)
	clusterHeathCollector := collector.NewClusterHealth(collectorLogger, httpClient, esURL)
	nodesCollector := collector.NewNodes(collectorLogger, httpClient, esURL, true, "_local")

	esCollectors := map[string]prometheus.Collector{
		"exporter":       exporterCollector,
		"indices":        indicesCollector,
		"shards":         shardsCollector,
		"cluster_health": clusterHeathCollector,
		"nodes":          nodesCollector,
	}
	defer func() {
		close(*indicesCollector.ClusterLabelUpdates())
		close(*shardsCollector.ClusterLabelUpdates())
	}()

	timestamp := time.Now().UnixMilli()

	for metricType, esCollector := range esCollectors {
		start := time.Now()
		registry := prometheus.NewRegistry()
		registry.MustRegister(esCollector)
		metricFamilies, err := registry.Gather()
		registry.Unregister(esCollector)

		if err != nil {
			return errors.WithMessagef(err, "CollectAndReportMetrics:collect es %s metrics failed", metricType)
		}

		esMetrics := make([]*clustermetrics.EsMetric, 0)

		for _, mf := range metricFamilies {
			// 处理指标数据
			metricType := mf.GetType()
			metricName := mf.GetName()
			for _, metric := range mf.GetMetric() {
				// 填充指标值
				m := make(map[string]float64)
				m[metricName] = GetMetricValue(metricType, metric)
				d := make(map[string]any)

				// 填充指标维度
				for _, label := range metric.GetLabel() {
					d[label.GetName()] = label.GetValue()
				}

				// 填充默认维度
				d["bk_biz_id"] = bkBizID
				d["cluster_id"] = strconv.Itoa(int(c.ClusterID))
				d["cluster_name"] = c.ClusterName
				if index, ok := d["index"].(string); ok {
					bizMatch := targetBizRe.FindStringSubmatch(index)
					if len(bizMatch) > 0 {
						if bizMatch[1] == "_space" {
							d["target_biz_id"] = "-" + bizMatch[2]
						} else {
							d["target_biz_id"] = bizMatch[2]
						}
					}
					rtMatch := rtRe.FindStringSubmatch(index)
					if len(rtMatch) > 1 {
						d["table_id"] = rtMatch[1]
					}
					logger.Debugf("CollectAndReportMetrics:index: %s, target_biz_id: %s, table_id: %s", index, d["target_biz_id"],
						d["table_id"])
				}

				esm := &clustermetrics.EsMetric{
					Metrics:   m,
					Target:    cfg.ESClusterMetricTarget,
					Timestamp: timestamp,
					Dimension: d,
				}
				esMetrics = append(esMetrics, esm)
			}
		}

		if len(esMetrics) == 0 {
			logger.Infof("CollectAndReportMetrics:skip to process es %s metrics [%s], all metric count: %v, current timestamp: %v ",
				metricType, c.ClusterName, len(esMetrics), timestamp)
			continue
		}

		logger.Infof("CollectAndReportMetrics:process es %s metrics success [%s], all metric count: %v, current timestamp: %v ",
			metricType, c.ClusterName, len(esMetrics), timestamp)

		customReportData := clustermetrics.CustomReportData{
			DataId: cfg.ESClusterMetricReportDataId, AccessToken: cfg.ESClusterMetricReportAccessToken, Data: esMetrics,
		}
		jsonData, err := jsonx.Marshal(customReportData)
		if err != nil {
			return errors.WithMessage(err, "CollectAndReportMetrics:custom report data json marshal failed")
		}

		u, err := url.Parse(cfg.ESClusterMetricReportUrl)
		if err != nil {
			return errors.WithMessage(err, "CollectAndReportMetrics:parse es cluster metric report url failed")
		}

		customReportUrl := u.String()
		err = func() error {
			req, _ := http.NewRequest("POST", customReportUrl, bytes.NewBuffer(jsonData))
			req.Header.Set("Content-Type", "application/json")
			resp, err := httpClient.Do(req)
			if err != nil {
				return errors.WithMessage(err, "CollectAndReportMetrics:report es metrics failed")
			}
			defer resp.Body.Close()

			return nil
		}()

		if err != nil {
			logger.Infof("CollectAndReportMetrics:report es %s metrics failed [%s], err: %v", metricType, c.ClusterName, err)
			continue
		}

		elapsed := time.Since(start)
		logger.Infof("CollectAndReportMetrics:report es %s metrics success [%s], task execution time：%s", metricType, c.ClusterName, elapsed)
	}

	return nil
}

// 构建全局http client
var httpClient = &http.Client{
	Timeout: 300 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{},
		Proxy:           http.ProxyFromEnvironment,
	},
}

type CollectESTaskParams struct {
	ClusterInfo storage.ClusterInfo `json:"cluster_info"`
}

var targetBizRe = regexp.MustCompile(`v2(_space)?_(\d+)_`)

var rtRe = regexp.MustCompile(`^v2_(.*)_.*?_.*$`)

func ReportESClusterMetrics(ctx context.Context, currentTask *t.Task) error {
	logger.Infof("start report es cluster metrics task.")
	// 1. 从metadata db中获取所有ES类型集群信息
	dbSession := mysql.GetDBSession()
	var esClusterInfoList []storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(dbSession.DB).ClusterTypeEq(models.StorageTypeES).All(&esClusterInfoList); err != nil {
		logger.Errorf("query all cluster info record error, %v", err)
		return err
	}
	if len(esClusterInfoList) == 0 {
		logger.Infof("no es cluster need to report metrics.")
		return nil
	}

	blacklist := cfg.ESClusterMetricReportBlackList
	blacklistMap := make(map[uint]bool)
	for _, cluster := range blacklist {
		clusterID := uint(cluster)
		blacklistMap[clusterID] = true
	}

	// 创建一个新的列表，用于存储不在黑名单中的集群
	var filteredClusterInfoList []storage.ClusterInfo
	for _, cluster := range esClusterInfoList {
		// 检查集群是否在黑名单中
		if _, found := blacklistMap[cluster.ClusterID]; !found {
			filteredClusterInfoList = append(filteredClusterInfoList, cluster)
		}
	}
	esClusterInfoList = filteredClusterInfoList

	// 2. 遍历存储获取集群信息
	wg := &sync.WaitGroup{}
	ch := make(chan struct{}, clustermetrics.GetGoroutineLimit("report_es"))
	wg.Add(len(esClusterInfoList))

	client, err := worker.GetClient()
	if err != nil {
		logger.Errorf("get client error, %v", err)
	}

	for _, clusterInfo := range esClusterInfoList {
		ch <- struct{}{}
		go func(c storage.ClusterInfo, wg *sync.WaitGroup, ch chan struct{}) {
			defer func() {
				<-ch
				wg.Done()
			}()
			// 3. 采集并上报集群指标
			payload, err := jsonx.Marshal(CollectESTaskParams{ClusterInfo: c})
			if _, err = client.Enqueue(&t.Task{
				Kind:    "async:collect_es_task",
				Payload: payload,
				Options: []t.Option{t.Queue("log-search")},
			}); err != nil {
				logger.Errorf("es_cluster_info: [%v] name [%s] try to enqueue collect task failed, %v", c.ClusterID, c.ClusterName, err)
			}
		}(clusterInfo, wg, ch)
	}
	wg.Wait()

	logger.Infof("report es cluster metrics task is done.")
	return nil
}
