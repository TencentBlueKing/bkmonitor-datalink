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
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/go-kit/log"
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// GetMetricValue 根据指标类型获取值
func GetMetricValue(metricType io_prometheus_client.MetricType, metric *io_prometheus_client.Metric) float64 {
	switch metricType {
	case io_prometheus_client.MetricType_COUNTER:
		return metric.GetGauge().GetValue()
	case io_prometheus_client.MetricType_GAUGE:
		return metric.GetCounter().GetValue()
	case io_prometheus_client.MetricType_UNTYPED:
		return metric.GetUntyped().GetValue()
	}
	return 0
}

// collectAndReportMetrics 采集&上报ES集群指标
func collectAndReportMetrics(c storage.ClusterInfo) error {
	var bkBizID float64
	var customOption map[string]interface{}
	_ = json.Unmarshal([]byte(c.CustomOption), &customOption)
	if bkBizIDVal, ok := customOption["bk_biz_id"].(float64); ok {
		bkBizID = bkBizIDVal
	} else {
		bkBizID = 0
	}

	var schema string
	if c.Schema != nil {
		schema = *c.Schema
	} else {
		schema = "http"
	}
	var esURLs = elasticsearch.ComposeESHosts(schema, c.DomainName, c.Port)
	var esUsername = c.Username
	var esPassword = cipher.DBAESCipher.AESDecrypt(c.Password)
	esURL, _ := url.Parse(esURLs[0])
	esURL.User = url.UserPassword(esUsername, esPassword)
	tlsConfig := &tls.Config{}

	var httpTransport http.RoundTripper

	httpTransport = &http.Transport{
		TLSClientConfig: tlsConfig,
		Proxy:           http.ProxyFromEnvironment,
	}
	httpClient := &http.Client{
		Timeout:   300 * time.Second,
		Transport: httpTransport,
	}

	collectorLogger := log.NewNopLogger()
	registry := prometheus.NewRegistry()
	// 注册收集器
	exporter, err := collector.NewElasticsearchCollector(
		collectorLogger,
		[]string{},
		collector.WithElasticsearchURL(esURL),
		collector.WithHTTPClient(httpClient),
	)
	if err != nil {
		logger.Errorf("failed to create Elasticsearch collector: %s", err)
		return err
	}
	registry.MustRegister(exporter)
	registry.MustRegister(collector.NewIndices(collectorLogger, httpClient, esURL, false, false))
	registry.MustRegister(collector.NewShards(collectorLogger, httpClient, esURL))
	registry.MustRegister(collector.NewClusterHealth(collectorLogger, httpClient, esURL))
	registry.MustRegister(collector.NewNodes(collectorLogger, httpClient, esURL, true, "_local"))
	metricFamilies, err := registry.Gather()
	if err != nil {
		logger.Errorf("collect gather es metrics failed: %s", err)
		return err
	}
	logger.Infof("collect gather es metrics success, metric family count: %v ", len(metricFamilies))

	esMetrics := make([]*clustermetrics.EsMetric, 0)
	currentTime := time.Now()
	truncatedTime := currentTime.Truncate(time.Minute)
	timestamp := truncatedTime.Unix() * 1000

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

			esm := &clustermetrics.EsMetric{
				Metrics:   m,
				Target:    "log-search-4",
				Timestamp: timestamp,
				Dimension: d,
			}
			esMetrics = append(esMetrics, esm)
		}
	}

	logger.Infof("process es metrics success, all metric count: %v, current timestamp: %v ",
		len(esMetrics), timestamp)
	customReportData := clustermetrics.CustomReportData{
		DataId: cfg.ESClusterMetricReportDataId, AccessToken: cfg.ESClusterMetricReportAccessToken, Data: esMetrics}
	jsonData, err := json.Marshal(customReportData)
	if err != nil {
		logger.Errorf("custom report data JSON Failed: %s ", err)
		return err
	}
	logger.Infof("all es metrics json: %s ", jsonData)

	u, _ := url.Parse(cfg.ESClusterMetricReportUrl)

	u.Path = path.Join(u.Path, "/v2/push")
	customReportUrl := u.String()
	req, _ := http.NewRequest("POST", customReportUrl, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 300 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		logger.Errorf("report es metrics failed: %s ", err)
		return err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	logger.Infof("report es metrics success, request url: %s, response body: %s", customReportUrl, body)

	return nil
}

func ReportESClusterMetrics(ctx context.Context, t *t.Task) error {
	// 1. 从metadata db中获取所有ES类型集群信息
	dbSession := mysql.GetDBSession()
	var esClusterInfoList []storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(dbSession.DB).ClusterTypeEq(models.StorageTypeES).All(&esClusterInfoList); err != nil {
		logger.Errorf("query all cluster info record error, %v", err)
		return err
	}
	if len(esClusterInfoList) == 0 {
		logger.Infof("no es cluster need to report metrics")
		return nil
	}

	// 2. 遍历存储获取集群信息
	wg := &sync.WaitGroup{}
	ch := make(chan bool, 10)
	wg.Add(len(esClusterInfoList))
	for _, clusterInfo := range esClusterInfoList {
		ch <- true
		go func(c storage.ClusterInfo, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			// 3. 采集并上报集群指标
			if err := collectAndReportMetrics(c); err != nil {
				logger.Errorf("es_cluster_info: [%v] name [%s] try to collect and report metrics failed, %v", c.ClusterID, c.ClusterName, err)
			} else {
				logger.Infof("es_cluster_info: [%v] name [%s] collect and report metrics success", c.ClusterID, c.ClusterName)
			}
		}(clusterInfo, wg, ch)
	}
	wg.Wait()

	return nil
}
