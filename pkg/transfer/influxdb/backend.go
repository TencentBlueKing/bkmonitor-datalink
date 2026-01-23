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
	"time"

	"github.com/influxdata/influxdb/client/v2"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type (
	// Client :
	Client = client.Client
	// HTTPConfig :
	HTTPConfig = client.HTTPConfig
)

// NewHTTPClient :
var (
	NewHTTPClient = client.NewHTTPClient
	BackendName   = "influxdb"
)

func isDisabledField(field *config.MetaFieldConfig) bool {
	options := utils.NewMapHelper(field.Option)
	value, ok := options.GetBool(config.MetaFieldOptInfluxDisabled)

	// 其它地方暂时不使用，放函数内
	disabledFieldName := []string{define.RecordCMDBLevelFieldName}

	if utils.IsStringInSlice(field.FieldName, disabledFieldName) || (ok && value) {
		return true
	}
	return false
}

// addExemplar: 将采样数据写入到metric map中
func addExemplar(exemplar map[string]interface{}, metrics map[string]interface{}) {
	for k, v := range exemplar {
		switch k {
		case "bk_trace_timestamp", "bk_trace_value":
			f, err := etl.TransformFloat64(v)
			if err != nil {
				continue
			}
			metrics[k] = f
		default:
			metrics[k] = v
		}
	}
}

// BulkHandler
type BulkHandler struct {
	pipeline.BaseBulkHandler
	dbName                string
	tableName             string
	retentionPolicy       string
	cli                   client.Client
	disabledMetrics       []string
	disabledDimensions    []string
	mustIncludeDimensions []string
	isSplitMeasurement    bool
}

func (b *BulkHandler) cleanRecord(record *Record) bool {
	for _, key := range b.disabledDimensions {
		delete(record.Dimensions, key)
	}

	for _, key := range b.disabledMetrics {
		delete(record.Metrics, key)
	}
	return !record.Clean()
}

// Product
func (b *BulkHandler) Handle(ctx context.Context, payload define.Payload, killChan chan<- error) (result interface{}, at time.Time, ok bool) {
	// 此处将Payload改变为实际的influxdb的point内容
	var record Record

	err := payload.To(&record)
	if err != nil {
		logging.Warnf("%v error %v dropped payload %+v", b, err, payload)
		return nil, time.Time{}, false
	}

	// 如果有 mustIncludeDimensions 则表示在本次 record 中 必须存在要求的`所有维度`
	if len(b.mustIncludeDimensions) > 0 {
		for _, d := range b.mustIncludeDimensions {
			if v := record.Dimensions[d]; v == nil {
				return nil, time.Time{}, false
			}
		}
	}

	// 如果是非单指标单表的模式，需要将exemplar的数据放置到指标中
	// 方便和指标数据写入到同一行记录中
	if !b.isSplitMeasurement {
		addExemplar(record.Exemplar, record.Metrics)
	}

	if b.cleanRecord(&record) {
		logging.Warnf("%v error %v dropped payload %+v for metric is empty", b, err, payload)
		return nil, time.Time{}, false
	}

	ts := utils.ParseTimeStamp(record.Time)

	// 分表逻辑打开时，基于metrics进行表名拆分
	if b.isSplitMeasurement {
		var pointList []*client.Point
		for metricName, metricValue := range record.Metrics {
			// 判断是否存在exemplar的信息, 并将数据和指标写入到一起
			metrics := map[string]interface{}{
				"value": metricValue,
			}
			// 如果是单指标单表，那在写入前将采样数据和指标合入到一行中
			addExemplar(record.Exemplar, metrics)
			// 单指标单表的情况下，需要将单个记录变成多个点返回到外部，此时返回的是[]Points
			point, err := client.NewPoint(metricName, record.GetDimensions(), metrics, ts)
			if err != nil {
				logging.Warnf("%v skipping influx data point %#v with error %v", b, record, err)
				return nil, time.Time{}, false
			}
			pointList = append(pointList, point)
		}

		return pointList, ts, true
	} else {
		// 非单指标单表的，则直接返回单个点即可
		var point *client.Point
		point, err = client.NewPoint(
			b.tableName, record.GetDimensions(), record.Metrics, ts,
		)
		if err != nil {
			logging.Warnf("%v skipping influx data point %#v with error %v", b, record, err)
			return nil, time.Time{}, false
		}

		return point, ts, true
	}
}

