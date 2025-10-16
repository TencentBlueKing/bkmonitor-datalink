// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tokenchecker

import (
	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorTokenChecker, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*tokenChecker, error) {
	decoders := confengine.NewTierConfig()
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	c.Clean()
	decoders.SetGlobal(NewTokenDecoder(*c))
	configs.SetGlobal(*c)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		cfg.Clean()
		decoders.Set(custom.Token, custom.Type, custom.ID, NewTokenDecoder(*cfg))
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)
	}

	return &tokenChecker{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		decoders:        decoders,
		configs:         configs,
	}, nil
}

type tokenChecker struct {
	processor.CommonProcessor
	decoders *confengine.TierConfig // type: Decoder
	configs  *confengine.TierConfig // type: Config
}

func (p *tokenChecker) Name() string {
	return define.ProcessorTokenChecker
}

func (p *tokenChecker) IsDerived() bool {
	return false
}

func (p *tokenChecker) IsPreCheck() bool {
	return true
}

func (p *tokenChecker) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.decoders = f.decoders
	p.configs = f.configs
}

func (p *tokenChecker) Process(record *define.Record) (*define.Record, error) {
	decoder := p.decoders.GetByToken(record.Token.Original).(TokenDecoder)
	config := p.configs.GetByToken(record.Token.Original).(Config)

	var err error
	switch record.RecordType {
	case define.RecordTraces:
		err = p.processTraces(decoder, config, record)
	case define.RecordMetrics:
		err = p.processMetrics(decoder, config, record)
	case define.RecordLogs:
		err = p.processLogs(decoder, config, record)
	case define.RecordProxy:
		err = p.processProxy(decoder, record)
	case define.RecordFta:
		err = p.processFta(decoder, record)
	default:
		err = p.processCommon(decoder, record)
	}
	return nil, err
}

// processFta Fta Token 解析
func (p *tokenChecker) processFta(decoder TokenDecoder, record *define.Record) error {
	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	record.Token, err = decoder.Decode(record.Token.Original)
	if err != nil {
		return errors.Wrap(err, "failed to decode token")
	}
	token := record.Token

	// 检查 DataID 及 PluginID 是否合法
	if token.MetricsDataId <= 0 {
		return errors.New("reject invalid dataId")
	}
	if token.AppName == "" {
		return errors.New("reject invalid pluginId")
	}

	record.Data.(*define.FtaData).PluginId = token.AppName
	return nil
}

// 对于 OT 的 token 解析优先级
// # HTTP Protocol
// 1) HTTP Headers -> X-BK-TOKEN
// 2) Span ResourceKey -> bk.data.token/...
//
// # GRPC Protocol
// 1) Span ResourceKey -> bk.data.token/...
//
// Note: 理论上来讲，单次请求包只能有一个 token，不支持多 token 场景。
// 支持从多个 attribute.keys 中读取 token

func tokenFromAttrs(attrs pcommon.Map, keys []string) string {
	for _, key := range keys {
		v, ok := attrs.Get(key)
		if ok {
			return v.AsString()
		}
	}
	return ""
}

// token 的解析也应该遵循一定的优先级
// 1) 从 headers 中提取
// 2) 从 attributes 中提取

func decodeToken(decoder TokenDecoder, src ...string) (define.Token, error) {
	var errs []error
	for _, s := range src {
		token, err := decoder.Decode(s)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		return token, nil
	}

	// 进入到这里一定是解析失败
	var token define.Token
	if len(errs) > 0 {
		return token, errs[0]
	}
	return token, errors.New("no token source")
}

