// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package infos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/influxdata/influxql"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	queryMod "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type InfoType string

const (
	TagKeys    InfoType = "tag_keys"
	TagValues  InfoType = "tag_values"
	FieldKeys  InfoType = "field_keys"
	Series     InfoType = "series"
	TimeSeries InfoType = "time_series"
	FieldMap   InfoType = "field_map"
)

// Params
type Params struct {
	DataSource string             `json:"data_source"`
	TableID    structured.TableID `json:"table_id"`
	Metric     string             `json:"metric_name"`
	// IsRegexp 指标是否使用正则查询
	IsRegexp bool `json:"is_regexp" example:"false"`

	Conditions structured.Conditions `json:"conditions"`
	Keys       []string              `json:"keys"`

	Limit  int `json:"limit"`
	Slimit int `json:"slimit"`

	Start string `json:"start_time"`
	End   string `json:"end_time"`

	Timezone string `json:"timezone,omitempty" example:"Asia/Shanghai"`
}

func (p *Params) StartTimeUnix() (int64, error) {
	return strconv.ParseInt(p.Start, 10, 64)
}

func (p *Params) EndTimeUnix() (int64, error) {
	return strconv.ParseInt(p.End, 10, 64)
}

// AnalysisQuery
func AnalysisQuery(stmt string) (*Params, error) {
	var query *Params
	err := json.Unmarshal([]byte(stmt), &query)
	if err != nil {
		return nil, err
	}
	return query, nil
}

var defaultLimit int

// getTime
func getTime(timestamp string) (time.Time, error) {
	timeNum, err := strconv.Atoi(timestamp)
	if err != nil {
		return time.Time{}, errors.New("parse time failed")
	}
	return time.Unix(int64(timeNum), 0), nil
}

// generateSQL
func generateSQL(
	_ context.Context, infoType InfoType, db, measurement, field string, whereList *promql.WhereList, sLimit, limit int,
) (influxdb.SQLInfo, error) {
	var (
		err     error
		sqlInfo influxdb.SQLInfo
	)

	sqlInfo.DB = db

	switch infoType {
	case Series:
		sqlInfo.SQL = `show series`
		if measurement != "" {
			sqlInfo.SQL = fmt.Sprintf(`%s from %s`, sqlInfo.SQL, influxql.QuoteIdent(measurement))
		}
	case TagKeys:
		sqlInfo.SQL = `show tag keys`
		if measurement != "" {
			sqlInfo.SQL = fmt.Sprintf(`%s from %s`, sqlInfo.SQL, influxql.QuoteIdent(measurement))
		}
	case TagValues:
		sqlInfo.SQL = `show tag values`
		if measurement != "" {
			sqlInfo.SQL = fmt.Sprintf(`%s from %s`, sqlInfo.SQL, influxql.QuoteIdent(measurement))
		}
		sqlInfo.SQL = fmt.Sprintf(`%s with key in ("%s")`, sqlInfo.SQL, field)
	case FieldKeys:
		sqlInfo.SQL = `show field keys`
		if measurement != "" {
			sqlInfo.SQL = fmt.Sprintf(`%s from %s`, sqlInfo.SQL, influxql.QuoteIdent(measurement))
		}
	case TimeSeries:
		if measurement == "" {
			measurement = `/.*/`
		}
		sqlInfo.SQL = fmt.Sprintf(
			`select %s, *::tag from %s`,
			influxql.QuoteIdent(field), influxql.QuoteIdent(measurement),
		)
	default:
		return sqlInfo, errors.New(`unknown info type`)
	}
	// 检查sql注入
	if err = influxdb.CheckSQLInject(sqlInfo.SQL); err != nil {
		return sqlInfo, err
	}

	if infoType == FieldKeys {
		return sqlInfo, err
	}
	whereStr := whereList.String()
	if whereStr != "" {
		sqlInfo.SQL = fmt.Sprintf("%s where %s", sqlInfo.SQL, whereStr)
	}
	if limit > 0 {
		sqlInfo.SQL = fmt.Sprintf("%s limit %d", sqlInfo.SQL, limit)
	}
	if sLimit > 0 {
		sqlInfo.SQL = fmt.Sprintf("%s slimit %d", sqlInfo.SQL, sLimit)
	}

	return sqlInfo, err
}

