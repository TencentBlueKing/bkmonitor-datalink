// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package routefixture

// 本文件只负责测试环境与共享源表数据初始化；行为断言位于 route_refresh_test.go。
// 显式 DSN、连接级临时表；不读取 bmw_test.yaml，不修改生产刷新逻辑。
import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/alicebob/miniredis/v2"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/recordrule"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	redisutils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

type Fixture struct {
	DB     *gorm.DB
	Server *miniredis.Miniredis
	Redis  *redis.Instance
}

func New(t *testing.T) *Fixture {
	t.Helper()
	dsn := os.Getenv("BMW_ROUTE_FIXTURE_DSN")
	if dsn == "" {
		t.Skip("set BMW_ROUTE_FIXTURE_DSN to a dedicated MySQL test database; only temporary tables are created")
	}
	db, err := gorm.Open("mysql", dsn)
	require.NoError(t, err)
	db.DB().SetMaxOpenConns(1)
	db.DB().SetMaxIdleConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	tables := []interface{}{
		&space.Space{}, &space.SpaceDataSource{}, &space.SpaceResource{}, &space.BkAppSpaceRecord{},
		&resulttable.ResultTable{}, &resulttable.DataSource{}, &resulttable.DataSourceResultTable{}, &resulttable.ResultTableOption{}, &resulttable.DataSourceOption{},
		&storage.InfluxdbStorage{}, &storage.InfluxdbProxyStorage{}, &storage.AccessVMRecord{}, &storage.ClusterInfo{}, &storage.ESStorage{}, &storage.DorisStorage{}, &storage.ClusterRecord{},
		&bcs.BCSClusterInfo{}, &recordrule.RecordRule{}, &customreport.TimeSeriesGroup{}, &customreport.TimeSeriesMetric{}, &resulttable.ResultTableField{}, &recordrule.RecordRuleV4{}, &space.VMShortLinkRecord{},
	}
	// 真实 MySQL 临时表只在此连接可见，关闭连接自动销毁；不操作持久表。
	// 测试只读取源数据，不复制生产库索引/约束，列类型直接来自模型。
	for _, table := range tables {
		scope := db.NewScope(table)
		columns := []string{}
		for _, field := range scope.GetModelStruct().StructFields {
			if field.IsNormal {
				column := *field
				column.TagSettings = map[string]string{}
				for key, value := range field.TagSettings {
					if key != "DEFAULT" && key != "UNIQUE" && key != "PRIMARY_KEY" && key != "COMMENT" {
						column.TagSettings[key] = value
					}
				}
				column.IsPrimaryKey = false
				typ := db.Dialect().DataTypeOf(&column)
				if value, ok := field.TagSettings["DEFAULT"]; ok && !strings.Contains(strings.ToUpper(typ), "TEXT") && !strings.Contains(strings.ToUpper(typ), "JSON") && !strings.Contains(strings.ToUpper(typ), "BLOB") {
					switch value {
					case "true":
						typ += " DEFAULT 1"
					case "false":
						typ += " DEFAULT 0"
					default:
						typ += " DEFAULT '" + strings.ReplaceAll(value, "'", "''") + "'"
					}
				}
				typ = strings.ReplaceAll(typ, "AUTO_INCREMENT", "")
				columns = append(columns, "`"+field.DBName+"` "+typ)
			}
		}
		require.NoError(t, db.Exec("CREATE TEMPORARY TABLE `"+scope.TableName()+"` ("+strings.Join(columns, ",")+")").Error, "%T", table)
	}
	for _, ddl := range []string{
		`CREATE TEMPORARY TABLE metadata_esfieldqueryaliasoption (table_id VARCHAR(128), bk_tenant_id VARCHAR(256), field_path VARCHAR(255), query_alias VARCHAR(255), is_deleted BOOL)`,
		`CREATE TEMPORARY TABLE apm_metricdatasource (bk_data_id BIGINT, bk_biz_id INT, app_name VARCHAR(255), result_table_id VARCHAR(128), shared_datasource_id BIGINT NULL)`,
		`CREATE TEMPORARY TABLE apm_apmapplication (bk_tenant_id VARCHAR(256), bk_biz_id INT, app_name VARCHAR(255), is_deleted BOOL DEFAULT FALSE, is_enabled BOOL DEFAULT TRUE)`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	seedRouteCoreTables(t, db)
	seedRouteFeatureTables(t, db)
	seedUnrelatedRouteTables(t, db)

	server := miniredis.RunT(t)
	port, err := strconv.Atoi(server.Port())
	require.NoError(t, err)
	r := redis.GetInstance(&redisutils.Option{Host: server.Host(), Port: port})
	t.Cleanup(func() { require.NoError(t, r.Client.Close()) })
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(mysql.GetDBSession, func() *mysql.DBSession { return &mysql.DBSession{DB: db} })
	patches.ApplyFunc(redis.GetStorageRedisInstance, func() *redis.Instance { return r })
	patches.ApplyGlobalVar(&cfg.SpaceToResultTableKey, "route-test:spaces")
	patches.ApplyGlobalVar(&cfg.DataLabelToResultTableKey, "route-test:labels")
	patches.ApplyGlobalVar(&cfg.ResultTableDetailKey, "route-test:details")
	patches.ApplyGlobalVar(&cfg.BkAppToSpaceKey, "route-test:apps")
	patches.ApplyGlobalVar(&cfg.SpaceToResultTableChannel, "route-test:space-channel")
	patches.ApplyGlobalVar(&cfg.DataLabelToResultTableChannel, "route-test:label-channel")
	patches.ApplyGlobalVar(&cfg.ResultTableDetailChannel, "route-test:detail-channel")
	patches.ApplyGlobalVar(&cfg.BkAppToSpaceChannelKey, "route-test:app-channel")
	patches.ApplyGlobalVar(&cfg.GlobalTimeSeriesMetricExpiredSeconds, 3600)
	patches.ApplyGlobalVar(&cfg.GlobalIsRestrictDsBelongSpace, false)
	patches.ApplyGlobalVar(&cfg.BkciSpaceAccessPlugins, []string{})
	patches.ApplyGlobalVar(&cfg.GlobalAccessDbmRtSpaceUid, []string{})
	patches.ApplyGlobalVar(&cfg.EnableMultiTenantMode, false)
	patches.ApplyGlobalVar(&cfg.QueryDbBatchSize, 2)
	patches.ApplyGlobalVar(&cfg.QueryDbBatchDelay, time.Duration(0))
	// MySQL information_schema omits session-local temporary tables. The fixture has created both optional route tables.
	patches.ApplyMethodReturn(db, "HasTable", true)
	t.Cleanup(patches.Reset)
	return &Fixture{DB: db, Server: server, Redis: r}
}

func (f *Fixture) ResetOutput(t *testing.T) {
	t.Helper()
	f.Server.FlushAll()
	cache, err := memcache.NewRistretto()
	require.NoError(t, err)
	patch := gomonkey.ApplyFunc(memcache.GetMemCache, func() (*memcache.Ristretto, error) { return cache, nil })
	t.Cleanup(func() { patch.Reset(); cache.Close() })
}

func seedRouteCoreTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	put := func(row interface{}) {
		t.Helper()
		if field, ok := db.NewScope(row).FieldByName("bk_tenant_id"); ok && field.IsBlank {
			require.NoError(t, field.Set("system"))
		}
		require.NoError(t, db.Create(row).Error, "%T", row)
	}
	str := func(v string) *string { return &v }
	for _, sp := range []space.Space{
		{Id: 1, SpaceTypeId: "bkcc", SpaceId: "1"}, {Id: 2, SpaceTypeId: "bkcc", SpaceId: "2"}, {Id: 3, SpaceTypeId: "bkcc", SpaceId: "999"},
		{Id: 4, SpaceTypeId: "bkci", SpaceId: "project"}, {Id: 5, SpaceTypeId: "bksaas", SpaceId: "app"}, {Id: 6, SpaceTypeId: "bkci", SpaceId: "empty"},
	} {
		v := sp
		put(&v)
	}
	put(&storage.InfluxdbProxyStorage{ID: 1, ProxyClusterId: 10, InstanceClusterName: "proxy-a"})
	put(&storage.ClusterInfo{ClusterID: 20, ClusterType: models.StorageTypeVM, ClusterName: "vm-a"})
	put(&storage.ClusterInfo{ClusterID: 30, ClusterType: models.StorageTypeES, ClusterName: "es-a"})
	// 一个数据源对应多表、跨空间授权、平台数据，以及所有存储/标签状态。
	rows := []struct {
		id               string
		data             uint
		label            *string
		kind             string
		enabled, deleted bool
	}{
		{"cpu.used", 101, str("system"), models.StorageTypeInfluxdb, true, false},
		{"cpu.idle", 101, str("system"), models.StorageTypeInfluxdb, true, false},
		{"vm.metric", 102, str("vm"), models.StorageTypeVM, true, false},
		{"dual.metric", 103, str("dual"), models.StorageTypeVM, true, false},
		{"platform.metric", 104, str("platform"), models.StorageTypeInfluxdb, true, false},
		{"disabled.metric", 105, str("system"), models.StorageTypeInfluxdb, false, false},
		{"deleted.metric", 106, str("system"), models.StorageTypeInfluxdb, true, true},
		{"unbound.metric", 107, str("unbound"), models.StorageTypeInfluxdb, true, false},
		{"null.metric", 108, nil, models.StorageTypeInfluxdb, true, false},
		{"empty.metric", 109, str(""), models.StorageTypeInfluxdb, true, false},
		{"multi.metric", 110, str("alpha,beta"), models.StorageTypeInfluxdb, true, false},
		{"logs", 111, str("logs"), models.StorageTypeES, true, false},
		{"bad.logs.name", 112, str("bad"), models.StorageTypeES, true, false},
		{"cluster.metric", 201, str("cluster"), models.StorageTypeVM, true, false},
		{"shared.metric", 202, str("shared"), models.StorageTypeVM, true, false},
		{"emptyshared.metric", 203, str("emptyshared"), models.StorageTypeVM, true, false},
		{"orphan.metric", 113, str("orphan"), models.StorageTypeVM, true, false},
	}
	seen := map[uint]bool{}
	for _, row := range rows {
		put(&resulttable.ResultTable{TableId: row.id, DataLabel: row.label, DefaultStorage: row.kind, IsEnable: row.enabled, IsDeleted: row.deleted, SchemaType: models.ResultTableSchemaTypeFixed, BkBizId: 1})
		if !seen[row.data] {
			put(&resulttable.DataSource{BkDataId: row.data, DataName: row.id, EtlConfig: models.ETLConfigTypeBkStandard, IsEnable: true, SpaceTypeId: "bkcc", SpaceUid: "bkcc__1", IsPlatformDataId: row.data == 104})
			seen[row.data] = true
		}
		put(&resulttable.DataSourceResultTable{BkDataId: row.data, TableId: row.id})
		if row.id == "unbound.metric" {
			continue
		}
		switch row.kind {
		case models.StorageTypeInfluxdb:
			put(&storage.InfluxdbStorage{TableID: row.id, InfluxdbProxyStorageId: 1, Database: "metrics", RealTableName: row.id, PartitionTag: "host,zone"})
		case models.StorageTypeVM:
			cluster := uint(20)
			if row.id == "orphan.metric" {
				cluster = 999
			}
			put(&storage.AccessVMRecord{ResultTableId: row.id, VmClusterId: cluster, VmResultTableId: "vm_" + row.id})
		case models.StorageTypeES:
			put(&storage.ESStorage{TableID: row.id, StorageClusterID: 30, SourceType: "log", IndexSet: "logs-*"})
		}
	}
	put(&storage.InfluxdbStorage{TableID: "dual.metric", InfluxdbProxyStorageId: 1, Database: "old", RealTableName: "dual", PartitionTag: ""})
	for _, link := range []space.SpaceDataSource{
		{SpaceTypeId: "bkcc", SpaceId: "1", BkDataId: 101}, {SpaceTypeId: "bkcc", SpaceId: "1", BkDataId: 102},
		{SpaceTypeId: "bkcc", SpaceId: "2", BkDataId: 101, FromAuthorization: true},
		{SpaceTypeId: "bksaas", SpaceId: "app", BkDataId: 102},
	} {
		v := link
		put(&v)
	}
	for _, c := range []bcs.BCSClusterInfo{
		{ClusterID: "BCS-single", K8sMetricDataID: 201}, {ClusterID: "BCS-shared", K8sMetricDataID: 202}, {ClusterID: "BCS-empty", K8sMetricDataID: 203},
	} {
		v := c
		put(&v)
	}
	put(&space.SpaceResource{SpaceTypeId: "bkci", SpaceId: "project", ResourceType: "bkcc", ResourceId: str("1")})
	put(&space.SpaceResource{SpaceTypeId: "bkci", SpaceId: "project", ResourceType: "bcs", ResourceId: str("project"), DimensionValues: `[{"cluster_id":"BCS-single","cluster_type":"single","namespace":null},{"cluster_id":"BCS-shared","cluster_type":"shared","namespace":["ns-a","ns-b"]},{"cluster_id":"BCS-empty","cluster_type":"shared","namespace":[]}]`})
	put(&space.SpaceResource{SpaceTypeId: "bkci", SpaceId: "malformed", ResourceType: "bcs", ResourceId: str("malformed"), DimensionValues: `invalid json`})
	put(&space.SpaceResource{SpaceTypeId: "bksaas", SpaceId: "app", ResourceType: "bksaas", ResourceId: str("app"), DimensionValues: `[{"cluster_id":"BCS-shared","cluster_type":"shared","namespace":["saas-ns"]}]`})

	put(&resulttable.ResultTableOption{TableID: "null.metric", Name: models.OptionEnableFieldBlackList, OptionBase: models.OptionBase{Value: "false"}})
	require.NoError(t, db.Exec(`INSERT INTO metadata_resulttablefield(bk_tenant_id,table_id,field_name,tag) VALUES ('system','null.metric','whitelist_metric','metric')`).Error)
	put(&recordrule.RecordRule{SpaceType: "bkcc", SpaceId: "1", TableId: "rule.cpu"})
	put(&space.BkAppSpaceRecord{BkAppCode: "monitor", SpaceUID: "bkcc__1", IsEnable: true})
	put(&space.BkAppSpaceRecord{BkAppCode: "monitor", SpaceUID: "bkci__project", IsEnable: true})
	put(&resulttable.ResultTableOption{TableID: "logs", Name: "time_field", OptionBase: models.OptionBase{Value: `"timestamp"`}})
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := old.Add(time.Hour)
	put(&storage.ClusterRecord{TableID: "logs", ClusterID: 29, CreateTime: old, EnableTime: &old})
	put(&storage.ClusterRecord{TableID: "logs", ClusterID: 30, CreateTime: recent, EnableTime: &recent, IsCurrent: true})
	put(&storage.ClusterRecord{TableID: "logs", ClusterID: 28, CreateTime: old, IsDeleted: true})
	require.NoError(t, db.Exec(`INSERT INTO metadata_resulttablefield(bk_tenant_id,table_id,field_name,tag) VALUES ('system','cpu.used','usage','metric'),('system','cpu.used','host','dimension'),('system','vm.metric','requests','metric')`).Error)
	// 不同 measurement_type 共用同一组存储关联，初始化后不再修改。
	require.NoError(t, db.Model(&resulttable.ResultTable{}).Where("table_id IN (?)", []string{"vm.metric", "cluster.metric", "shared.metric"}).UpdateColumn("schema_type", models.ResultTableSchemaTypeFree).Error)
	require.NoError(t, db.Model(&resulttable.DataSource{}).Where("bk_data_id = ?", 102).UpdateColumn("etl_config", models.ETLConfigTypeBkStandardV2TimeSeries).Error)
	put(&resulttable.DataSourceOption{BkDataId: 102, Name: models.OptionDisableMetricCutter, OptionBase: models.OptionBase{Value: "true"}})
	put(&resulttable.ResultTableOption{TableID: "cluster.metric", Name: models.OptionIsSplitMeasurement, OptionBase: models.OptionBase{Value: "true"}})
	put(&space.BkAppSpaceRecord{BkAppCode: "disabled-app", SpaceUID: "bkcc__1", IsEnable: true})
	require.NoError(t, db.Model(&space.BkAppSpaceRecord{}).Where("bk_app_code = ?", "disabled-app").UpdateColumn("is_enable", false).Error)

	// custom time series 与普通表混合时，覆盖有效/过期指标和字段黑白名单。
	put(&customreport.TimeSeriesGroup{TimeSeriesGroupID: 1, CustomGroupBase: customreport.CustomGroupBase{TableID: "vm.metric"}})
	require.NoError(t, db.Exec(`INSERT INTO metadata_timeseriesmetric(group_id,table_id,field_name,last_modify_time) VALUES (1,'vm.metric','requests',?),(1,'vm.metric','expired',?)`, time.Now().UTC(), time.Now().UTC().Add(-24*time.Hour)).Error)
}