func (p *tokenChecker) processTraces(decoder TokenDecoder, config Config, record *define.Record) error {
	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	var errs []error
	pdTraces := record.Data.(ptrace.Traces)
	pdTraces.ResourceSpans().RemoveIf(func(resourceSpans ptrace.ResourceSpans) bool {
		rsToken := tokenFromAttrs(resourceSpans.Resource().Attributes(), config.resourceKeys)
		if len(rsToken) > 0 {
			record.Token, err = decodeToken(decoder, rsToken)
			if err != nil {
				errs = append(errs, err)
				logger.Errorf("failed to parse pdTraces.rsToken (%s), err: %v", rsToken, err)
				return true
			}
			return false
		}

		record.Token, err = decodeToken(decoder, record.Token.Original)
		if err != nil {
			errs = append(errs, err)
			logger.Errorf("failed to parse pdTraces.original (%s), err: %v", record.Token.Original, err)
			return true
		}
		return false
	})

	// 当且仅当没有任何 spans 的情况下才算鉴权失败
	if pdTraces.ResourceSpans().Len() == 0 {
		if len(errs) > 0 {
			return errors.Wrapf(define.ErrSkipEmptyRecord, "drop spans cause %s", errs[0])
		}
		return define.ErrSkipEmptyRecord
	}
	return nil
}

func (p *tokenChecker) processMetrics(decoder TokenDecoder, config Config, record *define.Record) error {
	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	var errs []error
	pdMetrics := record.Data.(pmetric.Metrics)
	pdMetrics.ResourceMetrics().RemoveIf(func(resourceMetrics pmetric.ResourceMetrics) bool {
		rsToken := tokenFromAttrs(resourceMetrics.Resource().Attributes(), config.resourceKeys)
		if len(rsToken) > 0 {
			record.Token, err = decodeToken(decoder, rsToken)
			if err != nil {
				errs = append(errs, err)
				logger.Errorf("failed to parse pdMetrics.rsToken (%s), err: %v", rsToken, err)
				return true
			}
			return false
		}

		record.Token, err = decodeToken(decoder, record.Token.Original)
		if err != nil {
			errs = append(errs, err)
			logger.Errorf("failed to parse pdMetrics.original (%s), err: %v", record.Token.Original, err)
			return true
		}
		return false
	})

	// 当且仅当没有任何 metrics 的情况下才算鉴权失败
	if pdMetrics.ResourceMetrics().Len() == 0 {
		if len(errs) > 0 {
			return errors.Wrapf(define.ErrSkipEmptyRecord, "drop metrics cause %s", errs[0])
		}
		return define.ErrSkipEmptyRecord
	}
	return nil
}

func (p *tokenChecker) processLogs(decoder TokenDecoder, config Config, record *define.Record) error {
	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	var errs []error
	pdLogs := record.Data.(plog.Logs)
	pdLogs.ResourceLogs().RemoveIf(func(resourceLogs plog.ResourceLogs) bool {
		rsToken := tokenFromAttrs(resourceLogs.Resource().Attributes(), config.resourceKeys)
		if len(rsToken) > 0 {
			record.Token, err = decodeToken(decoder, rsToken)
			if err != nil {
				errs = append(errs, err)
				logger.Errorf("failed to parse pdLogs.rsToken (%s), err: %v", rsToken, err)
				return true
			}
			return false
		}

		record.Token, err = decodeToken(decoder, record.Token.Original)
		if err != nil {
			errs = append(errs, err)
			logger.Errorf("failed to parse pdLogs.original (%s), err: %v", record.Token.Original, err)
			return true
		}
		return false
	})

	if pdLogs.ResourceLogs().Len() == 0 {
		if len(errs) > 0 {
			return errors.Wrapf(define.ErrSkipEmptyRecord, "drop logs cause %s", errs[0])
		}
		return define.ErrSkipEmptyRecord
	}
	return nil
}

func (p *tokenChecker) processProxy(decoder TokenDecoder, record *define.Record) error {
	var err error
	record.Token, err = decoder.Decode(tokenparser.WrapProxyToken(record.Token))
	return err
}

func (p *tokenChecker) processCommon(decoder TokenDecoder, record *define.Record) error {
	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	record.Token, err = decoder.Decode(record.Token.Original)
	return err
}
