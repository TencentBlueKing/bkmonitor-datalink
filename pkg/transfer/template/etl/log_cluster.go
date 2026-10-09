// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	nameLogCluster = "log_cluster"
)

type LogClusterConfig struct {
	Address       string `mapstructure:"address" json:"address"`
	Timeout       string `mapstructure:"timeout" json:"timeout"`
	Retry         int    `mapstructure:"retry" json:"retry"`
	RetryInterval string `mapstructure:"retry_interval" json:"retry_interval"`
	BatchSize     int    `mapstructure:"batch_size" json:"batch_size"`
	PollInterval  string `mapstructure:"poll_interval" json:"poll_interval"`       // ms 单位
	MinGap        string `mapstructure:"min_gap" json:"min_gap"`                   // ticker 触发时最小相邻发送间隔
	Field         string `mapstructure:"clustering_field" json:"clustering_field"` // 聚类的字段
}

func (c LogClusterConfig) GetField() string {
	if c.Field == "" {
		return "log"
	}
	return c.Field
}

func (c LogClusterConfig) GetTimeout() time.Duration {
	if c.Timeout == "" {
		return time.Minute
	}

	v, err := time.ParseDuration(c.Timeout)
	if err != nil || v <= 0 {
		return time.Minute
	}
	return v
}

func (c LogClusterConfig) GetMinGap() time.Duration {
	if c.MinGap == "" {
		return time.Second
	}

	v, err := time.ParseDuration(c.MinGap)
	if err != nil || v <= 0 {
		return time.Second
	}
	return v
}

func (c LogClusterConfig) GetPollInterval() time.Duration {
	var l, r int
	if c.PollInterval == "" {
		l, r = 1000, 5000
		n := (l + (rand.Int() % (r - l))) / 100 * 100 // 取整数毫秒
		return time.Duration(n) * time.Millisecond
	}

	var s1, s2 string
	if strings.Contains(c.PollInterval, "-") {
		parts := strings.Split(c.PollInterval, "-")
		if len(parts) >= 2 {
			s1, s2 = parts[0], parts[1]
		}
	} else {
		s1, s2 = c.PollInterval, c.PollInterval
	}

	l, _ = strconv.Atoi(s1)
	r, _ = strconv.Atoi(s2)

	if l <= 1000 {
		l = 1000 // ticker 最小周期为 1s
	}
	if r < l {
		r = l
	}
	if l == r {
		return time.Duration(l) * time.Millisecond
	}

	n := (l + (rand.Int() % (r - l))) / 100 * 100 // 取整数毫秒
	return time.Duration(n) * time.Millisecond
}

func (c LogClusterConfig) GetRetryInterval() time.Duration {
	if c.RetryInterval == "" {
		return 200 * time.Millisecond
	}

	v, err := time.ParseDuration(c.RetryInterval)
	if err != nil || v <= 0 {
		return 200 * time.Millisecond
	}
	return v
}

func (c LogClusterConfig) GetBatchSize() int {
	if c.BatchSize <= 0 {
		return 2000
	}
	return c.BatchSize
}

type LogCluster struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor

	conf  LogClusterConfig
	queue *innerQueue
	cli   *http.Client
	mut   sync.Mutex

	lastRequestUnix int64
}

type innerQueue struct {
	size int
	q    []*define.ETLRecord
}

func (iq *innerQueue) Push(record *define.ETLRecord) bool {
	iq.q = append(iq.q, record)
	return len(iq.q) >= iq.size
}

func (iq *innerQueue) Pop() []*define.ETLRecord {
	q := iq.q
	iq.q = make([]*define.ETLRecord, 0)
	return q
}

type LogClusterRequest struct {
	Index     string `json:"__index__"`
	Log       string `json:"log"`
	Timestamp int64  `json:"timestamp"`
}

type LogClusterResponse struct {
	Signature string `json:"signature"`
	Pattern   string `json:"pattern"`
	IsNew     int    `json:"is_new"`
}

func (p *LogCluster) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	// 未初始话时透传即可
	if p.queue == nil {
		outputChan <- d
		return
	}

	p.mut.Lock() // 此 processor 会触发虚拟的 Process 事件 即有可能并发调用的情况 因此这里需要有个锁保护
	defer p.mut.Unlock()

	handle := func() {
		// 避免 send closed channel
		// 入参设计如此 ┓(-´∀`-)┏
		defer utils.RecoverError(func(err error) {
			logging.Errorf("%v process recover: %v", p, err)
		})

		p.lastRequestUnix = time.Now().Unix()
		batch := p.queue.Pop()
		if len(batch) == 0 {
			return // 队列无数据 放弃执行
		}

		// 聚类非关键路径 出错则按原始数据写入即可
		// 至少保证原始数据不丢弃
		rsp, err := p.doRequestWithRetry(batch)
		if err != nil {
			logging.Errorf("%v log_cluster request failed: %v", p.String(), err)
			rsp = batch
			p.CounterFails.Inc()
		} else {
			p.CounterSuccesses.Inc()
		}

		// 批处理之后需要逐个回放给到下一个 processor
		// 只有此 processor 是需要按批来处理
		for _, record := range rsp {
			var output define.Payload
			// 虚拟信号 先实例化 Payload
			if d == nil {
				output = define.NewDefaultPayload()
				err = output.From(&record)
			} else {
				output, err = define.DerivePayload(d, &record)
			}
			if err != nil {
				logging.Errorf("%v create payload from %v error: %v", p, d, err)
				continue
			}
			outputChan <- output
		}
	}

	// 虚拟 Process 充当信号使用
	if d == nil {
		if float64(time.Now().Unix()-p.lastRequestUnix) > p.conf.GetPollInterval().Seconds() {
			handle()
		}
		return
	}

	var dst define.ETLRecord
	if err := d.To(&dst); err != nil {
		p.CounterFails.Inc()
		logging.Errorf("payload %v to record failed: %v", d, err)
		return
	}

	// 非虚拟信号需要等待 full 时才进行推送
	full := p.queue.Push(&dst)
	if !full {
		return
	}
	handle()
}

