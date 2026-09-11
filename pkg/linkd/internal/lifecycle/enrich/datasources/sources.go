// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"linkd/internal/lifecycle/enrich"
)

const databaseMaxLifetime = 30 * time.Minute

// MySQLConfig 定义单个策略数据源的 MySQL 连接参数。
type MySQLConfig struct {
	Address  string
	Database string
	Username string
	Password string
}

// Config 定义 Enrich 范围内可复用的物理数据源连接。
type Config struct {
	MySQL *MySQLConfig
}

// Runtime 持有已完成连接检查的策略数据源及其连接池。
type Runtime struct {
	sources   enrich.Sources
	database  *sql.DB
	closeOnce sync.Once
	closeErr  error
}

// Open 创建一个 MySQL 连接池，并让各逻辑 Reader 共享该连接。
// Runtime 不迁移 schema，只检查连接并组装只读 Client。
func Open(ctx context.Context, config Config, maxConnections int) (_ *Runtime, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("open enrich datasources: context must not be nil")
	}
	if config.MySQL == nil {
		return nil, fmt.Errorf("open enrich datasources: mysql datasource is required")
	}
	if maxConnections < 1 {
		return nil, fmt.Errorf("open enrich datasources: max connections must be positive")
	}
	database, sqlDatabase, err := openMySQL(ctx, *config.MySQL, maxConnections)
	if err != nil {
		return nil, fmt.Errorf("open enrich mysql datasource: %w", err)
	}
	runtime := &Runtime{database: sqlDatabase}
	defer func() {
		if err != nil {
			err = errors.Join(err, runtime.Close())
		}
	}()
	runtime.sources, err = newMySQLSources(database)
	if err != nil {
		return nil, err
	}
	return runtime, nil
}

func newMySQLSources(database *gorm.DB) (enrich.Sources, error) {
	var sources enrich.Sources
	var err error
	if sources.CWStrategy, err = NewCWStrategyClient(CWStrategyClientConfig{DB: database}); err != nil {
		return enrich.Sources{}, fmt.Errorf("initialize cw strategy datasource: %w", err)
	}
	if sources.AlarmSource, err = NewAlarmSourceClient(AlarmSourceClientConfig{DB: database}); err != nil {
		return enrich.Sources{}, fmt.Errorf("initialize alarm source datasource: %w", err)
	}
	if sources.Metric, err = NewMetricClient(MetricClientConfig{DB: database}); err != nil {
		return enrich.Sources{}, fmt.Errorf("initialize metric datasource: %w", err)
	}
	return sources, nil
}

// Sources 返回已组装的真实策略数据源。
func (r *Runtime) Sources() enrich.Sources {
	if r == nil {
		return enrich.Sources{}
	}
	return r.sources
}

// Close 关闭所有数据源连接池；重复调用安全。
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.database != nil {
			if err := r.database.Close(); err != nil {
				r.closeErr = fmt.Errorf("close enrich mysql: %w", err)
			}
		}
	})
	return r.closeErr
}

func openMySQL(ctx context.Context, config MySQLConfig, maxConnections int) (*gorm.DB, *sql.DB, error) {
	if config.Address == "" || config.Database == "" || config.Username == "" {
		return nil, nil, fmt.Errorf("mysql address, database, and username are required")
	}
	dsn := driver.NewConfig()
	dsn.User = config.Username
	dsn.Passwd = config.Password
	dsn.Net = "tcp"
	dsn.Addr = config.Address
	dsn.DBName = config.Database
	dsn.ParseTime = true
	dsn.Loc = time.UTC

	sqlDatabase, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		return nil, nil, fmt.Errorf("open mysql: %w", err)
	}
	sqlDatabase.SetMaxOpenConns(max(maxConnections, 8))
	sqlDatabase.SetMaxIdleConns(max(maxConnections/2, 2))
	sqlDatabase.SetConnMaxLifetime(databaseMaxLifetime)
	if err := sqlDatabase.PingContext(ctx); err != nil {
		_ = sqlDatabase.Close()
		return nil, nil, fmt.Errorf("connect mysql: %w", err)
	}

	database, err := gorm.Open(
		mysql.New(mysql.Config{Conn: sqlDatabase}),
		&gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		_ = sqlDatabase.Close()
		return nil, nil, fmt.Errorf("initialize gorm: %w", err)
	}
	return database, sqlDatabase, nil
}
