// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter

import (
	"time"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// CheckRecordHandler 检查基本字段是否为空
func CheckRecordHandler(isLogData bool) define.ETLRecordChainingHandler {
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		if record.Time == nil {
			return errors.Wrapf(define.ErrValue, "record time is empty")
		}

		// 对于日志数据的处理，指标和维度可以不必关注，这里如果为nil的话，则补充一个长度为0的空map进去
		if isLogData {
			if record.Metrics == nil {
				record.Metrics = make(map[string]interface{})
			}
			if record.Dimensions == nil {
				record.Dimensions = make(map[string]interface{})
			}
		} else {
			if len(record.Metrics) == 0 {
				return errors.Wrapf(define.ErrValue, "record metrics is empty")
			}
			if len(record.Dimensions) == 0 {
				return errors.Wrapf(define.ErrValue, "record dimensions is empty")
			}
		}

		return next(record)
	}
}

// CheckNilRecordHandler 检查 record 是否为空
func CheckNilRecordHandler(record *define.ETLRecord, next define.ETLRecordHandler) error {
	if record.Time == nil {
		return errors.Wrapf(define.ErrValue, "record time is nil")
	}

	if record.Metrics == nil {
		return errors.Wrapf(define.ErrValue, "record metrics is nil")
	}

	if record.Dimensions == nil {
		return errors.Wrapf(define.ErrValue, "record dimensions is nil")
	}

	return next(record)
}

// FormatDimensionsHandler 格式化维度值为字符串
func FormatDimensionsHandler(record *define.ETLRecord, next define.ETLRecordHandler) error {
	dimensions := make(map[string]interface{}, len(record.Dimensions))
	for key, val := range record.Dimensions {
		switch value := val.(type) {
		case string:
			dimensions[key] = value
		default:
			dimensions[key] = conv.String(value)
		}
	}
	record.Dimensions = dimensions
	return next(record)
}

func fetchCCTopoResponseStore(record *define.ETLRecord, store define.Store) (*models.CCTopoBaseModelInfo, error) {
	info, _, err := fetchCCTopoResponse(record, store, ExtraMetaNone)
	return info, err
}

func fetchExtraMetaResponseStore(record *define.ETLRecord, store define.Store, metaType ExtraMetaType) (string, error) {
	_, s, err := fetchCCTopoResponse(record, store, metaType)
	return s, err
}

func fetchCCTopoResponse(record *define.ETLRecord, store define.Store, metaType ExtraMetaType) (*models.CCTopoBaseModelInfo, string, error) {
	var (
		modelInfo  models.CCInfo
		err        error
		isHostInfo bool
		extraMeta  string
	)

	if val, ok := record.Dimensions[define.RecordBkTargetServiceInstanceID]; ok && val != nil {
		modelInfo = &models.CCInstanceInfo{InstanceID: conv.String(val)}
	} else {
		ip, ipOk := record.Dimensions[define.RecordIPFieldName]
		if !ipOk {
			logging.Debugf("unable fill from store without IP in %v", record)
			return nil, "", err
		}
		cloudID, cloudOk := record.Dimensions[define.RecordCloudIDFieldName]
		if !cloudOk {
			logging.Debugf("unable fill from store without cloud ID in %v", record)
			return nil, "", err
		}
		modelInfo = &models.CCHostInfo{IP: conv.String(ip), CloudID: conv.Int(cloudID)}
		isHostInfo = true
	}

	err = modelInfo.LoadStore(store)
	if err != nil {
		return nil, "", errors.Wrapf(err, "key: %v not found", modelInfo)
	}

	// 这里 dbm_meta/devx_meta 应该只能两者取其一
	if isHostInfo {
		switch metaType {
		case ExtraMetaDbm:
			if obj, ok := modelInfo.(*models.CCHostInfo); ok && len(obj.DbmMeta) > 0 {
				extraMeta = obj.DbmMeta
			}
		case ExtraMetaDevx:
			if obj, ok := modelInfo.(*models.CCHostInfo); ok && len(obj.DevxMeta) > 0 {
				extraMeta = obj.DevxMeta
			}
		}
	}

	ccTopo, ccTopoOk := modelInfo.GetInfo().(*models.CCTopoBaseModelInfo)
	if !ccTopoOk {
		logging.Warnf("unknown topo structure in key: %v", modelInfo)
		return nil, "", err
	}
	return ccTopo, extraMeta, err
}

