// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpush

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/outputs"
	"github.com/elastic/beats/libbeat/publisher"
	"github.com/spf13/cast"

	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	gseinfo "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/logp"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/monitoring/report/bkpipe"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
)

const (
	dataIDKey  = "X-BK-DATA-ID"
	outputType = "bkpush"
)

func init() {
	outputs.RegisterType(outputType, MakeOutput)
}

type Config struct {
	RetryTimes          int           `config:"retrytimes"`
	RetryInterval       time.Duration `config:"retryinterval"`
	Endpoint            string        `config:"endpoint"`
	Timeout             time.Duration `config:"timeout"`
	Concurrency         int           `config:"concurrency"`
	EventBufferMax      int           `config:"eventbuffermax"`
	MaxIdleConns        int           `config:"maxidleconns"`
	MaxIdleConnsPerHost int           `config:"maxidleconnsperhost"`
	FlowLimit           int           `config:"flowlimit"` // unit: Bytes（仅在大于 0 时生效）
}

func (c *Config) Validate() error {
	if c.Endpoint == "" {
		return fmt.Errorf("no endpoint specified")
	}

	if c.Timeout <= 0 {
		c.Timeout = time.Minute
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 1
	}
	if c.EventBufferMax <= 0 {
		c.EventBufferMax = 128
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = 10
	}
	if c.MaxIdleConnsPerHost <= 0 {
		c.MaxIdleConnsPerHost = 20
	}
	return nil
}

type Record struct {
	DataID int32
	Data   interface{}
}

type Output struct {
	config *Config
	client *http.Client
	fl     *bkcommon.FlowLimiter
	ch     chan *Record
	stop   chan struct{}
}

func MakeOutput(_ outputs.IndexManager, _ beat.Info, _ outputs.Observer, cfg *common.Config) (outputs.Group, error) {
	output, err := New(cfg)
	if err != nil {
		return outputs.Fail(err)
	}
	return outputs.Success(output.config.EventBufferMax, 0, output)
}

func New(cfg *common.Config) (*Output, error) {
	config := &Config{}
	if err := cfg.Unpack(config); err != nil {
		return nil, err
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	cli := &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: config.Timeout,
			}).DialContext,
			MaxIdleConns:        config.MaxIdleConns,
			MaxIdleConnsPerHost: config.MaxIdleConnsPerHost,
			IdleConnTimeout:     2 * time.Minute,
		},
	}

	o := &Output{
		config: config,
		client: cli,
		ch:     make(chan *Record, config.Concurrency),
		stop:   make(chan struct{}),
	}

	if config.FlowLimit > 0 {
		o.fl = bkcommon.NewFlowLimiter(config.FlowLimit)
	}

	for i := 0; i < config.Concurrency; i++ {
		go o.loopHandle()
	}

	bkpipe.InitSender(o, gseinfo.AgentInfo{}) // TODO(mando): 兼容 bkpipe 自定义指标上报 ┓(=´∀`=)┏
	return o, nil
}

func (o *Output) Close() error {
	close(o.stop)
	return nil
}

func (o *Output) Report(dataid int32, data common.MapStr) error {
	r := &Record{
		DataID: dataid,
		Data:   data,
	}

	select {
	case o.ch <- r:
	case <-o.stop:
		return nil
	}

	return nil
}

func (o *Output) String() string { return outputType }

func (o *Output) Publish(batch publisher.Batch) error {
	events := batch.Events()
	for i := 0; i < len(events); i++ {
		event := events[i]
		if event.Content.Fields == nil {
			continue
		}
		if err := o.publish(event); err != nil {
			logp.L.Errorf("failed to publish event: %v", err)
		}
	}
	batch.ACK()
	return nil
}

func (o *Output) publish(event publisher.Event) error {
	content := event.Content
	data := content.Fields

	val, err := data.GetValue("dataid")
	if err != nil {
		return fmt.Errorf("event lost dataid")
	}
	dataid := cast.ToInt32(val)
	if dataid <= 0 {
		return fmt.Errorf("dataid %d <= 0", dataid)
	}

	if ok, _ := data.HasKey("gseindex"); !ok {
		data["gseindex"] = gse.GetGseIndex(dataid)
	}

	r := &Record{
		DataID: dataid,
		Data:   data,
	}

	select {
	case o.ch <- r:
	case <-o.stop:
		return nil
	}

	return nil
}

func (o *Output) doRequest(record *Record) error {
	buf, err := gse.MarshalFunc(record.Data)
	if err != nil {
		return err
	}

	if o.fl != nil {
		o.fl.Consume(len(buf))
	}

	req, _ := http.NewRequest(http.MethodPost, o.config.Endpoint, bytes.NewBuffer(buf))
	req.Header.Add(dataIDKey, fmt.Sprintf("%d", record.DataID))

	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (o *Output) loopHandle() {
	f := func(record *Record) {
		n := o.config.RetryTimes + 1
		for i := 0; i < n; i++ {
			if err := o.doRequest(record); err != nil {
				logp.L.Errorf("failed to post record, count=%d, err: %v", i, err)
				time.Sleep(o.config.RetryInterval)
				continue
			}
			return
		}
	}

	for {
		select {
		case <-o.stop:
			return

		case r := <-o.ch:
			f(r)
		}
	}
}
