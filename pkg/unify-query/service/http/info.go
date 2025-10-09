// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	routerInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// TagValuesData
type TagValuesData struct {
	TraceID string              `json:"trace_id,omitempty"`
	Values  map[string][]string `json:"values"`
}

type SeriesDataList []*SeriesData

// SeriesData
type SeriesData struct {
	TraceID     string     `json:"trace_id,omitempty"`
	Measurement string     `json:"measurement"`
	Keys        []string   `json:"keys"`
	Series      [][]string `json:"series"`
}

// SplitByte : 把string分割成数组，兼容反斜杠
func SplitByte(str string, seq uint8) []string {
	var (
		lv uint8
		cv uint8

		r     []string
		start = 0

		backslash = uint8(92)
	)

	for i := 0; i < len(str); i++ {
		cv = str[i]
		if i == len(str)-1 {
			r = append(r, str[start:])
		} else if cv == seq && lv != backslash {
			r = append(r, str[start:i])
			start = i + 1
		}
		lv = cv
	}
	return r
}

// FormatSeriesData 解析返回格式
// 返回数据格式为：
// bk_apm_duration,apdex_type=tolerating,bk_instance_id=python:bk_monitorv3_web:::,
// http_method=GET,http_status_code=500,kind=3,service_name=bk_monitorv3_web,span_name=HTTP\
// GET,status_code=1,target=otlp,telemetry_sdk_language=python,telemetry_sdk_name=opentelemetry,
// telemetry_sdk_version=1.6.0
//
// 44: ,
// 61: =
// 92: \
func FormatSeriesData(infoData *InfoData, keys []string) []*SeriesData {
	dataList := make([]*SeriesData, 0)

	measurements := make(map[string]struct{}, 0)
	measurementKeys := make(map[string][]string)
	measurementSeries := make(map[string][][]string)

	keyExists := make(map[string]struct{})
	dataExists := make(map[string]struct{})
	for _, table := range infoData.Tables {
		for _, value := range table.Values {
			if len(value) != 1 {
				codedErr := errno.ErrDataDeserializeFailed().
					WithComponent("InfoData").
					WithOperation("解析系列数据字段").
					WithContext("原始数据", value).
					WithSolution("检查表格数据字段数量是否为1")
				log.ErrorWithCodef(context.TODO(), codedErr)
				continue
			}
			row := SplitByte(value[0].(string), 44)
			measurement := row[0]
			if _, ok := measurements[measurement]; !ok {
				measurements[measurement] = struct{}{}
			}
			if _, ok := measurementKeys[measurement]; !ok {
				measurementKeys[measurement] = make([]string, 0)
			}
			if _, ok := measurementSeries[measurement]; !ok {
				measurementSeries[measurement] = make([][]string, 0)
			}

			kv := make(map[string]string)
			for _, columnStr := range row[1:] {
				column := SplitByte(columnStr, 61)
				if len(column) != 2 {
					codedErr := errno.ErrDataDeserializeFailed().
						WithComponent("InfoData").
						WithOperation("解析标签数据").
						WithContext("原始数据", columnStr).
						WithSolution("检查标签分割后是否为键值对格式")
					log.ErrorWithCodef(context.TODO(), codedErr)
					continue
				}

				// 如果不传tag key，则取所有的key
				if len(keys) == 0 {
					if _, ok := keyExists[column[0]]; !ok {
						keyExists[column[0]] = struct{}{}
						measurementKeys[measurement] = append(measurementKeys[measurement], column[0])
					}
				} else {
					for _, k := range keys {
						if k != "" {
							if _, ok := keyExists[k]; !ok {
								keyExists[k] = struct{}{}
								measurementKeys[measurement] = append(measurementKeys[measurement], k)
							}
						}
					}
				}

				if _, ok := keyExists[column[0]]; ok {
					kv[column[0]] = column[1]
				}
			}

			l := make([]string, 0)
			dataKey := ""
			for _, k := range measurementKeys[measurement] {
				l = append(l, kv[k])
				dataKey = fmt.Sprintf("%s%s%s", dataKey, k, kv[k])
			}
			// 移除重复的series
			if _, ok := dataExists[dataKey]; ok {
				continue
			}
			measurementSeries[measurement] = append(measurementSeries[measurement], l)
			dataExists[dataKey] = struct{}{}
		}
	}

	for m := range measurements {
		dataList = append(dataList, &SeriesData{
			Measurement: m,
			Keys:        measurementKeys[m],
			Series:      measurementSeries[m],
		})
	}

	return dataList
}