func FillCmdbLevelHandlerCreator(topo []interface{}, store define.Store, enable bool) define.ETLRecordChainingHandler {
	// 如果已经被补充过topo or 配置为不补充层级结构
	if len(topo) != 0 || !enable {
		return nil
	}

	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		topoValue, ok := record.Dimensions[define.RecordCMDBLevelFieldName]
		// now: 如果采集器上报了cmdb结构，那么不去关心cmdb的对错以及是否带有自定义结构
		if ok && topoValue != nil {
			return next(record)
		}
		ccTopo, err := fetchCCTopoResponseStore(record, store)
		if ccTopo == nil {
			logging.Debugf("unable to fill bk_cmdb_level in %v no topo found in store ", record)
			return next(record)
		}
		if err != nil {
			logging.Warnf("unable to fill bk_cmdb_level by %v", err)
			return next(record)
		}

		var bkCmdbLevel []map[string]string
		if v, ok := ccTopo.GetInfo().(*models.CCTopoBaseModelInfo); ok {
			singleLevelMapHelper := utils.NewMapStringHelper(map[string]string{})
			for _, value := range v.Topo {
				singleTopoMapHelper := utils.NewMapStringHelper(value)
				// 如果发现topo 没有业务
				if record.Dimensions[define.RecordBizIDFieldName] != nil {
					// topo 有业务 仅将该业务下topo 写入
					if cacheBizValue, ok := singleTopoMapHelper.Get(define.RecordBizIDFieldName); ok && cacheBizValue == conv.String(record.Dimensions[define.RecordBizIDFieldName]) {
						for objID, instID := range value {
							if !singleLevelMapHelper.Exists(objID) {
								singleLevelMapHelper.Set(objID, instID)
							}
						}
					}
				}
			}
			bkCmdbLevel = append(bkCmdbLevel, singleLevelMapHelper.Data)
		}

		record.Dimensions[define.RecordCMDBLevelFieldName], err = etl.TransformJSON(bkCmdbLevel)
		if err != nil {
			logging.Warnf("unable to fill bk_cmdb_level by %v", err)
			return next(record)
		}
		return next(record)
	}
}

func FillBizIDHandlerCreator(store define.Store, rtConfig *config.MetaResultTableConfig) define.ETLRecordChainingHandler {
	var isBizFilledNeed bool

	// 如果判断结果表字段中，没有bk_biz_id的字段，那么不必进行补充
	for _, fieldInfo := range rtConfig.FieldList {
		if fieldInfo.FieldName == define.RecordBizIDFieldName {
			isBizFilledNeed = true
			logging.Infof("biz_id_field->[%s] is found in field list, will filled the biz_id field.", define.RecordBizIDFieldName)
			break
		}
	}

	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		var err error

		if _, ok := record.Dimensions[define.RecordBizIDFieldName]; ok || !isBizFilledNeed {
			return next(record)
		}

		ccTopo, err := fetchCCTopoResponseStore(record, store)
		if err != nil || ccTopo == nil {
			logging.Infof("record->[%v] unable to fill bk_biz_id for->[%v], but will continue process.", record, err)
			return next(record)
		}

		for _, value := range ccTopo.BizID {
			record.Dimensions[define.RecordBizIDFieldName] = conv.String(value)
			err = next(record)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

// FillDefaultValueCreator
func FillDefaultValueCreator(enable bool, rt *config.MetaResultTableConfig) define.ETLRecordChainingHandler {
	if !enable {
		return nil
	}
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		for _, value := range rt.FieldList {
			switch value.Tag {
			case define.MetaFieldTagDimension:
				if _, ok := record.Dimensions[value.FieldName]; !ok {
					record.Dimensions[value.FieldName] = value.DefaultValue
				}
			case define.MetaFieldTagMetric:
				if _, ok := record.Metrics[value.FieldName]; !ok {
					record.Metrics[value.FieldName] = value.DefaultValue
				}
			}
		}
		return next(record)
	}
}

// FillSupplierIDHandler : 填充开发商ID
func FillSupplierIDHandler(record *define.ETLRecord, next define.ETLRecordHandler) error {
	_, ok := record.Dimensions[define.RecordSupplierIDFieldName]
	if ok {
		return next(record)
	}

	record.Dimensions[define.RecordSupplierIDFieldName] = 0 // default supplier id
	return next(record)
}

// RoundingTimeHandlerCreator : 对齐时间精度
func RoundingTimeHandlerCreator(duration string) define.ETLRecordChainingHandler {
	precision, err := time.ParseDuration(duration)
	if err != nil {
		return nil
	}

	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		recordTime, err := record.GetTime()
		if err != nil {
			return errors.Wrapf(err, "get record time error")
		}

		recordTime = recordTime.Round(precision)

		*record.Time = recordTime.Unix()
		return next(record)
	}
}

// AlignTimeUnitHandler 对齐时间单位 支持 s/ms/μs/ns
func AlignTimeUnitHandler(unit string) define.ETLRecordChainingHandler {
	if unit == "" {
		return nil
	}

	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		if record.Time != nil {
			newTs := utils.ConvertTimeUnitAs(*record.Time, unit)
			record.Time = &newTs
		}
		return next(record)
	}
}

