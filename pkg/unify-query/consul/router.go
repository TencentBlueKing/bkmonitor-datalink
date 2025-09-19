// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errors"
)

// MetaFieldType :
type MetaFieldType string

const (
	// MetaFieldTypeNested :
	MetaFieldTypeNested MetaFieldType = "nested"
	// MetaFieldTypeObject :
	MetaFieldTypeObject MetaFieldType = "object"
	// MetaFieldTypeInt :
	MetaFieldTypeInt MetaFieldType = "int"
	// MetaFieldTypeUint :
	MetaFieldTypeUint MetaFieldType = "uint"
	// MetaFieldTypeFloat :
	MetaFieldTypeFloat MetaFieldType = "float"
	// MetaFieldTypeString :
	MetaFieldTypeString MetaFieldType = "string"
	// MetaFieldTypeBool :
	MetaFieldTypeBool MetaFieldType = "bool"
	// MetaFieldTypeTimestamp :
	MetaFieldTypeTimestamp MetaFieldType = "timestamp"
)

// MetaFieldTagType :
type MetaFieldTagType string

type DataIDs []DataID

// Len
func (d DataIDs) Len() int {
	return len(d)
}

// Less
func (d DataIDs) Less(i, j int) bool {
	return int64(d[i]) < int64(d[j])
}

// Swap
func (d DataIDs) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}

// DataID :
type DataID int

const (
	// MetaFieldTagMetric :
	MetaFieldTagMetric MetaFieldTagType = "metric"
	// MetaFieldTagDimension :
	MetaFieldTagDimension MetaFieldTagType = "dimension"
	// MetaFieldTagTime :
	MetaFieldTagTime MetaFieldTagType = "timestamp"
	// MetaFieldTagGroup :
	MetaFieldTagGroup MetaFieldTagType = "group"
)

// PipelineConfig
type PipelineConfig struct {
	Option          map[string]any           `mapstructure:"option" json:"option"`
	ETLConfig       string                   `mapstructure:"etl_config" json:"etl_config"`
	ResultTableList []*MetaResultTableConfig `mapstructure:"result_table_list" json:"result_table_list"`
	MQConfig        *MetaClusterInfo         `mapstructure:"mq_config" json:"mq_config"`
	DataID          DataID                   `mapstructure:"data_id" json:"data_id"`
}

// MetaResultTableConfigOption
type MetaResultTableConfigOption struct {
	IsSplitMeasurement bool `mapstructure:"is_split_measurement" json:"is_split_measurement"`
}

// MetaResultTableConfig :
type MetaResultTableConfig struct {
	BizID       int                         `mapstructure:"bk_biz_id" json:"bk_biz_id"`
	Option      MetaResultTableConfigOption `mapstructure:"option" json:"option"`
	SchemaType  ResultTableSchemaType       `mapstructure:"schema_type" json:"schema_type"`
	ShipperList []*MetaClusterInfo          `mapstructure:"shipper_list" json:"shipper_list"`
	ResultTable string                      `mapstructure:"result_table" json:"result_table"`
	FieldList   any                         `mapstructure:"field_list" json:"field_list"`
}

// ResultTableSchemaType :
type ResultTableSchemaType string

// MetaClusterInfo :
type MetaClusterInfo struct {
	ClusterConfig *ClusterConfig `mapstructure:"cluster_config" json:"cluster_config"`
	StorageConfig map[string]any `mapstructure:"storage_config" json:"storage_config"`
	AuthInfo      *Auth          `mapstructure:"auth_info" json:"auth_info"`
	ClusterType   string         `mapstructure:"cluster_type" json:"cluster_type"`
}

// MetaFieldConfig :
type MetaFieldConfig struct {
	Option         map[string]any   `mapstructure:"option" json:"option"`
	Type           MetaFieldType    `mapstructure:"type" json:"type"`
	IsConfigByUser bool             `mapstructure:"is_config_by_user" json:"is_config_by_user"`
	Tag            MetaFieldTagType `mapstructure:"tag" json:"tag"`
	FieldName      string           `mapstructure:"field_name" json:"field_name"`
	AliasName      string           `mapstructure:"alias_name" json:"alias_name"`
	DefaultValue   any              `mapstructure:"default_value" json:"default_value"`
}

// ClusterConfig :
type ClusterConfig struct {
	DomainName       string `mapstructure:"domain_name" json:"domain_name"`
	Port             int    `mapstructure:"port" json:"port"`
	Schema           string `mapstructure:"schema" json:"schema"`
	IsSslVerify      bool   `mapstructure:"is_ssl_verify" json:"is_ssl_verify"`
	ClusterID        int    `mapstructure:"cluster_id" json:"cluster_id"`
	ClusterName      string `mapstructure:"cluster_name" json:"cluster_name"`
	Version          string `mapstructure:"version" json:"version"`
	CustomOption     string `mapstructure:"custom_option" json:"custom_option"`
	RegisteredSystem string `mapstructure:"registered_system" json:"registered_system"`
	Creator          string `mapstructure:"creator" json:"creator"`
	CreateTime       int64  `mapstructure:"create_time" json:"create_time"`
	LastModifyUser   string `mapstructure:"last_modify_user" json:"last_modify_user"`
	IsDefaultCluster bool   `mapstructure:"is_default_cluster" json:"is_default_cluster"`
}