// NewTagValuesData :
func NewTagValuesData(infoData *InfoData) *TagValuesData {
	result := new(TagValuesData)
	if len(infoData.Tables) == 0 {
		return result
	}

	hashKey := make(map[string]struct{})
	values := make(map[string][]string)
	// 默认0为tag key，1为tag value
	for _, table := range infoData.Tables {
		for _, value := range table.Values {
			if len(value) != 2 {
				codedErr := errno.ErrDataDeserializeFailed().
					WithComponent("TagValuesData").
					WithOperation("解析标签值数据字段").
					WithContext("原始数据", value).
					WithSolution("检查表格数据字段数量是否为2")
				log.ErrorWithCodef(context.TODO(), codedErr)
				continue
			}
			tagKey, ok := value[0].(string)
			if !ok {
				codedErr := errno.ErrDataDeserializeFailed().
					WithComponent("TagValuesData").
					WithOperation("解析标签键类型").
					WithContext("原始数据", value).
					WithSolution("检查字段类型是否为字符串")
				log.ErrorWithCodef(context.TODO(), codedErr)
				continue
			}
			tagValue, ok := value[1].(string)
			if !ok {
				codedErr := errno.ErrDataDeserializeFailed().
					WithComponent("TagValuesData").
					WithOperation("解析标签值类型").
					WithContext("原始数据", value).
					WithSolution("检查字段类型是否为字符串")
				log.ErrorWithCodef(context.TODO(), codedErr)
				continue
			}
			tagValues, ok := values[tagKey]
			if ok {
				// 去重，tagKey和tagValue一样则不需要写入
				checkKey := fmt.Sprintf("%s%s", tagKey, tagValue)
				if _, d := hashKey[checkKey]; !d {
					tagValues = append(tagValues, tagValue)
					hashKey[checkKey] = struct{}{}
				}
			} else {
				tagValues = make([]string, 0)
				tagValues = append(tagValues, tagValue)
			}
			values[tagKey] = tagValues
		}
	}

	result.Values = values

	return result
}

// InfoData 返回结构化数据
type InfoData struct {
	dimensions map[string]bool
	Tables     []*TablesItem `json:"series"`
}

// NewInfoData
func NewInfoData(dimensions []string) *InfoData {
	dimensionsMap := make(map[string]bool)
	for _, dimension := range dimensions {
		dimensionsMap[dimension] = true
	}
	return &InfoData{
		dimensions: dimensionsMap,
	}
}

// Fill
func (d *InfoData) Fill(tables *influxdb.Tables) error {
	d.Tables = make([]*TablesItem, 0)
	for index, table := range tables.Tables {
		tableItem := new(TablesItem)
		tableItem.Name = fmt.Sprintf("_result%d", index)
		tableItem.MetricName = table.MetricName
		tableItem.Columns = make([]string, 0, len(table.Headers))
		tableItem.Types = make([]string, 0, len(table.Headers))
		tableItem.GroupKeys = table.GroupKeys
		tableItem.GroupValues = table.GroupValues
		keyMap := make(map[string]bool)
		for _, key := range table.GroupKeys {
			keyMap[key] = true
		}

		indexList := make([]int, 0, len(table.Headers))
		for index, header := range table.Headers {
			// 是key则不输出
			if _, ok := keyMap[header]; ok {
				continue
			}
			if len(d.dimensions) != 0 {
				if _, ok := d.dimensions[header]; !ok {
					continue
				}
			}
			// 记录需要返回的字段及其索引
			tableItem.Columns = append(tableItem.Columns, header)
			tableItem.Types = append(tableItem.Types, table.Types[index])
			indexList = append(indexList, index)
		}
		values := make([][]any, 0)
		for _, data := range table.Data {
			value := make([]any, len(indexList))
			for valueIndex, headerIndex := range indexList {
				value[valueIndex] = data[headerIndex]
			}
			values = append(values, value)
		}
		tableItem.Values = values
		d.Tables = append(d.Tables, tableItem)
	}
	return nil
}

