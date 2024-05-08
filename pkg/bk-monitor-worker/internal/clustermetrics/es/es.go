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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
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

//type collectTaskInfo struct {
//	ClusterInfo storage.ClusterInfo
//	Timestamp   int64
//}

// collectAndReportMetrics 采集&上报ES集群指标
func collectAndReportMetrics(c storage.ClusterInfo, timestamp int64) error {
	logger.Infof("start to collect es cluster metrics, es cluster name [%s].", c.ClusterName)
	start := time.Now()
	// 从custom option中获取集群业务id
	var bkBizID float64
	var customOption map[string]interface{}
	err := jsonx.Unmarshal([]byte(c.CustomOption), &customOption)
	if err != nil {
		return errors.WithMessage(err, "failed to unmarshal custom option")
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
	var esURLs = elasticsearch.ComposeESHosts(schema, c.DomainName, c.Port)
	var esUsername = c.Username
	var esPassword = cipher.GetDBAESCipher().AESDecrypt(c.Password)
	esURL, err := url.Parse(esURLs[0])
	if err != nil {
		return errors.WithMessage(err, "failed to parse es cluster url")
	}
	esURL.User = url.UserPassword(esUsername, esPassword)
	var esURI = fmt.Sprintf("%s:%v", c.DomainName, c.Port)

	_, err = net.DialTimeout("tcp", esURI, 5*time.Second)
	if err != nil {
		return errors.WithMessage(err, "esURL unreachable")
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
		return errors.WithMessage(err, "failed to create elasticsearch collector")
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

	for metricType, esCollector := range esCollectors {
		registry := prometheus.NewRegistry()
		registry.MustRegister(esCollector)
		metricFamilies, err := registry.Gather()
		registry.Unregister(esCollector)

		if err != nil {
			return errors.WithMessagef(err, "collect es %s metrics failed", metricType)
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
				d := make(map[string]interface{})

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
					logger.Debugf("index: %s, target_biz_id: %s, table_id: %s", index, d["target_biz_id"],
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

		logger.Infof("process es %s metrics success [%s], all metric count: %v, current timestamp: %v ",
			metricType, c.ClusterName, len(esMetrics), timestamp)

		customReportData := clustermetrics.CustomReportData{
			DataId: cfg.ESClusterMetricReportDataId, AccessToken: cfg.ESClusterMetricReportAccessToken, Data: esMetrics}
		jsonData, err := jsonx.Marshal(customReportData)
		if err != nil {
			return errors.WithMessage(err, "custom report data json marshal failed")
		}

		u, err := url.Parse(cfg.ESClusterMetricReportUrl)
		if err != nil {
			return errors.WithMessage(err, "parse es cluster metric report url failed")
		}

		customReportUrl := u.String()
		_ = func() error {
			req, _ := http.NewRequest("POST", customReportUrl, bytes.NewBuffer(jsonData))
			req.Header.Set("Content-Type", "application/json")
			resp, err := httpClient.Do(req)
			if err != nil {
				return errors.WithMessage(err, "report es metrics failed")
			}
			defer resp.Body.Close()

			return nil
		}()

		elapsed := time.Since(start)
		logger.Infof("report es %s metrics success [%s], task execution time：%s", metricType, c.ClusterName, elapsed)
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

var targetBizRe = regexp.MustCompile(`v2(_space)?_(\d+)_`)

var rtRe = regexp.MustCompile(`^v2_(.*)_.*?_.*$`)

func ReportESClusterMetrics(ctx context.Context, t *t.Task) error {
	logger.Infof("start report es cluster metrics task.")
	timestamp := time.Now().Truncate(time.Minute).UnixMilli()
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

	// 2. 遍历存储获取集群信息
	wg := &sync.WaitGroup{}
	ch := make(chan struct{}, task.GetGoroutineLimit("report_es"))
	wg.Add(len(esClusterInfoList))
	for _, clusterInfo := range esClusterInfoList {
		ch <- struct{}{}
		go func(c storage.ClusterInfo, wg *sync.WaitGroup, ch chan struct{}) {
			defer func() {
				<-ch
				wg.Done()
			}()
			// 3. 采集并上报集群指标
			if err := collectAndReportMetrics(c, timestamp); err != nil {
				logger.Errorf("es_cluster_info: [%v] name [%s] try to collect and report metrics failed, %v", c.ClusterID, c.ClusterName, err)
			} else {
				logger.Infof("es_cluster_info: [%v] name [%s] collect and report metrics success", c.ClusterID, c.ClusterName)
			}
		}(clusterInfo, wg, ch)
	}
	wg.Wait()

	logger.Infof("report es cluster metrics task is done.")
	return nil
}
