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
	stdErrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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

const maxBulkFailureDetails = 10

// BulkHandler :
type BulkHandler struct {
	pipeline.BaseBulkHandler
	resultTable   *config.MetaResultTableConfig
	uniqueField   []string
	flushInterval time.Duration
	writer        BulkWriter
	indexRender   IndexRenderFn
	transformers  map[string]etl.TransformFn
	isLogsDB      bool
}

type retryableFlushError struct {
	err       error
	retryable bool
}

func (e *retryableFlushError) Error() string {
	return e.err.Error()
}

func (e *retryableFlushError) Unwrap() error {
	return e.err
}

func (e *retryableFlushError) Retryable() bool {
	return e.retryable
}

func newRetryableFlushError(err error, retryable bool) error {
	if err == nil {
		return nil
	}
	return &retryableFlushError{err: err, retryable: retryable}
}

func shouldRetryFlushError(err error) bool {
	var retryable interface{ Retryable() bool }
	if stdErrors.As(err, &retryable) {
		return retryable.Retryable()
	}
	return true
}

func isTransientHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
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
	if etlRecord.Time == nil {
		return nil, errors.Wrapf(define.ErrValue, "record time is empty")
	}

	values := make(map[string]interface{}, len(etlRecord.Metrics)+len(etlRecord.Dimensions)+1)
	for key, value := range etlRecord.Metrics {
		values[key] = value
	}
	for key, value := range etlRecord.Dimensions {
		values[key] = value
	}
	eventTime := utils.ParseTimeStamp(*etlRecord.Time)
	values[define.TimeFieldName] = eventTime

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
	if b.isLogsDB {
		values["@timestamp"] = eventTime.UnixMilli()
	}

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

	if etlRecord.Time == nil {
		logging.Errorf("backend %v record time is empty", b)
		return &etlRecord, time.Time{}, true
	}

	return &etlRecord, utils.ParseTimeStamp(*etlRecord.Time), true
}

func (b *BulkHandler) flush(ctx context.Context, index string, records Records) (count int, err error) {
	logging.Debugf("backend %v flush %d records", b, len(records))

	response, err := b.writer.Write(ctx, index, records)
	if response != nil && response.Body != nil {
		defer func() {
			logging.WarnIf("close response error", response.Body.Close())
		}()
	}

	if err != nil {
		logging.Warnf("backend %v flush error %v", b, err)
		return 0, newRetryableFlushError(errors.Wrapf(err, "%v write failed", b), true)
	}
	if response == nil {
		return 0, newRetryableFlushError(errors.Wrapf(define.ErrDisaster, "elasticsearch response is nil"), true)
	}
	if response.Body == nil {
		return 0, newRetryableFlushError(errors.Wrapf(define.ErrDisaster, "elasticsearch response body is nil"), true)
	}

	result, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, newRetryableFlushError(errors.Wrapf(err, "backend %v read elasticsearch response failed", b), true)
	}
	if response.IsError() {
		logging.Errorf("backend %v flush failed with HTTP %d: %s", b, response.StatusCode, result)
		return 0, newRetryableFlushError(
			errors.Wrapf(define.ErrOperationForbidden, "elasticsearch response %d: %s", response.StatusCode, result),
			isTransientHTTPStatus(response.StatusCode),
		)
	}

	logging.Debugf("backend %v write response status code %d", b, response.StatusCode)
	var writeResult ESWriteResult
	if err := json.Unmarshal(result, &writeResult); err != nil {
		logging.Errorf("backend %v parse elasticsearch response error %v from %s", b, err, result)
		return 0, newRetryableFlushError(errors.Wrapf(err, "backend %v parse elasticsearch response failed", b), true)
	}
	if len(writeResult.Items) != len(records) {
		return 0, newRetryableFlushError(
			errors.Errorf("backend %v elasticsearch bulk response item count mismatch: got %d, want %d", b, len(writeResult.Items), len(records)),
			true,
		)
	}

	failureCount := 0
	failureDetails := make([]string, 0, maxBulkFailureDetails)
	retryable := false
	for _, item := range writeResult.Items {
		index := item.Index
		if index.Status >= http.StatusOK && index.Status < http.StatusMultipleChoices && index.Error == nil {
			count++
			continue
		}

		failureCount++
		if index.Error != nil {
			cause := index.Error.CausedBy
			logging.Warnf("backend %v write %v to %v status %d error %v:%v", b, index.ID, index.Index, index.Status, cause.Type, cause.Reason)
			if len(failureDetails) < maxBulkFailureDetails {
				failureDetails = append(failureDetails, fmt.Sprintf("id=%s status=%d type=%s reason=%s", index.ID, index.Status, index.Error.Type, index.Error.Reason))
			}
		} else {
			logging.Warnf("backend %v write %v to %v failed with status %d", b, index.ID, index.Index, index.Status)
			if len(failureDetails) < maxBulkFailureDetails {
				failureDetails = append(failureDetails, fmt.Sprintf("id=%s status=%d", index.ID, index.Status))
			}
		}
		if index.Status == 0 || isTransientHTTPStatus(index.Status) {
			retryable = true
		}
	}

	if failureCount > 0 || writeResult.Errors {
		if failureCount == 0 {
			retryable = true
		}
		MonitorESBackendPartialWrite.WithLabelValues(b.resultTable.ResultTable).Add(float64(failureCount))
		msg := fmt.Sprintf("backend %v elasticsearch bulk write partially failed: %d succeeded, %d failed", b, count, failureCount)
		if len(failureDetails) > 0 {
			msg = fmt.Sprintf("%s: %s", msg, strings.Join(failureDetails, "; "))
			if failureCount > len(failureDetails) {
				msg = fmt.Sprintf("%s; %d more failures", msg, failureCount-len(failureDetails))
			}
		}
		logging.MinuteErrorSampling(b.String(), msg)
		return count, newRetryableFlushError(errors.New(msg), retryable)
	}

	return count, nil
}

// Flush :
func (b *BulkHandler) Flush(ctx context.Context, results []interface{}) (count int, err error) {
	lastIndex := ""
	errs := utils.NewMultiErrors()
	retryable := false
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
			cnt, err := b.flush(ctx, lastIndex, records)
			records = records[:0]
			count += cnt
			errs.Add(err)
			if err != nil && shouldRetryFlushError(err) {
				retryable = true
			}
		}
		lastIndex = index
		records = append(records, record)
	}

	if len(records) > 0 {
		cnt, err := b.flush(ctx, lastIndex, records)
		count += cnt
		errs.Add(err)
		if err != nil && shouldRetryFlushError(err) {
			retryable = true
		}
	}

	return count, newRetryableFlushError(errs.AsError(), retryable)
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
	insecureSkipVerify := cluster.GetSSLInsecureSkipVerify()
	logging.Debugf("[DEBUG] cluster address: %s, ssl_insecure_skip_verify: %v, cluster_config: %+v", cluster.GetAddress(), insecureSkipVerify, cluster.ClusterConfig)
	transport := NewTransportWithTLS(insecureSkipVerify)

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
		isLogsDB:      cluster.IsLogsDB(),
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