// HandleTimeSeries :
func HandleTimeSeries(c *gin.Context) {
	handleTsQueryInfosRequest(infos.TimeSeries, c)
}

// HandlePrint  打印路由信息
func HandlePrint(c *gin.Context) {
	res := influxdb.Print()
	c.String(200, res)
}

func HandlerHealth(c *gin.Context) {
	c.Status(200)
}

// HandleFeatureFlag  打印特性开关配置信息，refresh 不为空则强制刷新
func HandleFeatureFlag(c *gin.Context) {
	ctx := c.Request.Context()
	res := ""
	refresh := c.Query("r")

	if refresh != "" {
		res += "refresh feature flag\n"
		path := consul.GetFeatureFlagsPath()
		res += fmt.Sprintf("consul feature flags path: %s\n", path)
		data, err := consul.GetFeatureFlags()
		if err != nil {
			res += fmt.Sprintf("consul get feature flags error: %s\n", err.Error())
		}
		if data == nil {
			res += "consul get feature flags is empty\n"
		} else {
			err = featureFlag.ReloadFeatureFlags(data)
			if err != nil {
				res += fmt.Sprintf("reload feature flags err %s\n", err.Error())
			}
		}
		res += fmt.Sprintln("-------------------------------")
	}

	res += featureFlag.Print() + "\n"
	res += fmt.Sprintln("-----------------------------------")

	flagKey := c.Query("c")
	flagType := c.DefaultQuery("t", "string")

	key := c.Query("k")
	value := c.Query("v")

	if flagKey != "" {
		data := make(map[string]int, 0)
		for i := 0; i < 100; i++ {
			var k string

			ffUser := featureFlag.FFUser(fmt.Sprintf("%d", i), map[string]any{
				key: value,
			})

			if flagType == "bool" {
				boolCheck := featureFlag.BoolVariation(ctx, ffUser, flagKey, false)
				k = strconv.FormatBool(boolCheck)
			} else {
				k = featureFlag.StringVariation(ctx, ffUser, flagKey, "")
			}
			if _, ok := data[k]; !ok {
				data[k] = 0
			}
			data[k]++
		}

		res += fmt.Sprintf("check %s %s with %s => %s \n", flagType, flagKey, key, value)
		for k, v := range data {
			res += fmt.Sprintf("%s => %d \n", k, v)
		}
		res += fmt.Sprintln("-------------------------------")
	}

	c.String(200, res)
}

// HandleSpacePrint : 打印路由信息
func HandleSpacePrint(c *gin.Context) {
	ctx := c.Request.Context()
	typeKey := c.Query("type_key")
	refresh, _ := strconv.ParseBool(c.DefaultQuery("refresh", "false"))
	content, _ := strconv.ParseBool(c.DefaultQuery("content", "false"))

	router, err := influxdb.GetSpaceTsDbRouter()
	if err != nil {
		c.String(500, err.Error())
		return
	}
	res := ""
	if refresh {
		res += fmt.Sprintf("Refresh %s \n", typeKey)
		err = router.LoadRouter(ctx, typeKey, true)
		if err != nil {
			res += fmt.Sprintf("Error: %v\n", err)
		}
		res += fmt.Sprintln("--------------------------------")
	}
	res += router.Print(ctx, typeKey, content)
	c.String(200, res)
}

func HandleSpaceKeyPrint(c *gin.Context) {
	ctx := c.Request.Context()
	typeKey := c.Query("type_key")
	hashKey := c.Query("hash_key")
	toCached, _ := strconv.ParseBool(c.DefaultQuery("cached", "false"))
	refresh, _ := strconv.ParseBool(c.DefaultQuery("refresh", "false"))
	content, _ := strconv.ParseBool(c.DefaultQuery("content", "false"))

	router, err := influxdb.GetSpaceTsDbRouter()
	if err != nil {
		c.String(500, err.Error())
		return
	}
	res := ""
	if refresh {
		res += fmt.Sprintf("Refresh %s + %s\n", typeKey, hashKey)
		refreshMapping := map[string]string{
			routerInfluxdb.BkAppToSpaceKey:           routerInfluxdb.BkAppToSpaceChannelKey,
			routerInfluxdb.SpaceToResultTableKey:     routerInfluxdb.SpaceToResultTableChannelKey,
			routerInfluxdb.DataLabelToResultTableKey: routerInfluxdb.DataLabelToResultTableChannelKey,
			routerInfluxdb.ResultTableDetailKey:      routerInfluxdb.ResultTableDetailChannelKey,
		}
		err := router.ReloadByChannel(ctx, refreshMapping[typeKey], hashKey)
		if err != nil {
			res += fmt.Sprintf("Error: %v\n", err)
		}
		res += fmt.Sprintln("--------------------------------")
	}
	val := router.Get(ctx, typeKey, hashKey, toCached, false)
	if val != nil {
		res += fmt.Sprintf("Count: %v\n", val.Length())
		if content {
			res += fmt.Sprintf("Value: %s\n", val.Print())
		}
	} else {
		res += fmt.Sprintf("Value: nil")
	}
	c.String(200, res)
}

