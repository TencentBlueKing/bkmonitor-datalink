// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	version "github.com/hashicorp/go-version"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// BulkHandler :
type BulkHandler struct {
	pipeline.BaseBulkHandler
	resultTable   *config.MetaResultTableConfig
	uniqueField   []string
	flushInterval time.Duration
	writer        BulkWriter
	indexRender   IndexRenderFn
	transformers  map[string]etl.TransformFn
}

func (b *BulkHandler) makeRecordID(values map[string]interface{}) string {
	hash := md5.New()
	for _, key := range b.uniqueField {
		_, err := fmt.Fprintf(hash, "%#v", values[key])
		logging.PanicIf(err)
	}

	return hex.EncodeToString(hash.Sum(nil))
}

func (b *BulkHandler) asRecord(etlRecord *define.ETLRecord) (*Record, error) {
	values := make(map[string]interface{}, len(etlRecord.Metrics)+len(etlRecord.Dimensions)+1)
	for key, value := range etlRecord.Metrics {
		values[key] = value
	}
	for key, value := range etlRecord.Dimensions {
		values[key] = value
	}
	if etlRecord.Time != nil {
		values[define.TimeFieldName] = utils.ParseTimeStamp(*etlRecord.Time)
	}

	for name, transformer := range b.transformers {
		value, ok := values[name]
		if !ok {
			logging.Warnf("field %s not found in %#v", name, values)
			continue
		}
		result, err := transformer(value)
		if err != nil {
			return nil, err
		}
		values[name] = result
	}

	record := NewRecord(values)
	record.SetID(b.makeRecordID(values))
	record.SetType(b.resultTable.ResultTable)

	return record, nil
}

// Product
func (b *BulkHandler) Handle(ctx context.Context, payload define.Payload, killChan chan<- error) (result interface{}, at time.Time, ok bool) {
	var etlRecord define.ETLRecord
	err := payload.To(&etlRecord)
	if err != nil {
		logging.Warnf("%v error %v dropped payload %+v", b, err, payload)
		return nil, time.Time{}, false
	}

	return &etlRecord, utils.ParseTimeStamp(*etlRecord.Time), true
}

func (b *BulkHandler) flush(ctx context.Context, index string, records Records) (count int, err error) {
	logging.Debugf("backend %v flush %d records", b, len(records))

	errs := utils.NewMultiErrors()
	response, err := b.writer.Write(ctx, index, records)

	var e error
	var result []byte
	if response != nil {
		defer func() {
			logging.WarnIf("close response error", response.Body.Close())
		}()
		result, e = io.ReadAll(response.Body)
		errs.Add(e)
	}

	switch {
	case err != nil:
		logging.Warnf("backend %v flush error %v", b, err)
		errs.Add(errors.Wrapf(err, "%v write failed", b))

	case response == nil:
		errs.Add(errors.Wrapf(define.ErrDisaster, "response is nil"))

	case response.IsSysError():
		logging.Errorf("backend %v flush failed because server error %s", b, result)
		errs.Add(errors.Wrapf(define.ErrOperationForbidden, "response %d, %s", response.StatusCode, result))

	default:
		logging.Debugf("backend %v write response status code %d", b, response.StatusCode)
		var writeResult ESWriteResult
		if err := json.Unmarshal(result, &writeResult); err != nil {
			logging.Errorf("backend %v parse elasticsearch response error %v from %s, skipped", b, err, result)
			break
		}

		if writeResult.Errors {
			msg := fmt.Sprintf("backend %v write %d documents to elasticsearch failed, response: %s", b, writeResult.Took, result)
			logging.MinuteErrorSampling(b.String(), msg)
			var total int
			for _, item := range writeResult.Items {
				index := item.Index
				if index.Error != nil {
					total++
					cause := index.Error.CausedBy
					logging.Warnf("backend %v write %v to %v error %v:%v", b, index.ID, index.Index, cause.Type, cause.Reason)
				}
			}
			MonitorESBackendPartialWrite.WithLabelValues(b.resultTable.ResultTable).Add(float64(total))
		} else {
			count = len(writeResult.Items)
			if count != len(records) {
				logging.Warnf("backend %v write %d documents to elasticsearch with ack %d, please check why data lost, response: %s", b, len(records), count, string(result))
			}
		}
	}

	return count, errs.AsError()
}