func transformFields(from map[string]interface{}, transformers map[string]etl.TransformFn, allowMissing bool) (map[string]interface{}, error) {
	to := make(map[string]interface{}, len(from))
	for key, fn := range transformers {
		value, ok := from[key]
		if !ok {
			if !allowMissing {
				return nil, errors.Wrapf(define.ErrItemNotFound, "field %v not found", key)
			}
			continue
		}
		value, err := fn(value)
		if err != nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "transform field %v error %v", key, err)
		}

		// TODO(mando): 暂时没有找到优雅的方案处理 db record 先在这里做个断言
		r, ok := value.(etl.DbmRecord)
		if !ok {
			to[key] = value // 普通类型处理
			continue
		}

		to[r.BodyFieldName] = r.Body
		to[r.ResponseFieldName] = r.Response
	}
	return to, nil
}

// TransformMetricsHandlerCreator : 按照配置格式化指标类型
func TransformMetricsHandlerCreator(transformers map[string]etl.TransformFn, allowMetricsMissing bool) define.ETLRecordChainingHandler {
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		metrics, err := transformFields(record.Metrics, transformers, allowMetricsMissing)
		if err != nil {
			return err
		}
		if !allowMetricsMissing && len(metrics) == 0 {
			return errors.Wrapf(define.ErrOperationForbidden, "metrics has nothing")
		}
		record.Metrics = metrics

		return next(record)
	}
}

// TransformDimensionsHandlerCreator : 按照配置格式化维度类型
func TransformDimensionsHandlerCreator(transformers map[string]etl.TransformFn, allowDimensionsMissing bool) define.ETLRecordChainingHandler {
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		dimensions, err := transformFields(record.Dimensions, transformers, allowDimensionsMissing)
		if err != nil {
			return err
		}
		record.Dimensions = dimensions

		return next(record)
	}
}

// MetricsCutterHandler : 将指标切分成多条记录
func MetricsCutterHandler(record *define.ETLRecord, next define.ETLRecordHandler) error {
	metrics := record.Metrics
	for key, value := range metrics {
		record.Metrics = map[string]interface{}{
			define.MetricValueFieldName: value,
		}
		record.Dimensions[define.MetricKeyFieldName] = key
		err := next(record)
		if err != nil {
			return err
		}
	}
	return nil
}

func tryDecodeExtraMeta(s string) ([]map[string]string, error) {
	type V1Meta struct {
		Common map[string]string   `json:"common"`
		Custom []map[string]string `json:"custom"`
	}

	parseV0Meta := func(b []byte) ([]map[string]string, error) {
		ret := make([]map[string]string, 0)
		err := json.Unmarshal(b, &ret)
		if err != nil {
			return nil, err
		}
		return ret, nil
	}

	parseV1Meta := func(b []byte) ([]map[string]string, error) {
		var v1Meta V1Meta
		if err := json.Unmarshal(b, &v1Meta); err != nil {
			return nil, err
		}
		ret := make([]map[string]string, 0)
		for _, custom := range v1Meta.Custom {
			item := make(map[string]string)
			for k, v := range custom {
				item[k] = v
			}
			for k, v := range v1Meta.Common {
				item[k] = v
			}
			ret = append(ret, item)
		}
		return ret, nil
	}

	type tryV1 struct {
		Version string `json:"version"`
	}

	var tryv1 tryV1
	var ret []map[string]string

	// 尝试用最小代价解析 version 字段，判断其是否为 v1 格式
	// 不同版本格式
	// v0: []map[string]string
	// v1: V1Meta
	err := json.Unmarshal([]byte(s), &tryv1)
	if err == nil && tryv1.Version == "v1" {
		ret, err = parseV1Meta([]byte(s))
	} else {
		ret, err = parseV0Meta([]byte(s))
	}

	if err != nil {
		return nil, err
	}
	if len(ret) <= 0 {
		return nil, errors.New("empty extra meta record items")
	}
	return ret, nil
}

type ExtraMetaType uint8

const (
	ExtraMetaNone ExtraMetaType = iota
	ExtraMetaDbm
	ExtraMetaDevx
)

func TransferRecordCutterByDbmMetaCreator(store define.Store, enabled bool) define.ETLRecordChainingHandler {
	return transferRecordCutterByExtraMetaCreator(store, ExtraMetaDbm, enabled)
}

func TransferRecordCutterByDevxMetaCreator(store define.Store, enabled bool) define.ETLRecordChainingHandler {
	return transferRecordCutterByExtraMetaCreator(store, ExtraMetaDevx, enabled)
}

