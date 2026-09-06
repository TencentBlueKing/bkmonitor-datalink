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

// Config 定义需要组装的真实策略数据源。
// nil 配置表示本进程的 Processor Chain 不需要该数据源。
type Config struct {
	BKStrategy  *MySQLConfig
	CWStrategy  *MySQLConfig
	AlarmSource *MySQLConfig
	Metric      *MySQLConfig
}

// Runtime 持有已完成连接检查的策略数据源及其连接池。
type Runtime struct {
	sources   enrich.Sources
	databases []*sql.DB
	closeOnce sync.Once
	closeErr  error
}

// Open 按需创建策略和告警源 Client，并为每个 Client 使用独立配置和连接池。
// Runtime 不会迁移 schema，只检查连接并组装只读 Client。
func Open(ctx context.Context, config Config, maxConnections int) (_ *Runtime, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("open enrich datasources: context must not be nil")
	}
	if config.BKStrategy == nil && config.CWStrategy == nil && config.AlarmSource == nil && config.Metric == nil {
		return nil, fmt.Errorf("open enrich datasources: at least one mysql datasource is required")
	}
	if maxConnections < 1 {
		return nil, fmt.Errorf("open enrich datasources: max connections must be positive")
	}
	runtime := &Runtime{databases: make([]*sql.DB, 0, 4)}
	defer func() {
		if err != nil {
			err = errors.Join(err, runtime.Close())
		}
	}()
	if err = runtime.openBKStrategy(ctx, config.BKStrategy, maxConnections); err != nil {
		return nil, err
	}
	if err = runtime.openCWStrategy(ctx, config.CWStrategy, maxConnections); err != nil {
		return nil, err
	}
	if err = runtime.openAlarmSource(ctx, config.AlarmSource, maxConnections); err != nil {
		return nil, err
	}
	if err = runtime.openMetric(ctx, config.Metric, maxConnections); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *Runtime) openBKStrategy(ctx context.Context, config *MySQLConfig, maxConnections int) error {
	if config == nil {
		return nil
	}
	database, sqlDatabase, err := openMySQL(ctx, *config, maxConnections)
	if err != nil {
		return fmt.Errorf("open bk strategy datasource: %w", err)
	}
	r.databases = append(r.databases, sqlDatabase)
	client, err := NewBKStrategyClient(BKStrategyClientConfig{DB: database})
	if err != nil {
		return fmt.Errorf("initialize bk strategy datasource: %w", err)
	}
	r.sources.BKStrategy = client
	return nil
}

func (r *Runtime) openCWStrategy(ctx context.Context, config *MySQLConfig, maxConnections int) error {
	if config == nil {
		return nil
	}
	database, sqlDatabase, err := openMySQL(ctx, *config, maxConnections)
	if err != nil {
		return fmt.Errorf("open cw strategy datasource: %w", err)
	}
	r.databases = append(r.databases, sqlDatabase)
	client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: database})
	if err != nil {
		return fmt.Errorf("initialize cw strategy datasource: %w", err)
	}
	r.sources.CWStrategy = client
	return nil
}

func (r *Runtime) openAlarmSource(ctx context.Context, config *MySQLConfig, maxConnections int) error {
	if config == nil {
		return nil
	}
	database, sqlDatabase, err := openMySQL(ctx, *config, maxConnections)
	if err != nil {
		return fmt.Errorf("open alarm source datasource: %w", err)
	}
	r.databases = append(r.databases, sqlDatabase)
	client, err := NewAlarmSourceClient(AlarmSourceClientConfig{DB: database})
	if err != nil {
		return fmt.Errorf("initialize alarm source datasource: %w", err)
	}
	r.sources.AlarmSource = client
	return nil
}

func (r *Runtime) openMetric(ctx context.Context, config *MySQLConfig, maxConnections int) error {
	if config == nil {
		return nil
	}
	database, sqlDatabase, err := openMySQL(ctx, *config, maxConnections)
	if err != nil {
		return fmt.Errorf("open metric datasource: %w", err)
	}
	r.databases = append(r.databases, sqlDatabase)
	client, err := NewMetricClient(MetricClientConfig{DB: database})
	if err != nil {
		return fmt.Errorf("initialize metric datasource: %w", err)
	}
	r.sources.Metric = client
	return nil
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
		for _, database := range r.databases {
			if err := database.Close(); err != nil {
				r.closeErr = errors.Join(r.closeErr, fmt.Errorf("close enrich mysql: %w", err))
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