// Auth :
type Auth struct {
	Username string `mapstructure:"username" json:"username"`
	Password string `mapstructure:"password" json:"password"`
}

const (
	MetadataStorageDataBaseKey  = "database"
	MetadataStorageTableKey     = "real_table_name"
	MetadataInfluxdbClusterType = "influxdb"
	MetadataConsulDataIDKey     = "data_id"
)

// GetTSInfo: 从配置中获取influxdb所需要的db，measurement等信息
// db必定不为空，如果为空，则最好忽略此TableID
func (m *MetaResultTableConfig) GetTSInfo(dataID DataID, tableID *TableID) error {
	// 有值代表为分表

	for _, shipper := range m.ShipperList {
		if shipper.ClusterType != MetadataInfluxdbClusterType {
			continue
		}
		db, has := shipper.StorageConfig[MetadataStorageDataBaseKey]
		dbStr, ok := db.(string)
		if !has || !ok {
			log.Errorf(context.TODO(), "%s [%s] | 存储: InfluxDB | 操作: 获取数据库 | 数据ID: %d | 数据库: %v | 错误: 数据库不存在 | 解决: 检查DataID和数据库配置", errors.ErrDataProcessFailed, errors.GetErrorCode(errors.ErrDataProcessFailed), dataID, db)
			continue
		}
		tableID.ClusterID = fmt.Sprintf("%d", shipper.ClusterConfig.ClusterID)
		tableID.DB = dbStr

		// 如果分表，则不用理会 MetadataTableKey
		if m.Option.IsSplitMeasurement {
			tableID.IsSplitMeasurement = true
			tableID.Measurement = ""
		} else {
			measurement, has := shipper.StorageConfig[MetadataStorageTableKey]
			measurementStr, ok := measurement.(string)
			if !has || !ok {
				log.Errorf(context.TODO(), "%s [%s] | 存储: InfluxDB | 操作: 获取测量表 | 数据ID: %d | 测量表: %v | 错误: 测量表不存在 | 解决: 检查测量表配置和数据源", errors.ErrDataProcessFailed, errors.GetErrorCode(errors.ErrDataProcessFailed), dataID, measurement)
				continue
			}
			tableID.Measurement = measurementStr
		}
	}

	if tableID.DB == "" {
		return fmt.Errorf("empty db")
	}
	return nil
}

// TableID
type TableID struct {
	ClusterID          string
	DB                 string
	Measurement        string
	IsSplitMeasurement bool
}

// IsSplit
func (t *TableID) IsSplit() bool {
	return t.IsSplitMeasurement
}

// String
func (t *TableID) String() string {
	if t.IsSplitMeasurement {
		return t.DB
	}
	return t.DB + "." + t.Measurement
}

const (
	BizID     = "bk_biz_id"
	ProjectID = "project_id"
	ClusterID = "bsc_cluster_id"
)

// ReloadRouterInfo: 从consul获取router信息
// 这里的path和transfer watch的路径一致
func ReloadRouterInfo() (map[string][]*PipelineConfig, error) {
	// 获取metadata路径下的transfer全部实例，并遍历获取所有path路径下的dataID
	// 根据consul路径版本获取到所有transfer集群的data_id的路径
	paths, err := GetPathDataIDPath(MetadataPath, MetadataPathVersion)
	if err != nil {
		return nil, err
	}
	log.Debugf(context.TODO(), "get meatadata path :%v", paths)

	pipelineConfMap := make(map[string][]*PipelineConfig, len(paths))

	// 遍历所有集群路径，获取到所有的data_id元信息
	for _, path := range paths {
		kv, consulErr := GetDataWithPrefix(path)
		if consulErr != nil {
			return nil, consulErr
		}
		pipelineConfList, formatErr := FormatMetaData(kv)
		if formatErr != nil {
			return nil, formatErr
		}
		pipelineConfMap[path] = pipelineConfList
	}

	log.Debugf(context.TODO(), "get pipelineConfigMap: %#v", pipelineConfMap)

	return pipelineConfMap, nil
}

// FormatQueryRouter : 对pipelineConf序列化
func FormatMetaData(kvPairs api.KVPairs) ([]*PipelineConfig, error) {
	var (
		PipelineConfList []*PipelineConfig
		err              error
	)

	for _, kvPair := range kvPairs {
		var pipeConf *PipelineConfig
		err = json.Unmarshal(kvPair.Value, &pipeConf)
		if err != nil {
			log.Errorf(context.TODO(), "%s [%s] | 操作: 序列化管道配置 | 错误: %s | 解决: 检查配置格式和数据结构", errors.ErrDataProcessFailed, errors.GetErrorCode(errors.ErrDataProcessFailed), err)
			continue
		}
		PipelineConfList = append(PipelineConfList, pipeConf)
	}

	// 当err != nil，并且PipelineConfList为空，则返回错误, 否则跳过wrong format的pipeline
	if err != nil && len(PipelineConfList) == 0 {
		return nil, err
	}
	return PipelineConfList, nil
}

