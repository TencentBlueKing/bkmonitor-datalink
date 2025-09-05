// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ratelimiter

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/ratelimiter/throttle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorRateLimiter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*rateLimiter, error) {
	rateLimiters := confengine.NewTierConfig()

	var c throttle.Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	rateLimiters.SetGlobal(throttle.New(c))

	for _, custom := range customized {
		var cfg throttle.Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		rateLimiters.Set(custom.Token, custom.Type, custom.ID, throttle.New(cfg))
	}

	return &rateLimiter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		rateLimiters:    rateLimiters,
	}, nil
}

type rateLimiter struct {
	processor.CommonProcessor
	rateLimiters *confengine.TierConfig // type ratelimiter.RateLimiter
}

func (p *rateLimiter) Name() string {
	return define.ProcessorRateLimiter
}

func (p *rateLimiter) IsDerived() bool {
	return false
}

func (p *rateLimiter) IsPreCheck() bool {
	return true
}

func (p *rateLimiter) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.rateLimiters = f.rateLimiters
}

func (p *rateLimiter) Process(record *define.Record) (*define.Record, error) {
	token := record.Token.Original
	rl := p.rateLimiters.GetByToken(token).(throttle.RateLimiter)
	logger.Debugf("ratelimiter: token [%s] max qps allowed: %f", token, rl.QPS())
	if !rl.TryAccept() {
		return nil, errors.Errorf("ratelimiter rejected the request, token [%s] max qps allowed: %f", token, rl.QPS())
	}
	return nil, nil
}