func transferRecordCutterByExtraMetaCreator(store define.Store, metaType ExtraMetaType, enabled bool) define.ETLRecordChainingHandler {
	if !enabled {
		return nil
	}

	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		body, err := fetchExtraMetaResponseStore(record, store, metaType)
		if err != nil {
			return errors.Wrap(err, "failed to fetch extra meta response")
		}

		if len(body) <= 0 {
			return errors.New("empty extra meta response")
		}

		items, err := tryDecodeExtraMeta(body)
		if err != nil {
			return err
		}

		// 维度补充
		for _, item := range items {
			for k, v := range item {
				record.Dimensions[k] = v
			}
			if err := next(record); err != nil {
				return err
			}
		}
		return nil
	}
}

// RecordCutterByCmdbLevelHandler : 将数据按照层级拆分成多条
func TransferRecordCutterByCmdbLevelCreator(cmdbLevelConf []interface{}, enable bool) define.ETLRecordChainingHandler {
	if len(cmdbLevelConf) == 0 || !enable {
		return nil
	}
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		var topo []map[string]interface{}
		cmdbLevel := conv.String(record.Dimensions[define.RecordCMDBLevelFieldName])
		if err := json.Unmarshal([]byte(cmdbLevel), &topo); err != nil {
			return err
		}
		for _, level := range cmdbLevelConf {
			for _, topo := range topo {
				data := utils.NewMapHelper(topo)
				if l, ok := data.Get(conv.String(level)); ok {
					// 此处需要将bk_biz_id/bk_set_id/bk_module_id转换为biz/set/module
					switch level {
					case define.RecordBizIDFieldName:
						record.Dimensions[define.RecordCMDBLevelNameFieldName] = define.RecordBizName

					case define.RecordBkSetID:
						record.Dimensions[define.RecordCMDBLevelNameFieldName] = define.RecordSetName

					case define.RecordBkModuleID:
						record.Dimensions[define.RecordCMDBLevelNameFieldName] = define.RecordModuleName

					default:
						record.Dimensions[define.RecordCMDBLevelNameFieldName] = level
					}
					record.Dimensions[define.RecordCMDBLevelIDFieldName] = l

					err := next(record)
					if err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
}

// LocalTimeInjectHandlerCreator : 指标注入当前时间
func LocalTimeInjectHandlerCreator(field string, enable bool) define.ETLRecordChainingHandler {
	if !enable {
		return nil
	}
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		record.Metrics[field] = time.Now().Unix()
		return next(record)
	}
}

//	MetricsAsFloat64Creator : 移除不符合标准的指标项 并将bool类型转为0 or 1
//
// 注 value = "1" 该条应该被保留为value = 1
func MetricsAsFloat64Creator(enable bool) define.ETLRecordChainingHandler {
	if !enable { // 该处为用户配置
		return nil
	}
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		metrics := make(map[string]interface{})
		for key, value := range record.Metrics {
			if metricsValue, err := conv.DefaultConv.Float64(value); err == nil {
				metrics[key] = metricsValue
			}
		}
		record.Metrics = metrics
		return next(record) // 将record 塞到chan中
	}
}

// TransformAliasNameHandlerCreator : 字段别名
func TransformAliasNameHandlerCreator(rt *config.MetaResultTableConfig, enable bool) define.ETLRecordChainingHandler {
	if !enable {
		return nil
	}

	mappingsDimensions := make(map[string]string)
	mappingsMetrics := make(map[string]string)

	for _, f := range rt.FieldList {
		if f.AliasName != "" {
			switch f.Tag {
			case define.MetaFieldTagDimension:
				mappingsDimensions[f.FieldName] = f.AliasName
			case define.MetaFieldTagMetric:
				mappingsMetrics[f.FieldName] = f.AliasName
			}
		}
	}
	if len(mappingsDimensions) == 0 && len(mappingsMetrics) == 0 {
		return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
			return next(record)
		}
	}

	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		// 考虑到Dimensions 和 Metrics 中的字段有可能重名,因此分成两个map
		for fieldName, aliasName := range mappingsMetrics {
			if record.Metrics[fieldName] != nil {
				record.Metrics[aliasName] = record.Metrics[fieldName]
			}
		}
		for fieldName, aliasName := range mappingsDimensions {
			if record.Dimensions[fieldName] != nil {
				// ip 字段别名需要特殊处理
				// 需要将 define.RecordTmpUserIPFieldName 替换为 ip 别名同时删除该维度
				//
				// 分情况讨论
				// 1) 内置 dataid field_list 中 ip 字段别名为 bk_target_ip 此时不应该做替换
				// 2) 用户自定义上报数据上报 优先以用户的字段为准
				tmpIP, ok := record.Dimensions[define.RecordTmpUserIPFieldName]
				if fieldName == define.RecordIPFieldName && ok {
					record.Dimensions[aliasName] = tmpIP
					delete(record.Dimensions, define.RecordTmpUserIPFieldName)
				} else {
					record.Dimensions[aliasName] = record.Dimensions[fieldName]
				}
			}
		}
		return next(record)
	}
}
