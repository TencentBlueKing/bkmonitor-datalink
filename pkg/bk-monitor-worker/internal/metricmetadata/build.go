// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package metricmetadata

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type runInstance struct {
	dataID   uint
	db       *sql.DB
	window   *window.Processor
	consumer *notifier.Consumer
	tasks    []*scheduledTask
}

// scheduledTask runs one maintenance callback at a fixed interval until its context is canceled.
type scheduledTask struct {
	name     string
	dataID   string
	interval time.Duration
	run      func(context.Context) error
}

func newScheduledTask(name, dataID string, interval time.Duration, run func(context.Context) error) *scheduledTask {
	return &scheduledTask{name: name, dataID: dataID, interval: interval, run: run}
}

// Run waits one interval before its first callback and logs failures before retrying next tick.
func (t *scheduledTask) Run(ctx context.Context) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := t.run(ctx); err != nil && ctx.Err() == nil {
				logger.Warnf("metric metadata scheduled task failed task=%s dataid=%s: %v", t.name, t.dataID, err)
			}
		case <-ctx.Done():
			return
		}
	}
}

// Run owns all long-lived workers and joins scheduled tasks before the caller
// closes the shared DB pool.
func (i *runInstance) Run(ctx context.Context) error {
	taskCtx, cancel := context.WithCancel(ctx)
	var tasks sync.WaitGroup
	for _, scheduled := range i.tasks {
		tasks.Add(1)
		go func(scheduled *scheduledTask) {
			defer tasks.Done()
			scheduled.Run(taskCtx)
		}(scheduled)
	}
	err := i.consumer.Run(taskCtx)
	cancel()
	// Wait for in-flight DB tasks before Run closes the connection pool.
	tasks.Wait()
	return err
}

func optionsFromConfig() core.Options {
	o := core.DefaultOptions()
	getInt := func(key string, value int) int {
		return config.GetValue("taskConfig.metricmetadata."+key, value, viper.GetInt)
	}
	getDuration := func(key string, value time.Duration) time.Duration {
		return config.GetValue("taskConfig.metricmetadata."+key, value, viper.GetDuration)
	}
	o.BatchRecords = getInt("batch.size", o.BatchRecords)
	o.BatchBytes = getInt("batch.maxBytes", o.BatchBytes)
	o.BatchWait = getDuration("batch.wait", o.BatchWait)
	o.QueueMessages = getInt("queue.maxMessages", o.QueueMessages)
	o.QueueBytes = int64(getInt("queue.maxBytes", int(o.QueueBytes)))
	o.LoadPageSize = getInt("state.pageSize", o.LoadPageSize)
	o.MaxScopes = getInt("capacity.maxScopes", o.MaxScopes)
	o.MaxValues = getInt("capacity.maxValues", o.MaxValues)
	o.DBTimeout = getDuration("database.timeout", o.DBTimeout)
	o.RetryInterval = getDuration("retryInterval", o.RetryInterval)
	o.DrainTimeout = getDuration("drainTimeout", o.DrainTimeout)
	o.MaintenanceInterval = getDuration("refreshInterval", o.MaintenanceInterval)
	o.TouchInterval = getDuration("touchInterval", o.TouchInterval)
	if config.GlobalTimeSeriesMetricExpiredSeconds > 0 {
		o.ExpireAfter = time.Duration(config.GlobalTimeSeriesMetricExpiredSeconds) * time.Second
	}
	return o
}

// newRunInstance validates configuration and assembles the DB store, window,
// Kafka consumer, and scheduled maintenance tasks.
func newRunInstance(ctx context.Context, dataID uint) (*runInstance, error) {
	o := optionsFromConfig()
	if err := o.Validate(); err != nil {
		return nil, err
	}
	dsn := mysqlDriver.NewConfig()
	dsn.User = config.StorageMysqlUser
	dsn.Passwd = config.StorageMysqlPassword
	dsn.Net = "tcp"
	dsn.Addr = net.JoinHostPort(config.StorageMysqlHost, strconv.Itoa(config.StorageMysqlPort))
	dsn.DBName = config.StorageMysqlDbName
	dsn.ParseTime = true
	dsn.Loc = time.Local
	dsn.Timeout = o.DBTimeout
	dsn.ReadTimeout = o.DBTimeout
	dsn.WriteTimeout = o.DBTimeout
	dsn.Params = map[string]string{"charset": config.StorageMysqlCharset}
	db, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			if closeErr := db.Close(); closeErr != nil {
				logger.Warnf("metric metadata MySQL close after startup failure failed err=%v", closeErr)
			}
		}
	}()
	db.SetMaxIdleConns(config.StorageMysqlMaxIdleConnections)
	db.SetMaxOpenConns(config.StorageMysqlMaxOpenConnections)
	pingCtx, cancel := context.WithTimeout(ctx, o.DBTimeout)
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("connect metric metadata MySQL: %w", err)
	}
	store := storage.NewMySQLStore(db, o.DBTimeout, o.MaxScopes, o.MaxValues, o.TouchInterval, o.LoadPageSize)
	if err := store.CheckSchema(ctx); err != nil {
		return nil, err
	}
	kafkaCtx, cancelKafka := context.WithTimeout(ctx, o.DBTimeout)
	kafka, err := LoadConsulConfig(kafkaCtx)
	cancelKafka()
	if err != nil {
		return nil, err
	}
	if dataID != 0 && kafka.DataID != dataID {
		return nil, fmt.Errorf("metric metadata Consul DataID %d does not match task DataID %d", kafka.DataID, dataID)
	}
	dataID = kafka.DataID
	if err := window.RefreshRoutesIfDue(ctx, store, o.StateRefreshInterval); err != nil {
		logger.Warnf("metric metadata route snapshot refresh failed at startup dataid=%d; retaining any existing snapshot and using database lookups when no complete snapshot is available: %v", dataID, err)
	}
	p := window.NewProcessor(store, o, dataID)
	dataIDString := strconv.FormatUint(uint64(dataID), 10)
	tasks := []*scheduledTask{
		newScheduledTask("state-refresh", dataIDString, o.StateRefreshInterval, p.Refresh),
		newScheduledTask("expiry", dataIDString, o.MaintenanceInterval, p.Maintain),
	}
	consumer, err := notifier.New(dataID, notifier.KafkaConfig{
		Brokers: kafka.Brokers,
		Topic:   kafka.Topic, Username: kafka.Username, Password: kafka.Password,
	}, o, p)
	if err != nil {
		return nil, err
	}
	success = true
	logger.Infof("metric metadata consumer initialized dataid=%d topic=%s", dataID, kafka.Topic)
	return &runInstance{dataID: dataID, db: db, window: p, consumer: consumer, tasks: tasks}, nil
}
