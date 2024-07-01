package apiservice

import (
	"encoding/json"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"testing"
)

// 同步一个实例
func Test_SyncBkBaseResultTable(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		fmt.Println(err)
	}

	// 查询条件
	params := make(map[string]string)
	params["bk_biz_id"] = "2"
	params["page"] = "2"
	params["page_size"] = "1"
	params["genereage_type"] = "user"
	params["related"] = "fields"
	params["is_query"] = "1"

	// 访问bkbase接口获取数据
	bkApiAppCodeSecret, _ := json.Marshal(map[string]string{"bk_app_code": config.BkApiAppCode, "bk_app_secret": config.BkApiAppSecret})
	var resp bkdata.CommonListResp
	if _, err = bkdataApi.QueryResultTables().SetHeaders(map[string]string{"X-Bkapi-Authorization": string(bkApiAppCodeSecret)}).
		SetQueryParams(params).SetResult(&resp).Request(); err != nil {
		fmt.Println(err)
	}
	if err := resp.Err(); err != nil {
		fmt.Println(err)
	}

	var (
		resultTables = make([]resulttable.ResultTable, 0)
		s            = &BkdataService{}
	)

	for _, data := range resp.Data {
		tableInfo, ok := data.(map[string]interface{})
		if !ok {
			logger.Errorf("parse result table data error, result_table_info: %v", tableInfo)
			continue
		}

		// 获取结果表内容
		tableId := fmt.Sprintf("bkbase_%s.__default__", tableInfo["result_table_id"].(string))
		rt := s.GetBkBaseResultTables(tableId, tableInfo)
		resultTables = append(resultTables, rt)
	}

	db := mysql.GetDBSession().DB
	tx := db.Begin()
	if err := tx.Create(&resultTables); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
}

// 同步多个实例
func Test_SyncBkBaseResultTables(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	s := &BkdataService{}
	a := 2
	b := 2
	c := 1
	_ = s.SyncBkBaseResultTables(bkdata.SyncBkBaseDataParams{
		BkBizID:  &a,
		Page:     &b,
		PageSize: &c,
	})
}