// 多租户、日志、预取与分页场景共用核心源表；所有写入仅发生在初始化阶段。
func seedRouteFeatureTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	exec := func(sql string, args ...any) { t.Helper(); require.NoError(t, db.Exec(sql, args...).Error) }
	// InfluxDB 类型 RT 现在通过 AccessVMRecord 查询；保留 unbound 证明无链路时跳过。
	for _, id := range []string{"cpu.used", "cpu.idle", "platform.metric", "disabled.metric", "deleted.metric", "null.metric", "empty.metric", "multi.metric"} {
		exec(`INSERT INTO metadata_accessvmrecord (bk_tenant_id,result_table_id,vm_cluster_id,vm_result_table_id) VALUES ('system',?,20,?)`, id, "vm_"+id)
	}
	exec(`INSERT INTO metadata_resulttableoption(bk_tenant_id,table_id,name,value,value_type) VALUES
 ('system','cpu.used','data_label_aliases','["legacy","system","legacy",""]','list'),
 ('system','cpu.idle','data_label_aliases','["legacy"]','list'),
 ('system','null.metric','data_label_aliases','["alias_only"]','list'),
 ('system','empty.metric','data_label_aliases','invalid-json','list'),
 ('system','multi.metric','data_label_aliases','["ignored"]','string')`)
	// 相同 RT 和标签属于不同租户，名称相同但存储/字段/标签内容不同。
	exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,schema_type,is_enable,is_deleted,data_label,labels,bk_biz_id) VALUES ('cpu.used','other','victoria_metrics','fixed',1,0,'system',NULL,2)`)
	exec(`INSERT INTO metadata_accessvmrecord(bk_tenant_id,result_table_id,vm_cluster_id,vm_result_table_id) VALUES ('other','cpu.used',120,'other_vm')`)
	exec(`INSERT INTO metadata_clusterinfo(cluster_id,bk_tenant_id,cluster_name,cluster_type) VALUES (120,'other','other-vm','victoria_metrics'),(29,'system','es-history','elasticsearch'),(40,'system','doris-a','doris')`)
	exec(`INSERT INTO metadata_space(id,space_type_id,space_id,bk_tenant_id) VALUES (20,'bkcc','20','other'),(21,'bkci','other-project','other')`)
	// 日志选型、虚拟表、跨存储历史与不完整配置。
	for _, r := range []struct{ id, kind, origin string }{
		{"doris.logs", "doris", ""}, {"virtual.logs", "doris", "logs"}, {"missing.origin", "doris", "missing"},
		{"mixed.logs", "elasticsearch", ""}, {"broken.logs", "doris", ""}, {"disabled.logs", "elasticsearch", ""},
		{"global.logs", "elasticsearch", ""},
	} {
		exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,schema_type,is_enable,is_deleted,data_label,labels,bk_biz_id) VALUES (?,'system',?,'fixed',?,0,'logs',NULL,1)`, r.id, r.kind, r.id != "disabled.logs")
		if r.kind == "doris" {
			cluster := 40
			dbname := "doris_" + r.id
			if r.origin != "" {
				dbname = ""
			}
			if r.id == "broken.logs" {
				cluster = 999
			}
			exec(`INSERT INTO metadata_dorisstorage(table_id,bk_tenant_id,storage_cluster_id,bkbase_table_id,origin_table_id) VALUES (?,'system',?,?,?)`, r.id, cluster, dbname, r.origin)
		} else {
			exec(`INSERT INTO metadata_esstorage(table_id,bk_tenant_id,storage_cluster_id,source_type,index_set,origin_table_id) VALUES (?,'system',30,'log','logs-*','')`, r.id)
		}
	}
	exec(`INSERT INTO metadata_dorisstorage(table_id,bk_tenant_id,storage_cluster_id,bkbase_table_id,origin_table_id) VALUES ('logs','system',40,'logs_doris',''),('mixed.logs','system',40,'mixed_doris','')`)
	exec(`INSERT INTO metadata_storageclusterrecord(id,table_id,bk_tenant_id,cluster_id,enable_time,is_deleted) VALUES (4,'logs','system',40,'2025-01-02',0),(5,'logs','system',999,'2025-01-03',0)`)
	exec(`INSERT INTO metadata_esfieldqueryaliasoption VALUES ('logs','system','raw.host','host',0),('logs','system','removed','gone',1),('logs','other','foreign','wrong',0)`)
	exec(`INSERT INTO metadata_resulttableoption(bk_tenant_id,table_id,name,value,value_type) VALUES ('system','global.logs','query_router_config','{"space_type":"all","filter_key":"owner","filter_value":"bk_biz_id"}','dict')`)
	// APM 全局表 + 应用归属，有效独占数据源与共享/歧义/跨租户候选。
	exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,schema_type,is_enable,is_deleted,data_label,labels,bk_biz_id,bk_biz_id_alias) VALUES ('apm_global.metrics','system','victoria_metrics','fixed',1,0,'apm',NULL,1,'biz')`)
	exec(`INSERT INTO apm_apmapplication VALUES ('system',1,'checkout',0,1),('other',2,'foreign',0,1)`)
	exec(`INSERT INTO apm_metricdatasource VALUES (101,1,'checkout','cpu.used',NULL),(102,1,'shared','vm.metric',100),(103,2,'foreign','dual.metric',NULL)`)
	// 可从数据库加载的预取数据：旧/新版预计算、本空间/全局短链、停用/删除短链。
	exec(`INSERT INTO metadata_recordrulev4 (id,bk_tenant_id,space_type,space_id,table_id,conditions,desired_status) VALUES (1,'system','bkcc','1','rule.v4','[]','enabled')`)
	for _, r := range []struct {
		id, config               string
		global, enabled, deleted bool
	}{
		{"short.local", "{}", false, true, false},
		{"short.global", `{"space_type":"all","filter_key":"owner","filter_value":"bk_biz_id"}`, true, true, false},
		{"short.project", `{"space_type":"bkci","filter_key":"project","filter_value":"space_id"}`, true, true, false},
		{"short.fallback", `{"space_type":"invalid"}`, true, true, false},
		{"short.disabled", "{}", false, false, false}, {"short.deleted", "{}", false, true, true},
	} {
		exec(`INSERT INTO metadata_vmshortlinkrecord(bk_tenant_id,space_type,space_id,table_id,is_global,query_router_config,is_enabled,is_deleted) VALUES ('system','bkcc','1',?,?,?,?,?)`, r.id, r.global, r.config, r.enabled, r.deleted)
	}
	// V3/V4 指标生命周期：V3 不保留过期 active 指标，V4 保留。
	exec(`UPDATE metadata_timeseriesgroup SET bk_data_id=102 WHERE table_id='vm.metric'`)
	exec(`UPDATE metadata_datasource SET created_from='bkdata' WHERE bk_data_id=202`)
	exec(`INSERT INTO metadata_timeseriesgroup (bk_tenant_id,table_id,time_series_group_id,bk_data_id) VALUES ('system','shared.metric',2,202)`)
	exec(`INSERT INTO metadata_timeseriesmetric(group_id,table_id,field_name,last_modify_time,is_active) VALUES
 (2,'shared.metric','active_old',?,1),(2,'shared.metric','inactive_old',?,0),(2,'shared.metric','fresh',?,0)`, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC().Add(-24*time.Hour), time.Now().UTC())
	// 残缺日志即使有VM绑定也不可回退；默认存储缺失时不可选择另一份存储。
	exec(`INSERT INTO metadata_accessvmrecord(bk_tenant_id,result_table_id,vm_cluster_id,vm_result_table_id) VALUES ('system','broken.logs',20,'must_not_fallback')`)
	exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,is_enable,is_deleted,data_label) VALUES ('missing.selected','system','doris',1,0,'logs')`)
	exec(`INSERT INTO metadata_esstorage(table_id,bk_tenant_id,storage_cluster_id,source_type,index_set) VALUES ('missing.selected','system',30,'log','not-selected-*')`)
	// 相同标签的外租户 alias、孤儿 option 和空配置，均不能污染当前租户。
	exec(`INSERT INTO metadata_resulttableoption(bk_tenant_id,table_id,name,value,value_type) VALUES
 ('other','cpu.used','data_label_aliases','["foreign_alias"]','list'),
 ('system','missing','data_label_aliases','["orphan_alias"]','list'),
 ('system','disabled.logs','query_router_config','{}','dict'),
 ('system','broken.logs','query_router_config','invalid-json','dict'),
 ('system','doris.logs','query_router_config','{"space_type":"bkci","filter_key":"project","filter_value":"space_id"}','dict')`)
	// 预计算详情覆盖普通指标的同名输出。
	exec(`UPDATE metadata_recordrule SET vm_cluster_id=20,dst_vm_table_id='rule_vm',rule_metrics='{"expression":"rule_metric"}' WHERE table_id='rule.cpu'`)
	// 同租户数据源存在两个不同应用候选时，不得猜测归属。
	exec(`INSERT INTO apm_apmapplication VALUES ('system',1,'ambiguous-a',0,1),('system',1,'ambiguous-b',0,1)`)
	exec(`INSERT INTO apm_metricdatasource VALUES (104,1,'ambiguous-a','platform.metric',NULL),(104,1,'ambiguous-b','platform.metric',NULL)`)

	exec(`UPDATE metadata_resulttable SET labels='{"team":"core","apm_application_name":"stale"}' WHERE bk_tenant_id='system' AND table_id='cpu.used'`)
	exec(`UPDATE metadata_resulttable SET labels='{"source":"origin"}' WHERE bk_tenant_id='system' AND table_id='logs'`)
	exec(`UPDATE metadata_resulttable SET data_label='',labels='{"scope":"view"}' WHERE bk_tenant_id='system' AND table_id='virtual.logs'`)
	// 500 表分页边界：只占用独立 batch 租户，避免污染其他功能断言。
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("batch.%03d", i)
		exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,is_enable,is_deleted,data_label) VALUES (?,'batch','elasticsearch',1,0,'batch')`, id)
		exec(`INSERT INTO metadata_esstorage(table_id,bk_tenant_id,storage_cluster_id,source_type,index_set,origin_table_id) VALUES (?,'batch',230,'log','batch-*','')`, id)
	}
	exec(`INSERT INTO metadata_clusterinfo(cluster_id,bk_tenant_id,cluster_name,cluster_type) VALUES (230,'batch','batch-es','elasticsearch')`)
}

// Large unrelated populations make query-scope regressions observable without timing assertions.
func seedUnrelatedRouteTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for i := 0; i < 100; i++ {
		id := 10000 + i
		require.NoError(t, db.Exec(`INSERT INTO metadata_clusterinfo(cluster_id,bk_tenant_id,cluster_name,cluster_type) VALUES (?,'system',?,'victoria_metrics')`, id, fmt.Sprintf("unrelated-%d", i)).Error)
		require.NoError(t, db.Exec(`INSERT INTO metadata_recordrule(bk_tenant_id,space_type,space_id,table_id,vm_cluster_id,dst_vm_table_id,rule_metrics) VALUES ('system','bkcc','999',?,?,?,'{}')`, fmt.Sprintf("unrelated.rule%d", i), id, fmt.Sprintf("vm_unrelated_%d", i)).Error)
		require.NoError(t, db.Exec(`INSERT INTO metadata_recordrulev4(bk_tenant_id,space_type,space_id,table_id,conditions,deleted_at) VALUES ('system','bkcc','999',?,'[]',NULL)`, fmt.Sprintf("unrelated.v4_%d", i)).Error)
		require.NoError(t, db.Exec(`INSERT INTO metadata_vmshortlinkrecord(bk_tenant_id,space_type,space_id,table_id,is_global,query_router_config,is_enabled,is_deleted) VALUES ('system','bkcc','999',?,0,'{}',1,0)`, fmt.Sprintf("unrelated.short%d", i)).Error)
		require.NoError(t, db.Exec(`INSERT INTO metadata_bcsclusterinfo(cluster_id,bk_tenant_id,K8sMetricDataID,CustomMetricDataID) VALUES (?,'system',?,?)`, fmt.Sprintf("BCS-unrelated-%d", i), uint(id), uint(id+10000)).Error)
	}
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("unrelated.rt%d", i)
		require.NoError(t, db.Exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,is_enable,is_deleted,data_label) VALUES (?,'system','victoria_metrics',1,0,?)`, id, fmt.Sprintf("unrelated_label_%d", i)).Error)
		require.NoError(t, db.Exec(`INSERT INTO metadata_resulttableoption(bk_tenant_id,table_id,name,value_type,value) VALUES ('system',?,'data_label_aliases','list',?)`, id, fmt.Sprintf(`["unrelated_alias_%d"]`, i)).Error)
	}

	require.NoError(t, db.Exec(`INSERT INTO metadata_recordrule(bk_tenant_id,space_type,space_id,table_id,vm_cluster_id,dst_vm_table_id,rule_metrics) VALUES
 ('system','bkcc','1','dual.metric',20,'rule_override','{"expression":"rule_metric"}'),
 ('system','bkcc','1','broken.logs',20,'forbidden_log_override','{}'),
 ('other','bkcc','20','rule.cpu',120,'other_rule','{}')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_accessvmrecord(bk_tenant_id,result_table_id,vm_cluster_id,vm_result_table_id) VALUES ('system','orphan.second',999,'orphan_second')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_space(id,space_type_id,space_id,bk_tenant_id) VALUES (22,'bkci','migration','system')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,schema_type,is_enable,is_deleted,bk_biz_id) VALUES ('migration.metric','system','victoria_metrics','fixed',1,0,-22)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_datasource(bk_data_id,bk_tenant_id,data_name,space_type_id,space_uid,is_platform_data_id,is_enable) VALUES (9000,'system','migration-normal','bkci','bkci__migration',0,1),(10000,'system','migration-cluster','bkci','bkci__migration',0,1)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_datasourceresulttable(bk_tenant_id,bk_data_id,table_id) VALUES ('system',9000,'migration.metric'),('system',10000,'migration.metric')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_spacedatasource(space_type_id,space_id,bk_data_id,from_authorization) VALUES ('bkci','migration',9000,0),('bkci','migration',10000,0)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_accessvmrecord(bk_tenant_id,result_table_id,vm_cluster_id,vm_result_table_id) VALUES ('system','migration.metric',20,'migration_vm')`).Error)

	require.NoError(t, db.Exec(`INSERT INTO metadata_spacedatasource(space_type_id,space_id,bk_data_id,from_authorization) VALUES ('bkci','project',201,0),('bkci','project',102,0),('bksaas','app',202,0)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,schema_type,is_enable,is_deleted,bk_biz_id,data_label) VALUES ('system.cpu','system','victoria_metrics','fixed',1,0,1,'host')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_recordrulev4(bk_tenant_id,space_type,space_id,table_id,conditions,deleted_at) VALUES
 ('system','bkcc','1','rule.v4_recent','[]',?),('system','bkcc','1','rule.v4_expired','[]',?)`, time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 0, -200)).Error)
}

// QueryLog captures executed SQL and returned row counts using GORM's test logger.
type Query struct {
	SQL  string
	Rows int64
}

type QueryLog struct{ Entries []Query }

func (q *QueryLog) Print(values ...interface{}) {
	if len(values) >= 6 && values[0] == "sql" {
		q.Entries = append(q.Entries, Query{SQL: values[3].(string), Rows: values[5].(int64)})
	}
}

func (q *QueryLog) ForTable(table string) []Query {
	result := []Query{}
	for _, entry := range q.Entries {
		if strings.Contains(entry.SQL, "`"+table+"`") || strings.Contains(entry.SQL, "FROM "+table+" ") {
			result = append(result, entry)
		}
	}
	return result
}

func (f *Fixture) ObserveQueries(t *testing.T) *QueryLog {
	t.Helper()
	queries := &QueryLog{}
	f.DB.SetLogger(queries)
	f.DB.LogMode(true)
	t.Cleanup(func() { f.DB.LogMode(false) })
	return queries
}