// GetPathDataIDPath: 根据version信息，获取不同的metadata元数据信息
// 这里路径版本与transfer对齐
var GetPathDataIDPath = func(metadataPath, version string) ([]string, error) {
	switch version {
	case "":
		return []string{metadataPath}, nil
	default:
		// 默认为v1，transfer/cmd/root.go:201
		// 这里保证末尾有一个 /
		metadataPath = strings.Join([]string{metadataPath, "v1", ""}, "/")
		clusterPaths, _, err := globalInstance.client.KV.Keys(metadataPath, "/", nil)
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(clusterPaths))

		for _, clusterPath := range clusterPaths {
			path := clusterPath + MetadataConsulDataIDKey
			paths = append(paths, path)
		}

		return paths, nil
	}
}

// WatchQueryRouter: 监听consul路径，拿到es和influxdb等对应的查询信息
// 由于metadata的data_id元信息数据量比较大，采用延迟更新
var WatchQueryRouter = func(ctx context.Context) (<-chan any, error) {
	// 多个查询服务都需要此监听开启，但只运行一次就可以
	// 延迟更新consul，启动一个循环，周期性的查看当前事件是否触发了要更新，以及是否有更新内容
	path := fmt.Sprintf("%s/%s/", MetadataPath, MetadataPathVersion)
	return DelayWatchPath(ctx, path, "/", WatchChangeOnce)
}

// DelayWatchPath
func DelayWatchPath(
	ctx context.Context, path, separator string, fn func(ctx context.Context, path, separator string,
	) (<-chan any, error),
) (<-chan any, error) {
	var (
		ticker     = time.NewTicker(checkUpdatePeriod)
		delayT     = delayUpdateTime
		ch, err    = fn(ctx, path, separator)
		needUpdate bool
		updateAt   = time.Now()
		wrapCh     = make(chan any)
		cache      = make([]any, 0)
	)
	if err != nil {
		return nil, err
	}

	go func() {
	loop:
		for {
			select {
			case <-ctx.Done():
				// over
				ticker.Stop()
				close(wrapCh)
				break loop
			case i := <-ch:
				// 有发生变化
				cache = append(cache, i)
				log.Debugf(context.TODO(), "path:[%s] changed, need update", path)
				needUpdate = true
			case <-ticker.C:
				// 延迟更新到点之后，并且监听consul的路径有发生变化
				if updateAt.Add(delayT).Before(time.Now()) && needUpdate {
					log.Debugf(context.TODO(), "router path:[%s] update", path)
					// 正常来说 此值是有意义的，但是unify-query为触发事件全量更新，所以这里传什么其实无所谓
					wrapCh <- cache
					updateAt = time.Now()
					needUpdate = false
					cache = cache[:0]
				}
			}
		}
	}()

	return wrapCh, nil
}

// === metric router ===

// WatchMetricRouter: 监听 influxdb_metrics 路径
var WatchMetricRouter = func(ctx context.Context) (<-chan any, error) {
	path := fmt.Sprintf("%s/", MetricRouterPath)
	return DelayWatchPath(ctx, path, "/", WatchChangeOnce)
}

// ReloadMetricInfo:
// return : {dataid:[metrics]}, eg: {1001: ["usage", "idle", "iowait"], 1002: ["free", "total"]}
func ReloadMetricInfo() (map[int][]string, error) {
	path := MetricRouterPath
	kv, err := GetDataWithPrefix(path)
	if err != nil {
		return nil, err
	}
	return getDataidMetrics(kv, path+"/")
}

// getDataidMetrics: get dataid-metrics pairs
// return : {dataid:[metrics]}, eg: {1001: ["usage", "idle", "iowait"], 1002: ["free", "total"]}
func getDataidMetrics(kvPairs api.KVPairs, prefix string) (map[int][]string, error) {
	result := make(map[int][]string)
	for _, kv := range kvPairs {
		// kv.Key的格式应该为prefix/{dataid}/time_series_metric
		key := strings.TrimPrefix(kv.Key, prefix)
		items := strings.Split(key, "/")
		if len(items) != 2 {
			continue
		}
		dataid, err := strconv.Atoi(items[0])
		if err != nil {
			log.Errorf(context.TODO(), "%s [%s] | 存储: Consul | 操作: 解析DataID数值 | DataID: %s | 错误: %v | 解决: 检查DataID格式是否为数字", errors.ErrDataProcessFailed, errors.GetErrorCode(errors.ErrDataProcessFailed), items[0], err)
			continue
		}
		metrics := make([]string, 0)
		if err := json.Unmarshal(kv.Value, &metrics); err != nil {
			log.Warnf(context.TODO(), "%s [%s] | 存储: Consul | 操作: 反序列化DataID指标 | 指标数据: %s | 错误: %v | 建议: 检查指标数据JSON格式", errors.ErrWarningDataIncomplete, errors.GetErrorCode(errors.ErrWarningDataIncomplete), kv.Value, err)
			continue
		}
		result[dataid] = metrics
	}

	return result, nil
}
