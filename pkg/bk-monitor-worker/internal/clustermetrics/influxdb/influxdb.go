// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	redisStore "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Instance struct {
	ClusterName string
	HostName    string
	Host        *storage.InfluxdbHostInfo
}

func (inst *Instance) GetContext() map[string]string {
	return map[string]string{
		config.ClusterMetricClusterFieldName: inst.ClusterName,
		config.ClusterMetricHostFieldName:    inst.HostName,
	}
}

// Message represents a user message.
type Message struct {
	Level string `json:"level,omitempty" msg:"level"`
	Text  string `json:"text,omitempty" msg:"text"`
}

// Row
type Row struct {
	Name    string            `json:"name,omitempty" msg:"name"`
	Tags    map[string]string `json:"tags,omitempty" msg:"tags"`
	Columns []string          `json:"columns,omitempty" msg:"columns"`
	Values  [][]any           `json:"values,omitempty" msg:"values"`
	Partial bool              `json:"partial,omitempty" msg:"partial"`
}

// Result
type Result struct {
	StatementID int        `json:"statement_id,omitempty" msg:"statement_id"`
	Series      []*Row     `json:"series,omitempty" msg:"series"`
	Messages    []*Message `json:"messages,omitempty" msg:"messages"`
	Partial     bool       `json:"partial,omitempty" msg:"partial"`
	Err         string     `json:"error,omitempty" msg:"error"`
}

// Response
type Response struct {
	Results []Result `json:"results,omitempty" msg:"results"`
	Err     string   `json:"error,omitempty" msg:"error"`
}

func ReportInfluxdbClusterMetric(ctx context.Context, t *t.Task) error {
	var clusterHosts []storage.InfluxdbClusterInfo
	var hosts []storage.InfluxdbHostInfo
	var metrics []clustermetrics.ClusterMetric
	var err error
	// 从数据读取 influxdb 集群、实例数据、influxdb 集群指标配置
	dbSession := mysql.GetDBSession()
	err = storage.NewInfluxdbClusterInfoQuerySet(dbSession.DB).All(&clusterHosts)
	if err != nil {
		logger.Errorf("Fail to query InfluxdbCLusterInfo records, %v", err)
		return err
	}
	err = storage.NewInfluxdbHostInfoQuerySet(dbSession.DB).All(&hosts)
	if err != nil {
		logger.Errorf("Fail to query InfluxdbHostInfo, %v", err)
		return err
	}
	metrics, err = clustermetrics.QueryInfluxdbMetrics(ctx)
	if err != nil {
		logger.Errorf("Fail to query ClusterMetric, %v", err)
		return err
	}

	// 组装成用于请求指标的实例配置列表
	instances := make([]*Instance, 0)
	hostClusterMapping := map[string]string{}
	for _, cluster := range clusterHosts {
		hostClusterMapping[cluster.HostName] = cluster.ClusterName
	}
	for _, host := range hosts {
		clusterName, exists := hostClusterMapping[host.HostName]
		if exists {
			instances = append(instances, &Instance{
				ClusterName: clusterName,
				HostName:    host.HostName,
				Host:        &host,
			})
		}
	}
	// 初始化 Redis 客户端和 shipper
	redisClient := redisStore.GetStorageRedisInstance()
	ks := clustermetrics.KvShipper{RedisClient: redisClient}
	// 初始化 Loader，开始加载指标数据
	recordQueue := make(chan *clustermetrics.Record)
	bl := BatchLoader{
		wg:          &sync.WaitGroup{},
		semaphore:   make(chan struct{}, 10),
		recordQueue: recordQueue,
		instances:   instances,
		client:      http.NewClient(),
		metrics:     metrics,
	}
	go bl.Load(ctx)
	for {
		select {
		case record, ok := <-recordQueue:
			if !ok {
				return nil
			}
			logger.Infof("Load record(%v), start to write to kv store", (*record).Print())
			ks.Write(ctx, record)
		case <-ctx.Done():
			return nil
		}
	}
}

type BatchLoader struct {
	wg          *sync.WaitGroup
	semaphore   chan struct{}
	recordQueue chan *clustermetrics.Record
	instances   []*Instance
	client      http.Client
	metrics     []clustermetrics.ClusterMetric
}

func (bl *BatchLoader) Load(ctx context.Context) {
	for _, instance := range bl.instances {
		select {
		case bl.semaphore <- struct{}{}:
			bl.wg.Add(1)
			go func(inst *Instance) {
				defer func() {
					bl.wg.Done()
					<-bl.semaphore
				}()
				bl.loadHostMetrics(ctx, inst)
			}(instance)
		case <-ctx.Done():
			break
		}
	}
	bl.wg.Wait()
	close(bl.recordQueue)
}

func (bl *BatchLoader) loadHostMetrics(ctx context.Context, instance *Instance) {
	for _, m := range bl.metrics {
		// 根据指标配置组装请求语句
		values := url.Values{}
		values.Set("db", "_internal")
		values.Set("q", m.Config.SQL)
		values.Set("epoch", "s")
		pwd, err := cipher.GetDBAESCipher().AESDecrypt(instance.Host.Password)
		if err != nil {
			logger.Errorf("loadHostMetrics:Fail to decrypt password, %s", err)
			return
		}
		options := http.Options{
			BaseUrl:  fmt.Sprintf("http://%s:%d/query", instance.Host.DomainName, instance.Host.Port),
			Params:   values,
			Headers:  map[string]string{"Accept": "application/json"},
			UserName: instance.Host.Username,
			Password: pwd,
		}
		resp, err := bl.client.Request(ctx, http.MethodGet, options)
		if err != nil {
			logger.Errorf("loadHostMetrics:Fail to load influxdb host(%s) metrics, %s, %v", instance.HostName, err, options)
			return
		}

		// 解析请求响应体
		defer resp.Body.Close()
		resultData := Response{}
		err = json.NewDecoder(resp.Body).Decode(&resultData)
		if err != nil {
			logger.Errorf("loadHostMetrics:Fail to decode response body, %s", err)
			return
		}

		recordData := make([]map[string]any, 0)
		for _, r := range resultData.Results {
			for _, s := range r.Series {
				if len(s.Values) == 0 {
					continue
				}
				// 默认只取一个点
				vs := s.Values[0]
				d := make(map[string]any)
				for idx, v := range vs {
					d[s.Columns[idx]] = v
				}
				// 替换时间戳字段为当前时间戳
				d["time"] = float64(time.Now().Unix())
				// 补充标签字段
				for k, v := range s.Tags {
					d[stringx.CamelToSnake(k)] = v
				}
				// 补充 bkm_% 内置标签字段
				for k, v := range instance.GetContext() {
					if m.IsInTags(k) {
						d[k] = v
					}
				}
				recordData = append(recordData, d)
			}
		}
		// 推送消息
		recordMetric := m
		bl.recordQueue <- &clustermetrics.Record{Instance: instance, Metric: &recordMetric, Data: recordData}
	}
}
