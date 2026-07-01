// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

const StorageClusterRecordOverlap = time.Hour

type Filter map[string]string

// Record 表示 storage_cluster_records 中的一条时间分段路由。
// 当一个 RT 在不同时间段写入不同存储（例如 ES + Doris）时，UQ 会用这里的字段覆盖外层 result_table_detail。
type Record struct {
	// StorageID 是该时间段命中的存储集群 ID，用于定位底层存储实例。
	StorageID string `json:"storage_id,omitempty"`
	// StorageType 是该时间段应查询的存储类型；Doris 在查询侧统一表达为 bk_sql。
	StorageType string `json:"storage_type,omitempty"`
	// StorageName / ClusterName 表示存储集群名，Doris 查询 BKBase 时会透传为 properties.cluster_name。
	StorageName string `json:"storage_name,omitempty"`
	ClusterName string `json:"cluster_name,omitempty"`
	// DB / Measurement 是该时间段的物理查询目标。ES 使用 index_set + __default__，Doris 使用 bkbase_table_id + doris。
	DB          string `json:"db,omitempty"`
	Measurement string `json:"measurement,omitempty"`
	// SourceType 是 ES 查询入口选择依据；BKData ES 分段需要覆盖外层 RT 的 source_type。
	SourceType string `json:"source_type,omitempty"`
	// EnableTime 是该路由开始生效的 Unix 秒级时间戳。
	EnableTime int64 `json:"enable_time,omitempty"`
}

type StorageClusterRecords []Record

type StorageIDRange struct {
	StorageID     string
	StorageType   string
	StorageName   string
	ClusterName   string
	DB            string
	Measurement   string
	SourceType    string
	HasSourceType bool
	Start         time.Time
	End           time.Time
	QueryStart    time.Time
	QueryEnd      time.Time
}

func (r StorageIDRange) IsZero() bool {
	return r.Start.IsZero() || r.End.IsZero() || !r.Start.Before(r.End)
}

func (r StorageIDRange) QueryIsZero() bool {
	return r.QueryStart.IsZero() || r.QueryEnd.IsZero() || !r.QueryStart.Before(r.QueryEnd)
}

// TsDBV2 适配查询语句的结构体，以 TableID + MetricName 为条件，检索出 RT 基本信息和存储信息
type TsDBV2 struct {
	TableID         string              `json:"table_id"`
	Field           []string            `json:"field,omitempty"`
	FieldAlias      metadata.FieldAlias `json:"field_alias,omitempty"`
	MeasurementType string              `json:"measurement_type,omitempty"`
	Filters         []Filter            `json:"filters,omitempty"`
	SegmentedEnable bool                `json:"segmented_enable,omitempty"`
	DataLabel       string              `json:"data_label,omitempty"`
	// 将存储信息合并在 TsDB 中
	StorageID   string `json:"storage_id,omitempty"`
	StorageName string `json:"storage_name,omitempty"`

	// StorageClusterRecords
	StorageClusterRecords StorageClusterRecords `json:"storage_cluster_records,omitempty"`

	ClusterName   string   `json:"cluster_name,omitempty"`
	TagsKey       []string `json:"tags_key,omitempty"`
	DB            string   `json:"db,omitempty"`
	Measurement   string   `json:"measurement,omitempty"`
	VmRt          string   `json:"vm_rt,omitempty"`
	CmdbLevelVmRt string   `json:"cmdb_level_vm_rt,omitempty"`

	// 补充检索的元信息
	MetricName        string   `json:"metric_name,omitempty"`
	ExpandMetricNames []string `json:"expand_metric_names,omitempty"`
	// timeField
	TimeField metadata.TimeField `json:"time_field,omitempty"`
	// NeedAddTime
	NeedAddTime bool `json:"need_add_time"`

	// SourceType 数据来源
	SourceType  string `json:"source_type,omitempty"`
	StorageType string `json:"storage_type,omitempty"`
}

func (z *TsDBV2) IsSplit() bool {
	return z.MeasurementType == redis.BkSplitMeasurement
}

func (z *TsDBV2) String() string {
	return fmt.Sprintf("dataLabel:%v,tableID:%v,field:%s,measurementType:%s,segmentedEnable:%v,filter:%+v",
		z.DataLabel, z.TableID, z.Field, z.MeasurementType, z.SegmentedEnable, z.Filters,
	)
}

// validateStorageRouteRecord 校验 storage_cluster_records 记录，并转换为查询侧可消费的完整路由。
// 第二个返回值表示该记录是否满足路由完整性要求；跨存储记录缺关键字段时返回 false。
// 第三个返回值仅在校验失败时标记实际缺失字段，用于日志排障。
func (z *TsDBV2) validateStorageRouteRecord(record Record) (Record, bool, string) {
	storageType := record.StorageType
	if storageType == "" {
		storageType = z.StorageType
	}
	hasExplicitStorageType := record.StorageType != ""
	requiresBkSQLRouteFields := record.StorageType == metadata.BkSqlStorageType
	// 显式声明 storage_type 的新格式 route 必须自带目标物理表字段，不能从外层 RT 逐字段 fallback。
	// 切到 BKSQL 时还必须携带 cluster_name，用于 BKBase query_sync 的 properties.cluster_name。
	if hasExplicitStorageType && (record.DB == "" || record.Measurement == "" || (requiresBkSQLRouteFields && record.ClusterName == "")) {
		missingFields := make([]string, 0, 3)
		if record.DB == "" {
			missingFields = append(missingFields, "db")
		}
		if record.Measurement == "" {
			missingFields = append(missingFields, "measurement")
		}
		if requiresBkSQLRouteFields && record.ClusterName == "" {
			missingFields = append(missingFields, "cluster_name")
		}
		return Record{}, false, strings.Join(missingFields, ",")
	}

	// 旧 Redis 缓存未声明 storage_type 时只下发 storage_id/enable_time，继续从外层 RT detail 补齐查询目标。
	storageName := record.StorageName
	if storageName == "" {
		if requiresBkSQLRouteFields {
			// BKSQL 路由的 storage_name 旧字段和 cluster_name 都表示 BKBase 集群名；record 已校验 cluster_name 非空，可安全回填。
			storageName = record.ClusterName
		} else if !hasExplicitStorageType {
			storageName = z.StorageName
		}
	}
	clusterName := record.ClusterName
	if clusterName == "" && !hasExplicitStorageType {
		clusterName = z.ClusterName
	}
	db := record.DB
	if db == "" && !hasExplicitStorageType {
		db = z.DB
	}
	measurement := record.Measurement
	if measurement == "" && !hasExplicitStorageType {
		measurement = z.Measurement
	}
	sourceType := record.SourceType
	if !hasExplicitStorageType {
		sourceType = z.SourceType
	}
	return Record{
		StorageID:   record.StorageID,
		StorageType: storageType,
		StorageName: storageName,
		ClusterName: clusterName,
		DB:          db,
		Measurement: measurement,
		SourceType:  sourceType,
		EnableTime:  record.EnableTime,
	}, true, ""
}