// makeInfluxQLList
func makeInfluxQLListBySpaceUid(
	ctx context.Context, infoType InfoType, params *Params, whereList *promql.WhereList, spaceUid string,
) ([]influxdb.SQLInfo, error) {
	var (
		err          error
		influxQLList []influxdb.SQLInfo
		limit        int
		tsDBs        []*queryMod.TsDBV2
	)

	if params.Limit > 0 {
		limit = params.Limit
	} else {
		limit = defaultLimit
	}

	ctx, span := trace.NewSpan(ctx, "make-influxQL-list-by-space-uid")
	defer span.End(&err)

	user := metadata.GetUser(ctx)
	tsDBs, err = structured.GetTsDBList(ctx, &structured.TsDBOption{
		SpaceUid:    spaceUid,
		TableID:     params.TableID,
		FieldName:   params.Metric,
		IsSkipSpace: user.IsSkipSpace(),
	})
	if err != nil {
		return nil, err
	}

	for _, tsDB := range tsDBs {
		var (
			field        string
			newWhereList = promql.NewWhereList()
			metricName   string
			sqlInfo      influxdb.SQLInfo
			// 如果有额外condition，则录入where语句中
			conditions [][]promql.ConditionField
		)
		db := tsDB.DB
		measurement := tsDB.Measurement
		storageID := tsDB.StorageID

		// 增加过滤条件
		for _, filter := range tsDB.Filters {
			var cond []promql.ConditionField
			for k, v := range filter {
				if v != "" {
					cond = append(cond, promql.ConditionField{
						DimensionName: k,
						Value:         []string{v},
						Operator:      promql.EqualOperator,
					})
				}
			}
			if len(cond) > 0 {
				conditions = append(conditions, cond)
			}
		}

		if infoType == TimeSeries {
			for _, metricName = range params.Keys {
				// 指针的值拷贝，因为下面还会对 whereList 进行操作
				*newWhereList = *whereList

				// 单指标单表
				if tsDB.MeasurementType == redis.BkSplitMeasurement {
					measurement = metricName
					field = promql.StaticField
				} else {
					// 判断是否是行专列
					if tsDB.MeasurementType == redis.BkExporter {
						field = promql.StaticMetricValue
						newWhereList.Append(
							promql.AndOperator, promql.NewWhere(
								promql.StaticMetricName, metricName, promql.EqualOperator, promql.StringType,
							))
					} else {
						field = metricName
					}
				}

				if len(conditions) > 0 {
					newWhereList.Append(promql.AndOperator, promql.NewTextWhere(promql.MakeOrExpression(conditions)))
				}
				sqlInfo, err = generateSQL(ctx, infoType, db, measurement, field, newWhereList, params.Slimit, limit)
				sqlInfo.ClusterID = storageID
				sqlInfo.MetricName = metricName
				if err != nil {
					return influxQLList, err
				}
				influxQLList = append(influxQLList, sqlInfo)
			}
		} else {
			// 指针的值拷贝，因为下面还会对 whereList 进行操作
			*newWhereList = *whereList

			// 单指标单表
			if tsDB.MeasurementType == redis.BkSplitMeasurement {
				measurement = params.Metric
			} else {
				// 判断是否是行专列
				if tsDB.MeasurementType == redis.BkExporter {
					if params.Metric != "" {
						newWhereList.Append(
							promql.AndOperator, promql.NewWhere(
								promql.StaticMetricName, params.Metric, promql.EqualOperator, promql.StringType,
							))
					}
				}
			}

			field = strings.Join(params.Keys, `","`)

			if len(conditions) > 0 {
				newWhereList.Append(promql.AndOperator, promql.NewTextWhere(promql.MakeOrExpression(conditions)))
			}
			sqlInfo, err = generateSQL(ctx, infoType, db, measurement, field, newWhereList, params.Slimit, limit)
			sqlInfo.ClusterID = storageID
			sqlInfo.MetricName = metricName
			if err != nil {
				return influxQLList, err
			}
			influxQLList = append(influxQLList, sqlInfo)
		}
	}

	return influxQLList, err
}

