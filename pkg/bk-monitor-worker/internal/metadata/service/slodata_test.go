package service

import (
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSloData(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	//标签前缀
	prefix := "/slo/"
	//标签后缀
	suffixes := []string{"volume", "error", "latency", "availability"}

	//寻找符合标签规范的全部策略。然后统计其上层全部业务
	allBizIds, err := QueryBizV2(db, prefix, suffixes)
	assert.NoError(t, err)
	fmt.Println(allBizIds)

}