// Flush :
func (b *BulkHandler) Flush(ctx context.Context, results []interface{}) (count int, err error) {
	lastIndex := ""
	errs := utils.NewMultiErrors()
	records := make(Records, 0, len(results))
	for _, value := range results {
		payload := value.(*define.ETLRecord)
		record, err := b.asRecord(payload)
		if err != nil {
			logging.Errorf("backend %v format payload %#v error %v", b, payload, err)
			errs.Add(err)
			continue
		}

		index, err := b.indexRender(record)
		if err != nil {
			logging.Errorf("backend %v render index for %#v error %v", b, record, err)
			errs.Add(err)
			continue
		}

		logging.Debugf("backend %v ready to flush record %#v to index %s", b, record, index)

		// 处理跨时间间隔
		if index != lastIndex && lastIndex != "" {
			cnt, err := b.flush(ctx, index, records)
			records = records[:0]
			count += cnt
			errs.Add(err)
		}
		lastIndex = index
		records = append(records, record)
	}

	if len(records) > 0 {
		cnt, err := b.flush(ctx, lastIndex, records)
		count += cnt
		errs.Add(err)
	}

	return count, errs.AsError()
}

// Close :
func (b *BulkHandler) Close() error {
	return b.writer.Close()
}

// BulkHandler
func NewBulkHandler(cluster *config.ElasticSearchMetaClusterInfo, table *config.MetaResultTableConfig, flushInterval time.Duration, uniqueFields []string, indexRender IndexRenderFn) (*BulkHandler, error) {
	ver, err := version.NewVersion(cluster.GetVersion())
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("v%d", ver.Segments()[0])
	logging.Infof("create elasticsearch writer %s by version %s", name, ver.String())

	// 从集群配置中读取 ssl_insecure_skip_verify 决定是否跳过证书校验
	transport := NewTransportWithTLS(cluster.GetSSLInsecureSkipVerify())

	authConf := utils.NewMapHelper(cluster.AuthInfo)
	writer, err := NewBulkWriter(name, map[string]interface{}{
		"Addresses": []string{cluster.GetAddress()},
		"Username":  authConf.GetOrDefault("username", ""),
		"Password":  authConf.GetOrDefault("password", ""),
		"Transport": transport,
	})
	if err != nil {
		return nil, err
	}

	transformers := make(map[string]etl.TransformFn)
	err = table.VisitUserSpecifiedFields(func(field *config.MetaFieldConfig) error {
		switch field.Type {
		case define.MetaFieldTypeTimestamp:
			options := utils.NewMapHelper(field.Option)
			if options.Exists(config.MetaFieldOptESFormat) {
				name := options.MustGetString(config.MetaFieldOptESFormat)
				transformers[field.Name()] = etl.TransformTimeStingByName(name)
				logging.Debugf("rt %s will format time field %s to string by name %s", table.ResultTable, field.Name(), name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	handler := &BulkHandler{
		resultTable:   table,
		flushInterval: flushInterval,
		writer:        writer,
		uniqueField:   uniqueFields,
		indexRender:   indexRender,
		transformers:  transformers,
	}
	return handler, nil
}

// NewBackend :
func NewBackend(ctx context.Context, name string, maxQps int) (define.Backend, error) {
	conf := config.FromContext(ctx)
	resultTable := config.ResultTableConfigFromContext(ctx)

	option := utils.NewMapHelper(resultTable.Option)
	uniqueFields := make([]string, 0)
	specifiedUniqueFields, ok := option.GetArray(config.ResultTableOptLogUniqueFields)
	if ok {
		for _, value := range specifiedUniqueFields {
			switch field := value.(type) {
			case string:
				uniqueFields = append(uniqueFields, field)
			}
		}
	}

	shipper := config.ShipperConfigFromContext(ctx)
	cluster := shipper.AsElasticSearchCluster()
	flushInterval := conf.GetDuration(pipeline.ConfKeyPayloadFlushInterval)
	clusterConf := utils.NewMapHelper(cluster.ClusterConfig)
	clusterConf.SetDefault("version", conf.GetString(ConfKeyDefaultVersion))

	fn, err := ConfigTemplateRender(cluster)
	if err != nil {
		return nil, err
	}

	bulk, err := NewBulkHandler(cluster, resultTable, flushInterval, uniqueFields, fn)
	if err != nil {
		return nil, err
	}

	return pipeline.NewBulkBackendDefaultAdapter(ctx, name, bulk, maxQps), nil
}

func init() {
	define.RegisterBackend("elasticsearch", func(ctx context.Context, name string) (backend define.Backend, e error) {
		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
		}
		if config.ShipperConfigFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "shipper is empty")
		}

		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
		}

		options := utils.NewMapHelper(rt.Option)
		maxQps, _ := options.GetInt(config.PipelineConfigOptMaxQps)
		return NewBackend(ctx, rt.FormatName(name), maxQps)
	})
}