// makeInfluxQLList
func makeInfluxQLList(
	ctx context.Context, infoType InfoType, params *Params, spaceUid string,
) ([]influxdb.SQLInfo, error) {
	var (
		err          error
		influxQLList []influxdb.SQLInfo
		limit        int
		whereList    = promql.NewWhereList()
	)

	ctx, span := trace.NewSpan(ctx, "make-influxQL-list")
	defer span.End(&err)

	if params.Limit > 0 {
		limit = params.Limit
	} else {
		limit = defaultLimit
	}
	condition, err := params.Conditions.AnalysisConditions()
	if err != nil {
		return nil, err
	}
	if len(condition) != 0 {
		influxdbCondition := structured.ConvertToPromBuffer(condition)
		if len(influxdbCondition) > 0 {
			whereList.Append(
				promql.AndOperator, promql.NewTextWhere(
					promql.MakeOrExpression(influxdbCondition),
				))
		}

	}
	// 增加时间维度查询，秒级转纳秒
	if params.Start != "" && params.End != "" {
		start, timeErr := getTime(params.Start)
		if timeErr != nil {
			return nil, timeErr
		}
		end, timeErr := getTime(params.End)
		if timeErr != nil {
			return nil, timeErr
		}

		whereList.Append(
			promql.AndOperator, promql.NewWhere(
				"time", fmt.Sprintf("%d", start.UnixNano()), promql.UpperEqualOperator, promql.NumType,
			),
		)
		whereList.Append(
			promql.AndOperator, promql.NewWhere(
				"time", fmt.Sprintf("%d", end.UnixNano()), promql.LowerOperator, promql.NumType,
			),
		)
	}

	if spaceUid != "" {
		return makeInfluxQLListBySpaceUid(ctx, infoType, params, whereList, spaceUid)
	}

	var tableInfos []*consul.TableID
	tableIDFilter, err := structured.NewTableIDFilter(params.Metric, params.TableID, nil, params.Conditions)
	if err != nil {
		return influxQLList, nil
	}
	if !tableIDFilter.IsAppointTableID() {
		dataIDList := tableIDFilter.DataIDList()
		for _, dataID := range dataIDList {
			tableInfo := influxdb.GetTableIDsByDataID(dataID)
			if len(tableInfo) == 0 {
				continue
			}
			tableInfos = append(tableInfos, tableInfo...)
		}
	} else {
		routes := tableIDFilter.GetRoutes()
		for _, route := range routes {
			tableInfos = append(tableInfos, influxdb.GetTableIDByDBAndMeasurement(
				route.DB(), route.Measurement(),
			))
		}
	}

	for _, tableID := range tableInfos {
		var (
			db           = tableID.DB
			measurement  string
			field        string
			newWhereList = promql.NewWhereList()
			metricName   string
			sqlInfo      influxdb.SQLInfo
		)

		if infoType == TimeSeries {
			for _, metricName = range params.Keys {
				// 指针的值拷贝，因为下面还会对 whereList 进行操作
				*newWhereList = *whereList

				// 单指标单表
				if tableID.IsSplit() {
					measurement = metricName
					field = promql.StaticField
				} else {
					measurement = tableID.Measurement
					// 判断是否是行专列
					if influxdb.IsPivotTable(tableID.String()) {
						field = promql.StaticMetricValue
						newWhereList.Append(
							promql.AndOperator, promql.NewWhere(
								promql.StaticMetricName, metricName, promql.EqualOperator, promql.StringType,
							))
					} else {
						field = metricName
					}
				}

				sqlInfo, err = generateSQL(ctx, infoType, db, measurement, field, newWhereList, params.Slimit, limit)
				sqlInfo.ClusterID = tableID.ClusterID
				sqlInfo.MetricName = metricName
				if err != nil {
					return influxQLList, err
				}
				influxQLList = append(influxQLList, sqlInfo)
			}
		} else {
			// 指针的值拷贝，因为下面还会对 whereList 进行操作
			*newWhereList = *whereList

			// 单指标单表
			if tableID.IsSplit() {
				measurement = params.Metric
			} else {
				measurement = tableID.Measurement
				// 判断是否是行专列
				if influxdb.IsPivotTable(tableID.String()) {
					if params.Metric != "" {
						newWhereList.Append(
							promql.AndOperator, promql.NewWhere(
								promql.StaticMetricName, params.Metric, promql.EqualOperator, promql.StringType,
							),
						)
					}
				}
			}

			field = strings.Join(params.Keys, `","`)

			sqlInfo, err = generateSQL(ctx, infoType, db, measurement, field, newWhereList, params.Slimit, limit)
			sqlInfo.ClusterID = tableID.ClusterID
			sqlInfo.MetricName = metricName
			if err != nil {
				return influxQLList, err
			}
			influxQLList = append(influxQLList, sqlInfo)
		}
	}

	return influxQLList, err
}

// QueryAsync 查询信息
func QueryAsync(ctx context.Context, infoType InfoType, params *Params, spaceUid string) (*influxdb.Tables, error) {
	sqlInfos, err := makeInfluxQLList(ctx, infoType, params, spaceUid)
	if err != nil {
		codedErr := errno.ErrBusinessLogicError().
			WithComponent("信息查询处理器").
			WithOperation("生成SQL查询列表").
			WithContext("info_type", string(infoType)).
			WithContext("space_uid", spaceUid).
			WithContext("error", err.Error()).
			WithSolution("检查查询参数和表配置")
		log.ErrorWithCodef(ctx, codedErr)
		return nil, err
	}
	log.Debugf(context.TODO(), "get sqlInfos:%#v", sqlInfos)

	result, errs := influxdb.QueryInfosAsync(ctx, sqlInfos, "", params.Limit)
	if len(errs) != 0 {
		codedErr := errno.ErrStorageConnFailed().
			WithComponent("信息查询处理器").
			WithOperation("执行异步查询").
			WithContext("info_type", string(infoType)).
			WithContext("errors", fmt.Sprintf("%v", errs)).
			WithSolution("检查InfluxDB连接和SQL语法")
		log.ErrorWithCodef(ctx, codedErr)
		return nil, errs[0]
	}

	return result, nil
}