// GetStorageIDRangesWithDirectionalOverlap 通过查询时间和前后方向的额外窗口获取存储 ID 以及该存储在本次查询中的有效时间段。
func (z *TsDBV2) GetStorageIDRangesWithDirectionalOverlap(start, end time.Time, backwardOverlap, forwardOverlap time.Duration) []StorageIDRange {
	// 没有迁移记录时只返回 storage_id，不填时间范围，避免普通单路由查询覆盖原有 step hints。
	if len(z.StorageClusterRecords) == 0 {
		return []StorageIDRange{
			{
				StorageID: z.StorageID,
			},
		}
	}

	records := append(StorageClusterRecords{}, z.StorageClusterRecords...)
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].EnableTime > records[j].EnableTime
	})

	ranges := make([]StorageIDRange, 0, len(records))
	// query overlap 用于决定需要查询哪些 storage：默认保留迁移前后 1h 重叠，
	// 当 PromQL range selector / offset 需要更大窗口时，按方向扩展，避免旧/新 storage 在 route 选择阶段被漏掉。
	backwardQueryOverlap := StorageClusterRecordOverlap
	if backwardOverlap > backwardQueryOverlap {
		backwardQueryOverlap = backwardOverlap
	}
	forwardQueryOverlap := StorageClusterRecordOverlap
	if forwardOverlap > forwardQueryOverlap {
		forwardQueryOverlap = forwardOverlap
	}
	checkStart := start.Add(-backwardQueryOverlap)
	checkEnd := end.Add(forwardQueryOverlap)

	// routeCheckStart 用于计算 route 生效权重范围：固定 1h 迁移重叠只负责多查数据，
	// 不参与权重；只有 PromQL 额外窗口真实覆盖到的 route 才需要参与后续 merge 权重。
	routeCheckStart := start
	if backwardOverlap > 0 {
		routeCheckStart = start.Add(-backwardOverlap)
	}
	routeCheckEnd := end
	if forwardOverlap > 0 {
		routeCheckEnd = end.Add(forwardOverlap)
	}
	// 遍历 storageClusterRecords 记录，按照开启时间倒序
	for i, record := range records {
		// 切换到 BKSQL 的分段路由必须携带目标存储的完整查询字段；缺字段时跳过该 record，避免 ES->Doris 场景混用外层 ES 配置。
		route, ok, missingFields := z.validateStorageRouteRecord(record)
		if !ok {
			log.Warnf(
				context.TODO(),
				"skip invalid storage_cluster_record route, table_id:%s, storage_id:%s, storage_type:%s, missing_fields:%s, db:%s, measurement:%s, cluster_name:%s",
				z.TableID, record.StorageID, record.StorageType, missingFields, record.DB, record.Measurement, record.ClusterName,
			)
			continue
		}
		recordStart := time.Unix(record.EnableTime, 0)
		recordEnd := checkEnd
		if i > 0 {
			// 倒序列表中，上一条记录是更新的 storage 生效点，也就是当前 storage 的结束时间。
			recordEnd = time.Unix(records[i-1].EnableTime, 0)
		}
		// checkStart/checkEnd 已经保留迁移前后 1h 重叠，以及 PromQL range/lookback 需要的额外回看窗口；
		// 这里不能再次扩展 route 边界，否则会把更远的相邻 storage 也选进来。
		queryStart := maxTime(checkStart, recordStart)
		queryEnd := minTime(checkEnd, recordEnd)
		if !queryStart.Before(queryEnd) {
			continue
		}

		// 权重范围只纳入用户查询窗口和 PromQL 额外回看窗口真实命中的 route 区间，避免固定 1h 迁移查询重叠影响权重。
		routeStart := maxTime(routeCheckStart, recordStart)
		routeEnd := minTime(routeCheckEnd, recordEnd)
		storageRange := StorageIDRange{
			StorageID:     route.StorageID,
			StorageType:   route.StorageType,
			StorageName:   route.StorageName,
			ClusterName:   route.ClusterName,
			DB:            route.DB,
			Measurement:   route.Measurement,
			SourceType:    route.SourceType,
			HasSourceType: record.StorageType == metadata.ElasticsearchStorageType,
			QueryStart:    queryStart,
			QueryEnd:      queryEnd,
		}
		if routeStart.Before(routeEnd) {
			storageRange.Start = routeStart
			storageRange.End = routeEnd
		}
		ranges = append(ranges, storageRange)
	}

	return ranges
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
