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
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/influxdata/influxql"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// FakeConsulRouter
func FakeConsulRouter(t *testing.T) *gostub.Stubs {
	log.InitTestLogger()
	_ = consul.SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", nil,
	)
	consul.MetadataPath = "test/metadata/v1/default/data_id"
	consul.MetricRouterPath = "test/metadata/influxdb_metrics"

	res := map[string]api.KVPairs{
		// dataid metadata
		consul.MetadataPath: {
			{
				Key:   consul.MetadataPath + "/1500009",
				Value: []byte(`{"bk_data_id":1500009,"data_id":1500009,"mq_config":{"storage_config":{"topic":"0bkmonitor_15000090","partition":1},"cluster_config":{"domain_name":"kafka.service.consul","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","result_table_list":[{"bk_biz_id":2,"result_table":"process.port","shipper_list":[{"storage_config":{"real_table_name":"port","database":"process","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[{"field_name":"alive","type":"float","tag":"metric","default_value":"0","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_biz_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_collect_config_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_cloud_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_ip","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_service_category_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_topo_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_topo_level","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"listen_address","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"listen_port","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"pid","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"process_name","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"target","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"time","type":"timestamp","tag":"timestamp","default_value":"","is_config_by_user":true,"description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","unit":"","alias_name":"","option":{}}],"schema_type":"free","option":{}}],"option":{"inject_local_time":true,"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bk_bkmonitorv3_enterprise_production/metadata/influxdb_metrics/1500009/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"custom","token":"4774c8313d74430ca68c204aa6491eee","transfer_cluster_id":"default"}`),
			},
			{
				Key:   consul.MetadataPath + "/1500015",
				Value: []byte(`{"bk_biz_id":2,"bk_data_id":1500015,"data_id":1500015,"mq_config":{"storage_config":{"topic":"0bkmonitor_15000150","partition":1},"cluster_config":{"domain_name":"kafka.service.consul","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_event","result_table_list":[{"bk_biz_id":2,"result_table":"2_bkmonitor_event_public_1500015","shipper_list":[{"storage_config":{"index_datetime_format":"write_20060102","index_datetime_timezone":0,"date_format":"%Y%m%d","slice_size":500,"slice_gap":1440,"retention":30,"warm_phase_days":0,"warm_phase_settings":{},"base_index":"2_bkmonitor_event_public_1500015","index_settings":{"number_of_shards":4,"number_of_replicas":1},"mapping_settings":{"dynamic_templates":[{"discover_dimension":{"path_match":"dimensions.*","mapping":{"type":"keyword"}}}]}},"cluster_config":{"domain_name":"es7.service.consul","port":9200,"schema":null,"is_ssl_verify":false,"cluster_id":3,"cluster_name":"es7_cluster","version":"7.2","custom_option":"","registered_system":"_default","creator":"system","create_time":1624001652,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"elasticsearch","auth_info":{"password":"5gYTZqvd7Z7s","username":"elastic"}}],"field_list":[{"field_name":"dimensions","type":"object","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"object","es_dynamic":true}},{"field_name":"event","type":"object","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"object","es_properties":{"content":{"type":"text"},"count":{"type":"integer"}}}},{"field_name":"event_name","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword"}},{"field_name":"target","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword"}},{"field_name":"time","type":"timestamp","tag":"timestamp","default_value":"","is_config_by_user":true,"description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","unit":"","alias_name":"","option":{"es_type":"date_nanos","es_format":"epoch_millis"}}],"schema_type":"free","option":{"es_unique_field_list":["event","target","dimensions","event_name","time"]}}],"option":{"inject_local_time":true,"timestamp_precision":"ms","flat_batch_key":"data"},"type_label":"log","source_label":"bk_monitor","token":"d6dc05057e384f6db70e3542e3f8a2ce","transfer_cluster_id":"default"}`),
			},
			{
				Key:   consul.MetadataPath + "/1573194",
				Value: []byte(`{"bk_data_id":1573194,"data_id":1573194,"mq_config":{"storage_config":{"topic":"0bkmonitor_15731940","partition":7},"cluster_config":{"domain_name":"127.0.0.1","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","option":{"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bk_bkmonitorv3_enterprise_production/metadata/influxdb_metrics/1573194/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"bk_monitor","token":"9e180be8199946d3a8645639e236a50c","transfer_cluster_id":"default","data_name":"bcs_BCS-K8S-00000_k8s_metric","result_table_list":[{"bk_biz_id":2,"result_table":"2_bkmonitor_time_series_1573194.__default__","shipper_list":[{"storage_config":{"real_table_name":"__default__","database":"2_bkmonitor_time_series_1573194","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[],"schema_type":"free","option":{"is_split_measurement":true}}]}`),
			},
			{
				Key:   consul.MetadataPath + "/1573195",
				Value: []byte(`{"bk_data_id":1573195,"data_id":1573195,"mq_config":{"storage_config":{"topic":"0bkmonitor_15731950","partition":6},"cluster_config":{"domain_name":"127.0.0.1","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","option":{"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bk_bkmonitorv3_enterprise_production/metadata/influxdb_metrics/1573195/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"bk_monitor","token":"f383f2f00f0449e7ba849fc8b6e28bad","transfer_cluster_id":"default","data_name":"bcs_BCS-K8S-00000_custom_metric","result_table_list":[{"bk_biz_id":2,"result_table":"2_bkmonitor_time_series_1573195.__default__","shipper_list":[{"storage_config":{"real_table_name":"__default__","database":"2_bkmonitor_time_series_1573195","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[],"schema_type":"free","option":{"is_split_measurement":true}}]}`),
			},
		},
		// metric info
		consul.MetricRouterPath: {
			{
				Key:   consul.MetricRouterPath + "/1500009/time_series_metric",
				Value: []byte(`["alive"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/1573194/time_series_metric",
				Value: []byte(`["node_memory_Cached_bytes", "node_filesystem_files"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/1573195/time_series_metric",
				Value: []byte(`["node_memory_Cached_bytes", "node_filesystem_files"]`),
			},
		},
	}

	consul.GetDataWithPrefix = func(p string) (api.KVPairs, error) {
		return res[p], nil
	}
	consul.GetPathDataIDPath = func(metadataPath, version string) ([]string, error) {
		return []string{metadataPath}, nil
	}
	stubs := gostub.New()

	_ = consul.ReloadBCSInfo()
	reloadData, err := consul.ReloadRouterInfo()
	assert.Nil(t, err)
	ReloadTableInfos(reloadData)
	metricData, err := consul.ReloadMetricInfo()
	assert.Nil(t, err)
	ReloadMetricRouter(metricData)

	return stubs
}

// TestRouter_DBBiz
func TestRouter_DBBiz(t *testing.T) {
	stubs := FakeConsulRouter(t)
	defer stubs.Reset()

	expectsTsDBRouter := NewTsDBRouter()
	expectsTsDBRouter.AddTables(1500009, []*consul.TableID{
		{DB: "process", Measurement: "port", IsSplitMeasurement: false, ClusterID: "2"},
	})
	expectsTsDBRouter.AddTables(1573194, []*consul.TableID{
		{DB: "2_bkmonitor_time_series_1573194", Measurement: "", IsSplitMeasurement: true, ClusterID: "2"},
	})
	expectsTsDBRouter.AddTables(1573195, []*consul.TableID{
		{DB: "2_bkmonitor_time_series_1573195", Measurement: "", IsSplitMeasurement: true, ClusterID: "2"},
	})

	expectBizRouter := NewBizRouter()
	expectBizRouter.AddRouter(2, []consul.DataID{
		1500009, 1500015, 1573194, 1573195,
	}...)

	expectMetricMap := NewMetricRouter()
	expectMetricMap.AddRouter("alive", []consul.DataID{1500009}...)
	expectMetricMap.AddRouter("node_memory_Cached_bytes", []consul.DataID{1573194, 1573195}...)
	expectMetricMap.AddRouter("node_filesystem_files", []consul.DataID{1573194, 1573195}...)

	var dataIDs = []consul.DataID{1500009, 1500015, 1573194, 1573195}
	var bizID = 2
	var metrics = []string{"alive", "node_memory_Cached_bytes", "node_filesystem_files"}

	assert.Equal(t,
		expectsTsDBRouter.GetTableIDs(dataIDs...),
		tsDBRouter.GetTableIDs(dataIDs...),
	)
	assert.Equal(t,
		expectBizRouter.GetRouter(bizID),
		bizRouter.GetRouter(bizID),
	)

	assert.Equal(t,
		expectMetricMap.GetRouter(metrics...),
		metricRouter.GetRouter(metrics...),
	)
}

const (
	ReferenceName = "m"
	COUNT         = "count"
)

// ctxKeyPromQueryKeyName
type ctxKeyPromQueryKeyName struct {
}

// ctxKeyPromMetricMappingKey
type ctxKeyPromMetricMappingKey struct {
}

// QueryInfo
type QueryInfo struct {
	DB          string
	Measurement string
	DataIDList  []consul.DataID

	// 是否为行转列表
	IsPivotTable bool
}

// QueryInfoIntoContext
func QueryInfoIntoContext(ctx context.Context, metrics string, queryInfo *QueryInfo) context.Context {
	var (
		buffer map[string]*QueryInfo
		ok     bool
	)
	if buffer, ok = ctx.Value(&ctxKeyPromQueryKeyName{}).(map[string]*QueryInfo); !ok {
		buffer = make(map[string]*QueryInfo)
	}
	buffer[metrics] = queryInfo

	ctx = context.WithValue(ctx, &ctxKeyPromQueryKeyName{}, buffer)
	return ctx
}

// MetricMappingIntoContext
func MetricMappingIntoContext(ctx context.Context, referenceName string, metricName string) context.Context {
	var mapping map[string]string
	var ok bool
	if mapping, ok = ctx.Value(&ctxKeyPromMetricMappingKey{}).(map[string]string); !ok {
		mapping = make(map[string]string)
	}
	mapping[referenceName] = metricName
	return context.WithValue(ctx, &ctxKeyPromMetricMappingKey{}, mapping)
}

// MetricMappingFromContext
func MetricMappingFromContext(ctx context.Context, referenceName string) (string, error) {
	if mapping, ok := ctx.Value(&ctxKeyPromMetricMappingKey{}).(map[string]string); ok {
		if metricName, ok := mapping[referenceName]; ok {
			return metricName, nil
		}
		return "", errors.New("get metric mapping failed")
	}
	return "", errors.New("get metric mapping failed")
}

// where类型说明，决定了where语句的渲染格式
type ValueType int

const (
	StringType1 ValueType = 0
	NumType     ValueType = 1
	RegexpType  ValueType = 2
	TextType    ValueType = 3
)

// 操作符号映射
type Operator string

const (
	EqualOperator      string = "="
	NEqualOperator     string = "!="
	UpperOperator      string = ">"
	UpperEqualOperator string = ">="
	LowerOperator      string = "<"
	LowerEqualOperator string = "<="
	RegexpOperator     string = "=~"
	NRegexpOperator    string = "!~"
)

const (
	AndOperator string = "and"
	OrOperator  string = "or"
)

// WhereList
type WhereList struct {
	whereList   []*Where
	logicalList []string
}

// NewWhereList
func NewWhereList() *WhereList {
	return &WhereList{
		whereList:   make([]*Where, 0),
		logicalList: make([]string, 0),
	}
}

// Append
func (l *WhereList) Append(logicalOperator string, where *Where) {
	l.logicalList = append(l.logicalList, logicalOperator)
	l.whereList = append(l.whereList, where)
}

// String
func (l *WhereList) String() string {
	b := new(strings.Builder)
	for index, where := range l.whereList {
		if index == 0 {
			b.WriteString("where ")
		} else {
			b.WriteString(" " + l.logicalList[index-1] + " ")
		}
		b.WriteString(where.String())
	}
	return b.String()
}

// Check 判断条件里是包含tag的值，例如：tagName: bk_biz_id，tagValue：[1, 2]，bk_biz_id = 1 和 bk_biz_id = 2 都符合
func (l *WhereList) Check(tagName string, tagValue []string) bool {
	tagMap := make(map[string]interface{})
	for _, v := range tagValue {
		tagMap[v] = nil
	}
	for _, w := range l.whereList {
		if w.Name == tagName && w.ValueType == StringType1 && w.Operator == EqualOperator {
			if _, ok := tagMap[w.Value]; ok {
				return true
			}
		}
	}
	return false
}

// Where
type Where struct {
	Name      string
	Value     string
	Operator  string
	ValueType ValueType
}

// String
func (w *Where) String() string {
	switch w.ValueType {
	case NumType:
		return fmt.Sprintf("%s %s %s", w.Name, w.Operator, w.Value)
	case RegexpType:
		// influxdb 中以 "/" 为分隔符，所以这里将正则中的 "/" 做个简单的转义 "\/"
		return fmt.Sprintf("%s %s /%s/", w.Name, w.Operator, strings.ReplaceAll(w.Value, "/", "\\/"))
	case TextType:
		// 直接将长文本追加，作为一种特殊处理逻辑，influxQL 需要转义反斜杠
		return fmt.Sprintf("%s", strings.ReplaceAll(w.Value, "\\", "\\\\"))
	default:
		// 默认为字符串类型
		return fmt.Sprintf("%s %s '%s'", w.Name, w.Operator, w.Value)
	}
}

// 这里降低influxdb流量，主要不是根据时间减少点数，而是预先聚合减少series数量
func generateSQL(field, measurement, db, aggregation string, whereList *WhereList, sLimit, limit int, dimensions []string, window time.Duration) (string, bool, bool) {

	var (
		groupingStr    string
		isWithGroupBy  bool
		withTag        = ",*::tag"
		limitStr       string
		sLimitStr      string
		aggField       string
		rpName         string
		isCountGroup   bool
		newAggregation string
	)

	newAggregation = aggregation
	//rpName, field, newAggregation = GetRp(db, measurement, field, aggregation, window, whereList)

	// 根据RP重新生成measurement
	if rpName != "" {
		measurement = fmt.Sprintf("\"%s\".\"%s\"", rpName, measurement)
	} else {
		measurement = fmt.Sprintf("\"%s\"", measurement)
	}

	// 存在聚合条件，需要增加聚合
	if newAggregation != "" && window != 0 {
		var groupList []string
		isWithGroupBy = true
		isCountGroup = aggregation == COUNT

		if len(dimensions) > 0 {
			for _, d := range dimensions {
				groupList = append(groupList, fmt.Sprintf("\"%s\"", d))
			}
		}
		groupList = append(groupList, "time("+window.String()+")")
		groupingStr = " group by " + strings.Join(groupList, ",")
		withTag = ""
		aggField = fmt.Sprintf("%s(\"%s\")", newAggregation, field)
		// 由于此处存在聚合，所以可以直接使用influxdb的limit能力
		if sLimit > 0 {
			sLimitStr = fmt.Sprintf(" slimit %d", sLimit)
		}
	} else {
		aggField = fmt.Sprintf("\"%s\"", field)
	}

	if limit > 0 {
		limitStr = fmt.Sprintf(" limit %d", limit)
	}

	return fmt.Sprintf("select %s as %s,time as %s%s from %s %s%s%s%s",
		aggField, ResultColumnName, TimeColumnName, withTag, measurement, whereList.String(), groupingStr, limitStr, sLimitStr,
	), isWithGroupBy, isCountGroup
}

// do
func do(ctx context.Context) []string {
	metric, err := MetricMappingFromContext(ctx, ReferenceName)
	if err != nil {
		panic(err)
	}
	dataIDs := NewDataIDFilter(metric).FilterByBizIDs(2).FilterByProjectIDs().
		FilterByClusterIDs().Values()

	tableList := make([]*consul.TableID, 0)
	for _, dataID := range dataIDs {
		tableInfo := GetTableIDsByDataID(dataID)
		if len(tableInfo) == 0 {
			continue
		}
		tableList = append(tableList, tableInfo...)
	}

	var res []string
	where := NewWhereList()
	for _, t := range tableList {
		sql, _, _ := generateSQL("value", metric, t.DB, "max", where, 100, 100, nil, 5*time.Minute)
		res = append(res, sql)
	}
	return res
}

// TestGetTableIDByDBAndMeasurement
func TestGetTableIDByDBAndMeasurement(t *testing.T) {
	stubs := FakeConsulRouter(t)
	defer stubs.Reset()

	metricName := []string{"node_memory_Cached_bytes", "node_filesystem_files"}

	queryInfo := QueryInfo{
		DB:           "",
		Measurement:  "",
		DataIDList:   nil,
		IsPivotTable: false,
	}

	var wg sync.WaitGroup
	var wgCh sync.WaitGroup
	var num = 1000

	var checkSQL = map[string]string{
		"node_filesystem_files":    "select max(\"value\") as _value,time as _time from \"node_filesystem_files\"  group by time(5m0s) limit 100 slimit 100",
		"node_memory_Cached_bytes": "select max(\"value\") as _value,time as _time from \"node_memory_Cached_bytes\"  group by time(5m0s) limit 100 slimit 100",
	}
	var ch = map[string]chan []string{
		"node_filesystem_files":    make(chan []string, 0),
		"node_memory_Cached_bytes": make(chan []string, 0),
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		num = rand.Intn(50)
		for i := 0; i < num; i++ {
			go func() {
				time.Sleep(time.Duration(num) * time.Millisecond)
				data, err := consul.ReloadRouterInfo()
				assert.Nil(t, err)
				ReloadTableInfos(data)

				metricData, err := consul.ReloadMetricInfo()
				assert.Nil(t, err)
				ReloadMetricRouter(metricData)
			}()
		}
		fmt.Printf("fresh %d\n", num)
		time.Sleep(time.Second)
	}()

	for _, metric := range metricName {
		wgCh.Add(1)
		go func(metric string) {
			defer wgCh.Done()
			for c := range ch[metric] {
				for _, sql := range c {
					_, err := influxql.ParseQuery(sql)
					assert.Equal(t, sql, checkSQL[metric])
					assert.Nil(t, err)
					t.Log(sql)
				}
			}
		}(metric)

		ctx := context.Background()
		ctx = QueryInfoIntoContext(ctx, ReferenceName, &queryInfo)
		ctx = MetricMappingIntoContext(ctx, ReferenceName, metric)

		var ticker = time.NewTicker(5 * time.Second)
		go func(metric string) {
			defer ticker.Stop()
			for {
				for i := 0; i < num; i++ {
					wg.Add(1)
					go func(metric string) {
						defer wg.Done()
						ch[metric] <- do(ctx)
					}(metric)
				}
				select {
				case <-ticker.C:
					close(ch[metric])
					return
				}
			}
		}(metric)
	}
	wg.Wait()
	fmt.Println(ch)
	wgCh.Wait()
}