func HandleTsDBPrint(c *gin.Context) {
	ctx := c.Request.Context()
	spaceId := c.Query("space_id")
	tableId := structured.TableID(c.Query("table_id"))
	fieldName := c.Query("field_name")

	results := make([]string, 0)
	option := structured.TsDBOption{
		SpaceUid:  spaceId,
		TableID:   tableId,
		FieldName: fieldName,
		IsRegexp:  false,
	}

	tsDBs, err := structured.GetTsDBList(ctx, &option)
	results = append(results, fmt.Sprintf("GetTsDBList count: %d, result: %v, err: %v", len(tsDBs), tsDBs, err))

	router, err := influxdb.GetSpaceTsDbRouter()
	if err != nil {
		results = append(results, fmt.Sprintf("GetSpaceTsDbRouter err: %v", err))
	}
	space := router.GetSpace(ctx, spaceId)
	if space == nil {
		results = append(results, fmt.Sprintf("Space: %s, %v ", spaceId, space))
	} else {
		results = append(results, fmt.Sprintf("Space: %s, num: %v ", spaceId, len(space)))
	}
	rtIds := make([]string, 0)
	if len(tableId) == 0 {
		for rtId := range space {
			rt := router.GetResultTable(ctx, rtId, true)
			if rt != nil {
				for _, rtFieldName := range rt.Fields {
					if rtFieldName == fieldName {
						rtIds = append(rtIds, rt.TableId)
						break
					}
				}
			}
		}
		results = append(results, fmt.Sprintf("FieldToResulTable: %s, %v", fieldName, rtIds))
	} else {
		if !strings.Contains(string(tableId), ".") {
			rtIds = router.GetDataLabelRelatedRts(ctx, string(tableId))
			results = append(results, fmt.Sprintf("DataLabelToResulTable: %s, %v", tableId, rtIds))
		} else {
			rtIds = append(rtIds, string(tableId))
		}
	}
	for _, rtId := range rtIds {
		if space != nil {
			spaceRt, ok := space[rtId]
			results = append(results, fmt.Sprintf("SpaceResultTable: %s, %v", rtId, spaceRt))
			if ok {
				rt := router.GetResultTable(ctx, rtId, true)
				results = append(results, fmt.Sprintf("ResultTableDetail: %s, %+v", rtId, rt))
			}
		}
	}
	c.String(200, strings.Join(results, "\n\n"))
}