func (p *LogCluster) getClusteringField(record *define.ETLRecord) string {
	if len(record.Metrics) > 0 {
		v, ok := record.Metrics[p.conf.GetField()]
		if ok && v != nil {
			return fmt.Sprintf("%s", v)
		}
	}
	if len(record.Dimensions) > 0 {
		v, ok := record.Dimensions[p.conf.GetField()]
		if ok && v != nil {
			return fmt.Sprintf("%s", v)
		}
	}
	return ""
}

func (p *LogCluster) doRequestWithRetry(records []*define.ETLRecord) ([]*define.ETLRecord, error) {
	var rsp []*define.ETLRecord
	var err error

	for i := 0; i < p.conf.Retry+1; i++ {
		rsp, err = p.doRequest(records)
		if err != nil {
			time.Sleep(p.conf.GetRetryInterval()) // 缓解服务端压力
			continue
		}
		return rsp, err
	}
	return rsp, err
}

func (p *LogCluster) doRequest(records []*define.ETLRecord) ([]*define.ETLRecord, error) {
	items := make([]LogClusterRequest, 0, len(records))
	for i, record := range records {
		field := p.getClusteringField(record)
		if len(field) == 0 {
			continue
		}
		items = append(items, LogClusterRequest{
			Index:     strconv.Itoa(i),
			Log:       field,
			Timestamp: *record.Time,
		})
	}

	b, err := json.Marshal(map[string]interface{}{
		"predict_args": map[string]interface{}{"is_only_predict": 1},
		"data":         items,
	})
	if err != nil {
		return nil, err
	}

	rsp, err := p.cli.Post(p.conf.Address, "application/json", bytes.NewBuffer(b))
	if err != nil {
		return nil, err
	}
	defer rsp.Body.Close()

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return nil, err
	}

	type Response struct {
		Data []LogClusterResponse `json:"data"`
	}
	var r Response
	err = json.Unmarshal(body, &r)
	if err != nil {
		return nil, err
	}

	// 聚类服务端保证数据是按序返回的
	// 这里假定已经是一一对应
	if len(r.Data) != len(records) {
		return nil, errors.Errorf("records length not match, want %d but got %d", len(records), len(r.Data))
	}

	for i := 0; i < len(records); i++ {
		records[i].Dimensions["signature"] = r.Data[i].Signature
		records[i].Dimensions["pattern"] = r.Data[i].Pattern
		records[i].Dimensions["is_new"] = r.Data[i].IsNew
	}
	return records, nil
}

func NewLogCluster(ctx context.Context, name string) (*LogCluster, error) {
	rtOption := config.PipelineConfigFromContext(ctx).Option
	unmarshal := func() (*LogClusterConfig, error) {
		v, ok := rtOption[config.PipelineConfigOptLogClusterConfig]
		if !ok {
			return nil, nil
		}
		obj, ok := v.(map[string]interface{})
		if !ok {
			return nil, nil
		}

		var conf LogClusterConfig
		err := mapstructure.Decode(obj[nameLogCluster], &conf)
		if err != nil {
			return nil, err
		}

		_, err = url.Parse(conf.Address)
		if err != nil {
			return nil, err
		}
		return &conf, nil
	}

	conf, err := unmarshal()
	if err != nil || conf == nil {
		// 创建失败即忽略本 processor 数据直接导向下一节点
		logging.Errorf("failed to unmarshal log_cluster config: %v", err)
		return &LogCluster{
			BaseDataProcessor: define.NewBaseDataProcessor(name),
			ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		}, nil
	}

	p := &LogCluster{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		conf:              *conf,
		queue: &innerQueue{
			size: conf.GetBatchSize(),
		},
		cli: &http.Client{
			Timeout: conf.GetTimeout(),
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 64,
				IdleConnTimeout:     time.Minute * 5,
			},
		},
	}
	p.SetPoll(conf.GetPollInterval())
	return p, nil
}

func init() {
	define.RegisterDataProcessor(nameLogCluster, func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		rtConfig := config.ResultTableConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table config is empty")
		}
		rtName := rtConfig.ResultTable
		name = fmt.Sprintf("%s:%s", name, rtName)
		return NewLogCluster(ctx, pipeConfig.FormatName(name))
	})
}
