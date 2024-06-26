package apiservice

import (
	"encoding/json"
	"fmt"
	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	gormbulk "github.com/t-tiger/gorm-bulk-insert"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 同步一个实例
func Test_SyncBkBaseResultTable(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		fmt.Println(err)
	}

	var bkBizId *int
	related := []string{"fields", "storages"}
	a := 2
	bkBizId = &a

	params := make(map[string]string)
	if bkBizId != nil {
		params["bk_biz_id"] = strconv.Itoa(*bkBizId)
	}

	// fields&related=storages&related=root不是合法的参数值，请检查后重试
	if len(related) > 0 {
		for k, rel := range related {
			if k == 0 {
				params["related"] = rel
				continue
			}
			params["related"] = params["related"] + fmt.Sprintf("&related=%s", rel)
		}
	}

	bkApiAppCodeSecret, _ := json.Marshal(map[string]string{"bk_app_code": cfg.BkApiAppCode, "bk_app_secret": cfg.BkApiAppSecret})
	var resp bkdata.CommonMapResp
	if _, err = bkdataApi.QueryResultTableResource().SetHeaders(map[string]string{"X-Bkapi-Authorization": string(bkApiAppCodeSecret)}).
		SetPathParams(map[string]string{"result_table_id": "2_http_clean"}).SetQueryParams(params).SetResult(&resp).Request(); err != nil {
		fmt.Println(err)
	}
	if err := resp.Err(); err != nil {
		fmt.Println(err)
	}

	tableIdList := make([]string, 0)
	resultTables := make([]interface{}, 0)
	resultTableFields := make([]interface{}, 0)
	mysqlStorages := make([]interface{}, 0)
	hdfsStorages := make([]interface{}, 0)
	tspiderStorages := make([]interface{}, 0)
	postgresqlStorages := make([]interface{}, 0)
	redisStorages := make([]interface{}, 0)
	oracleStorages := make([]interface{}, 0)
	dorisStorages := make([]interface{}, 0)
	tableInfo := resp.Data
	tableId := fmt.Sprintf("bkbase_%s.__default__", tableInfo["result_table_id"].(string))
	tableIdList = append(tableIdList, tableId)
	tableNameZh := tableInfo["result_table_name"].(string)
	operator := tableInfo["created_by"].(string)
	if bkBizId == nil {
		*bkBizId = tableInfo["bk_biz_id"].(int)
	}
	storages := []string{"mysql", "es", "hdfs"}
	storageList := make([]string, 0)
	for _, sto := range storages {
		switch sto {
		case models.StorageTypeMySQL:
			storageList = append(storageList, models.StorageTypeMySQL)
		case models.StorageTypeHdfs:
			storageList = append(storageList, models.StorageTypeHdfs)
		case models.StorageTypePostgresql:
			storageList = append(storageList, models.StorageTypePostgresql)
		case models.StorageTypeTspider:
			storageList = append(storageList, models.StorageTypeTspider)
		case models.StorageTypeRedis:
			storageList = append(storageList, models.StorageTypeRedis)
		case models.StorageTypeOracle:
			storageList = append(storageList, models.StorageTypeOracle)
		case models.StorageTypeDoris:
			storageList = append(storageList, models.StorageTypeDoris)
		}
	}
	storageStr := strings.Join(storageList, ",")
	defaultStorage := storages[0]
	fields := tableInfo["fields"].([]interface{})
	// 创建逻辑结果表内容
	rt := resulttable.ResultTable{
		TableId:            tableId,
		TableNameZh:        tableNameZh,
		IsCustomTable:      false,
		SchemaType:         models.ResultTableSchemaTypeFree,
		DefaultStorage:     defaultStorage,
		Creator:            operator,
		CreateTime:         time.Now(),
		LastModifyUser:     operator,
		LastModifyTime:     time.Now(),
		BkBizId:            *bkBizId,
		IsDeleted:          false,
		Label:              models.StorageTypeBkbase,
		IsEnable:           true,
		DataLabel:          nil,
		StorageClusterType: &storageStr,
	}
	resultTables = append(resultTables, rt)

	for _, data := range fields {
		field, ok := data.(map[string]interface{})
		if !ok {
			logger.Errorf("parse result table field data error, field_info: %v", field)
			continue
		}
		description, _ := field["description"].(string)
		unit, _ := field["unit"].(string)
		aliasName, _ := field["alias_name"].(string)
		tag, _ := field["tag"].(string)
		isConfigByUser, _ := field["is_config_by_user"].(bool)
		defaultValue, _ := field["default_value"].(*string)
		rtf := resulttable.ResultTableField{
			TableID:        tableId,
			FieldName:      field["field_name"].(string),
			FieldType:      field["field_type"].(string),
			Description:    description,
			Unit:           unit,
			Tag:            tag,
			IsConfigByUser: isConfigByUser,
			DefaultValue:   defaultValue,
			Creator:        field["created_by"].(string),
			CreateTime:     time.Now(),
			LastModifyUser: field["updated_by"].(string),
			LastModifyTime: time.Now(),
			AliasName:      aliasName,
			IsDisabled:     false,
		}
		resultTableFields = append(resultTableFields, rtf)
	}

	for _, ste := range storageList {
		switch ste {
		case models.StorageTypeMySQL:
			mysqlStorages = append(mysqlStorages, storage.MysqlStorage{TableID: tableId, StorageClusterID: models.MySQLStorageClusterId})
		case models.StorageTypeHdfs:
			hdfsStorages = append(hdfsStorages, storage.HdfsStorage{TableID: tableId, StorageClusterID: models.HdfsStorageClusterId})
		case models.StorageTypeTspider:
			tspiderStorages = append(tspiderStorages, storage.TsPiDerStorage{TableID: tableId, StorageClusterID: models.TspiderStorageClusterId})
		case models.StorageTypePostgresql:
			postgresqlStorages = append(postgresqlStorages, storage.PostgresqlStorage{TableID: tableId, StorageClusterID: models.PostgresqlStorageClusterId})
		case models.StorageTypeRedis:
			redisStorages = append(redisStorages, storage.RedisStorage{TableID: tableId, StorageClusterID: models.RedisStorageClusterId})
		case models.StorageTypeOracle:
			oracleStorages = append(oracleStorages, storage.OracleStorage{TableID: tableId, StorageClusterID: models.OracleStorageClusterId})
		case models.StorageTypeDoris:
			dorisStorages = append(dorisStorages, storage.DorisStorage{TableID: tableId, StorageClusterID: models.DorisStorageClusterId})
		}
	}

	db := mysql.GetDBSession().DB
	tx := db.Begin()

	if err := gormbulk.BulkInsert(tx, resultTables, len(resultTables)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, resultTableFields, len(resultTableFields)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, mysqlStorages, len(mysqlStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, tspiderStorages, len(tspiderStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, postgresqlStorages, len(postgresqlStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, redisStorages, len(redisStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, oracleStorages, len(oracleStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, dorisStorages, len(dorisStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, hdfsStorages, len(hdfsStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	tx.Commit()
}

// 同步多个实例
func Test_SyncBkBaseResultTables(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	s := &BkdataService{}
	a := 2
	b := 2
	c := 1
	_ = s.SyncBkBaseResultTables(&a, &b, &c)
}