// HandleTsQueryInfosRequest 查询info数据接口
func handleTsQueryInfosRequest(infoType infos.InfoType, c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		err  error
		user = metadata.GetUser(ctx)
	)

	// 这里开始context就使用trace生成的了
	ctx, span := trace.NewSpan(ctx, "handle-ts-info")
	defer span.End(&err)

	// 获取body中的具体参数
	queryStmt, err := io.ReadAll(c.Request.Body)
	if err != nil {
		codedErr := errno.ErrDataDeserializeFailed().
			WithComponent("InfoHandler").
			WithOperation("读取请求体").
			WithContext("错误信息", err.Error()).
			WithSolution("检查请求体格式和大小限制")
		log.ErrorWithCodef(context.TODO(), codedErr)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	span.Set("info-request-header", c.Request.Header)
	span.Set("info-request-data", string(queryStmt))

	// 如果header中有bkbizid，则以header中的值为最优先
	spaceUid := user.SpaceUID

	span.Set("request-space-uid", spaceUid)

	params, err := infos.AnalysisQuery(string(queryStmt))
	if err != nil {
		codedErr := errno.ErrQueryParseInvalidSQL().
			WithComponent("InfoHandler").
			WithOperation("解析查询语句").
			WithContext("查询语句", string(queryStmt)).
			WithContext("错误信息", err.Error()).
			WithSolution("检查查询语句语法格式")
		log.ErrorWithCodef(context.TODO(), codedErr)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	result, err := infos.QueryAsync(ctx, infoType, params, spaceUid)
	if err != nil {
		codedErr := errno.ErrBusinessLogicError().
			WithComponent("InfoHandler").
			WithOperation("执行信息查询").
			WithContext("查询类型", infoType).
			WithContext("空间ID", spaceUid).
			WithContext("错误信息", err.Error()).
			WithSolution("检查查询参数和后端服务状态")
		log.ErrorWithCodef(context.TODO(), codedErr)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	// 根据info type，转化为不同的数据
	data, err := convertInfoData(ctx, infoType, params, result)
	if err != nil {
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}
	c.JSON(200, data)
}

// convertInfoData: 转化influxdb数据
func convertInfoData(
	ctx context.Context, infoType infos.InfoType, params *infos.Params, tables *influxdb.Tables,
) (any, error) {
	resp := NewInfoData(nil)
	if tables == nil {
		return resp, nil
	}
	err := resp.Fill(tables)
	if err != nil {
		codedErr := errno.ErrDataDeserializeFailed().
			WithComponent("InfoData").
			WithOperation("填充响应数据").
			WithContext("错误信息", err.Error()).
			WithSolution("检查表格数据结构完整性")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return nil, err
	}

	switch infoType {
	case infos.TimeSeries:
		return resp, nil
	case infos.Series:
		realResp := FormatSeriesData(resp, params.Keys)
		return realResp, nil
	case infos.TagValues:
		// tagvalues需要进行一次数据格式转换
		if len(resp.Tables) == 0 {
			dimensions := params.Keys
			result := &TagValuesData{
				Values: make(map[string][]string),
			}
			for _, dimension := range dimensions {
				result.Values[dimension] = []string{}
			}
			return result, nil
		}
		realResp := NewTagValuesData(resp)
		return realResp, nil
	case infos.TagKeys:
		// tagKeys需要进行提取values
		if len(resp.Tables) == 0 {
			return []any{}, nil
		}

		// 合并多table数据，并去重
		res := make(map[string]struct{}, 0)
		result := make([]string, 0)
		for _, table := range resp.Tables {
			for _, value := range table.Values {
				k, ok := value[0].(string)
				if !ok {
					continue
				}
				if _, ok = res[k]; !ok {
					res[k] = struct{}{}
					result = append(result, k)
				}
			}
		}

		return result, nil
	case infos.FieldKeys:
		if len(resp.Tables) == 0 {
			return []any{}, nil
		}

		res := make(map[string]struct{}, 0)
		result := make([]string, 0)
		for _, table := range resp.Tables {
			fieldIndex := 0
			for index, value := range table.Columns {
				if value == "fieldKey" {
					fieldIndex = index
					break
				}
			}
			for _, value := range table.Values {
				if len(value) <= fieldIndex+1 {
					codedErr := errno.ErrDataDeserializeFailed().
						WithComponent("InfoData").
						WithOperation("解析字段键数据长度").
						WithContext("原始数据", value).
						WithContext("期望索引", fieldIndex+1).
						WithSolution("检查数据长度是否足够")
					log.ErrorWithCodef(context.TODO(), codedErr)
					continue
				}
				v, ok := value[fieldIndex].(string)
				if !ok {
					codedErr := errno.ErrDataDeserializeFailed().
						WithComponent("InfoData").
						WithOperation("解析字段键数据类型").
						WithContext("原始数据", value).
						WithContext("字段索引", fieldIndex).
						WithSolution("检查字段类型是否为字符串")
					log.ErrorWithCodef(context.TODO(), codedErr)
					continue
				}
				if _, ok = res[v]; !ok {
					res[v] = struct{}{}
					result = append(result, v)
				}
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("unsupport infotype %v", infoType)
}