// Flush
func (b *BulkHandler) Flush(ctx context.Context, results []interface{}) (int, error) {
	count := len(results)

	points, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:        b.dbName,
		Precision:       "ns",
		RetentionPolicy: b.retentionPolicy,
	})
	if err != nil {
		return 0, errors.WithMessagef(err, "%v create points", b)
	}

	for _, value := range results {
		// 如果发现数组中的元素是多个点组成的，则需要进一步的拆解增加点
		if resultList, ok := value.([]*client.Point); ok {
			for _, r := range resultList {
				points.AddPoint(r)
			}
		} else {
			// 否则按照已有的方式处理即可
			points.AddPoint(value.(*client.Point))
		}
	}

	logging.Debugf("%v ready to push %d remains", b, count)

	err = b.cli.Write(points)
	if err != nil {
		return 0, errors.WithMessagef(err, "%v write points", b)
	}

	return count, nil
}

// Close : close backend, should call Wait() function to wait
func (b *BulkHandler) Close() error {
	return b.cli.Close()
}

func (b *BulkHandler) SetETLRecordFields(f *define.ETLRecordFields) {}

// NewBulkBackend
func NewBulkHandler(rt *config.MetaResultTableConfig, shipper *config.MetaClusterInfo) (*BulkHandler, error) {
	cluster := shipper.AsInfluxCluster()
	dbName := cluster.GetDataBase()
	tableName := cluster.GetTable()

	// Create a new HTTPClient
	addr := cluster.GetAddress()
	conf := client.HTTPConfig{Addr: addr}

	auth := config.NewAuthInfo(shipper)
	userName, err := auth.GetUserName()
	if err != nil {
		logging.Debugf("%v may not establish connection %v: username", addr, define.ErrGetAuth)
	}
	passWord, err := auth.GetPassword()
	if err != nil {
		logging.Debugf("%v may not establish connection %v: password", addr, define.ErrGetAuth)
	}
	if userName != "" || passWord != "" && err != nil {
		conf.Username = userName
		conf.Password = passWord
	}

	cli, err := NewHTTPClient(conf)
	if err != nil {
		logging.Errorf("new %s.%s http client failed:%v", dbName, tableName, err)
		return nil, err
	}

	logging.Infof("influx %s.%s connect to %s", dbName, tableName, addr)

	var disabledMetrics, disabledDimensions []string
	err = rt.VisitFieldByTag(func(field *config.MetaFieldConfig) error {
		if isDisabledField(field) {
			disabledMetrics = append(disabledMetrics, field.Name())
		}
		return nil
	}, func(field *config.MetaFieldConfig) error {
		if isDisabledField(field) {
			disabledDimensions = append(disabledDimensions, field.Name())
		}
		return nil
	})

	util := utils.MapHelper{Data: rt.Option}
	isSplitMeasurement := util.GetOrDefault(config.ResultTableOptIsSplitMeasurement, false).(bool)

	var mustDimensions []string
	arr, _ := util.GetArray(config.ResultTableOptMustIncludeDimensions)
	if len(arr) > 0 {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				mustDimensions = append(mustDimensions, s)
			}
		}
	}

	return &BulkHandler{
		dbName:                dbName,
		tableName:             tableName,
		retentionPolicy:       cluster.GetRetentionPolicy(),
		cli:                   cli,
		disabledMetrics:       disabledMetrics,
		disabledDimensions:    disabledDimensions,
		mustIncludeDimensions: mustDimensions,
		isSplitMeasurement:    isSplitMeasurement,
	}, nil
}

// Backend :
type Backend struct {
	*pipeline.BulkBackendAdapter
}

// NewBackend : client can only re-use but not share
func NewBackend(ctx context.Context, name string, maxQps int) (*Backend, error) {
	bulk, err := NewBulkHandler(
		config.ResultTableConfigFromContext(ctx),
		config.ShipperConfigFromContext(ctx),
	)
	if err != nil {
		return nil, err
	}

	return &Backend{
		BulkBackendAdapter: pipeline.NewBulkBackendDefaultAdapter(ctx, name, bulk, maxQps),
	}, nil
}

func init() {
	define.RegisterBackend(BackendName, func(ctx context.Context, name string) (define.Backend, error) {
		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
		}
		if config.ShipperConfigFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "shipper config is empty")
		}
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "resultTable config is empty")
		}

		options := utils.NewMapHelper(pipeConfig.Option)
		maxQps, _ := options.GetInt(config.PipelineConfigOptMaxQps)
		influxdbBackend, err := NewBackend(ctx, pipeConfig.FormatName(name), maxQps)
		if rt.SchemaType == config.ResultTableSchemaTypeFree && options.GetOrDefault(config.PipelineConfigOptDisableMetricCutter, false) == false {
			if err != nil {
				return nil, err
			}
			return pipeline.NewBackendWithCutterAdapter(ctx, influxdbBackend), err
		}
		return NewBackend(ctx, pipeConfig.FormatName(name), maxQps)
	})
}
