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
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	tsdbService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	innerTsdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
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

// InfoData 返回结构化数据
type InfoData struct {
	dimensions map[string]bool
	Tables     []*TablesItem `json:"series"`
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
	res := ""
	refresh := c.Query("r")

	if refresh != "" {
		res += "refresh tsdb storage\n"
		// 使用接口多态，根据配置自动选择 Consul 或 Redis
		provider := tsdbService.GetStorageProvider()
		storageData, err := provider.GetTsDBStorageInfo(ctx)
		if err != nil {
			res += fmt.Sprintf("get tsdb storage info error: %s\n", err.Error())
		} else if storageData == nil {
			res += "get tsdb storage info is empty\n"
		} else {
			// 构建 Options 配置
			options := &innerTsdb.Options{
				InfluxDB: &innerTsdb.InfluxDBOption{
					Timeout:        tsdbService.InfluxDBTimeout,
					ContentType:    tsdbService.InfluxDBContentType,
					ChunkSize:      tsdbService.InfluxDBChunkSize,
					RawUriPath:     tsdbService.InfluxDBQueryRawUriPath,
					Accept:         tsdbService.InfluxDBQueryRawAccept,
					AcceptEncoding: tsdbService.InfluxDBQueryRawAcceptEncoding,
					MaxLimit:       tsdbService.InfluxDBMaxLimit,
					MaxSLimit:      tsdbService.InfluxDBMaxSLimit,
					Tolerance:      tsdbService.InfluxDBTolerance,
					RouterPrefix:   tsdbService.InfluxDBRouterPrefix,
					ReadRateLimit:  tsdbService.InfluxDBQueryReadRateLimit,
				},
				Es: &innerTsdb.ESOption{
					Timeout:    tsdbService.EsTimeout,
					MaxRouting: tsdbService.EsMaxRouting,
					MaxSize:    tsdbService.EsMaxSize,
				},
			}
			err = innerTsdb.ReloadTsDBStorage(ctx, storageData, options)
			if err != nil {
				res += fmt.Sprintf("reload tsdb storage err %s\n", err.Error())
			}
		}
		res += fmt.Sprintln("-------------------------------")
	}

	// 从内存中打印存储配置信息
	res += tsdb.Print() + "\n"
	res += fmt.Sprintln("-----------------------------------")

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

// HandleStorage 打印存储配置信息，refresh 不为空则强制刷新
func HandleStorage(c *gin.Context) {
	ctx := c.Request.Context()
	res := ""
	refresh := c.Query("r")
	source := c.DefaultQuery("source", "consul") // 可选参数，默认为consul,指定数据源: consul 或 redis

	// 使用接口多态，根据 source 参数自动选择 Consul 或 Redis
	provider := getStorageInfoProvider(source)
	storageName := provider.GetStorageName()

	var data map[string]any
	var err error
	var fromMemory bool

	if refresh != "" {
		res += "refresh storage info\n"
		path := provider.GetStoragePath()
		res += fmt.Sprintf("%s storage path: %s\n", storageName, path)
		data, err = provider.GetStorageInfo(ctx)
		if err != nil {
			res += fmt.Sprintf("%s get storage info error: %s\n", storageName, err.Error())
		} else if data == nil {
			res += fmt.Sprintf("%s get storage info is empty\n", storageName)
		} else {
			// 重新加载配置到内存
			// 使用 TSDB 的重新加载方法，因为 GetStorageInfo 返回的是通用的存储配置
			options := &innerTsdb.Options{
				InfluxDB: &innerTsdb.InfluxDBOption{
					Timeout:        tsdbService.InfluxDBTimeout,
					ContentType:    tsdbService.InfluxDBContentType,
					ChunkSize:      tsdbService.InfluxDBChunkSize,
					RawUriPath:     tsdbService.InfluxDBQueryRawUriPath,
					Accept:         tsdbService.InfluxDBQueryRawAccept,
					AcceptEncoding: tsdbService.InfluxDBQueryRawAcceptEncoding,
					MaxLimit:       tsdbService.InfluxDBMaxLimit,
					MaxSLimit:      tsdbService.InfluxDBMaxSLimit,
					Tolerance:      tsdbService.InfluxDBTolerance,
					RouterPrefix:   tsdbService.InfluxDBRouterPrefix,
					ReadRateLimit:  tsdbService.InfluxDBQueryReadRateLimit,
				},
				Es: &innerTsdb.ESOption{
					Timeout:    tsdbService.EsTimeout,
					MaxRouting: tsdbService.EsMaxRouting,
					MaxSize:    tsdbService.EsMaxSize,
				},
			}
			err = innerTsdb.ReloadTsDBStorage(ctx, data, options)
			if err != nil {
				res += fmt.Sprintf("reload storage err %s\n", err.Error())
			}
		}
		res += fmt.Sprintln("-------------------------------")
	} else {
		// 从内存中获取存储配置
		// 注意：当 Redis pub 发布配置变更时，tsdb.Service 的 loopReloadStorage 会自动监听并更新内存
		// 因此这里从内存读取的配置已经是最新的（无需手动 refresh）
		memoryStorage := innerTsdb.GetAllStorageFromMemory()
		// 转换为 map[string]any 格式，与 provider.GetStorageInfo(ctx) 的格式保持一致
		data = make(map[string]any, len(memoryStorage))
		for k, v := range memoryStorage {
			data[k] = v
		}
		fromMemory = true
	}

	// 打印存储配置信息
	if err != nil {
		res += fmt.Sprintf("get storage info from %s error: %s\n", storageName, err.Error())
	} else {
		sourceDesc := "memory"
		if !fromMemory {
			sourceDesc = storageName
		}
		res += fmt.Sprintf("storage info from %s (count: %d):\n", sourceDesc, len(data))
		for storageID, value := range data {
			var address, storageType, username string
			switch s := value.(type) {
			case *consul.Storage:
				address, storageType, username = s.Address, s.Type, s.Username
			case *redis.Storage:
				address, storageType, username = s.Address, s.Type, s.Username
			case *innerTsdb.Storage:
				// 从内存获取的 *tsdb.Storage
				address, storageType, username = s.Address, s.Type, s.Username
			default:
				res += fmt.Sprintf("  %s: unsupported storage type: %T\n", storageID, value)
				continue
			}
			res += fmt.Sprintf("  %s: address=%s, type=%s, username=%s\n",
				storageID, address, storageType, username) // 打印存储配置,只打印地址、类型、用户名
		}
	}
	res += fmt.Sprintln("-----------------------------------")

	c.String(200, res)
}
