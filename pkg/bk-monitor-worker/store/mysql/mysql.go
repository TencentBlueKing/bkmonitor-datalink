// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysql

import (
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/mysql"
)

//const (
//	mysqlHostPath    = "store.database.host"
//	mysqlPortPath    = "store.database.port"
//	mysqlUserPath    = "store.database.user"
//	mysqlPWDPath     = "store.database.password"
//	mysqlDBNamePath  = "store.database.db_name"
//	mysqlCharset     = "store.database.charset"
//	maxIdleConnsPath = "store.database.max_idle_conns"
//	maxOpenConnsPath = "store.database.max_open_conns"
//	debugModePath    = "store.database.debug_mode"
//)
//
//func init() {
//	viper.SetDefault(mysqlPortPath, 3306)
//	viper.SetDefault(mysqlUserPath, "root")
//	viper.SetDefault(maxIdleConnsPath, 10)
//	viper.SetDefault(maxOpenConnsPath, 100)
//	viper.SetDefault(debugModePath, false)
//}

// DBSession
type DBSession struct {
	DB *gorm.DB
}

var dbSession *DBSession

func GetDBSession() *DBSession {
	if dbSession != nil {
		return dbSession
	}
	dbSession = &DBSession{}
	err := dbSession.Open()
	if err != nil {
		logger.Errorf("connection mysql error, %v", err)
		panic(err)
	}
	return dbSession
}

// Open connect the mysql db
func (db *DBSession) Open() error {
	var err error

	dbhost := fmt.Sprintf("tcp(%s:%d)", config.StorageMysqlHost, config.StorageMysqlPort)
	db.DB, err = gorm.Open("mysql", fmt.Sprintf(
		"%s:%s@%s/%s?charset=%s&parseTime=True&loc=Local",
		config.StorageMysqlUser,
		config.StorageMysqlPassword,
		dbhost,
		config.StorageMysqlDbName,
		config.StorageMysqlCharset,
	))
	if err != nil {
		logger.Errorf("new a mysql connection error, %v", err)
		return err
	}
	sqldb := db.DB.DB()
	sqldb.SetMaxIdleConns(config.StorageMysqlMaxIdleConnections)
	sqldb.SetMaxOpenConns(config.StorageMysqlMaxOpenConnections)

	// 判断连通性
	if err := sqldb.Ping(); err != nil {
		return err
	}

	// 是否开启 debug 模式
	if config.StorageMysqlDebug {
		logger.Info("Debug mode of mysql is enabled. Check if it is in the test environment!")
		db.DB.LogMode(true)
	}

	return nil
}

// Close close connection
func (db *DBSession) Close() error {
	if db.DB != nil {
		return db.DB.Close()
	}
	return nil
}
