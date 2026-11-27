// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"fmt"
	"time"
)

const StateRefreshInterval = 15 * time.Minute

// Kafka offset reset strategies for consumer groups without a committed offset.
const (
	KafkaOffsetNewest = "newest"
	KafkaOffsetOldest = "oldest"
)

// Options bounds consumption, retained state and database operations.
type Options struct {
	BatchRecords  int
	BatchBytes    int
	BatchWait     time.Duration // Maximum duration of a consumer aggregation window.
	QueueMessages int
	QueueBytes    int64
	LoadPageSize  int
	// MaxScopes and MaxValues are optional per-group admission limits; zero means unlimited.
	MaxScopes     int
	MaxValues     int
	DBTimeout     time.Duration
	RetryInterval time.Duration
	DrainTimeout  time.Duration
	// KafkaOffsetReset selects the initial offset when the consumer group has
	// no committed offset: KafkaOffsetNewest or KafkaOffsetOldest.
	KafkaOffsetReset string
	// MaintenanceInterval controls how often the expiry task removes stale scopes.
	MaintenanceInterval time.Duration
	// StateRefreshInterval controls how often the full state snapshot is reloaded.
	StateRefreshInterval time.Duration
	// TouchInterval controls how often active scopes refresh their last-seen time.
	TouchInterval time.Duration
	ExpireAfter   time.Duration
}

// DefaultOptions provides limits suitable for the metadata report protocol.
func DefaultOptions() Options {
	return Options{
		BatchRecords: 30000, BatchBytes: 16 << 20, BatchWait: 30 * time.Second,
		QueueMessages: 1000, QueueBytes: 64 << 20,
		LoadPageSize: 1000,
		DBTimeout:    20 * time.Second, RetryInterval: 5 * time.Second, DrainTimeout: 25 * time.Second,
		KafkaOffsetReset:    KafkaOffsetNewest,
		MaintenanceInterval: 5 * time.Minute, StateRefreshInterval: StateRefreshInterval,
		TouchInterval: 24 * time.Hour,
		ExpireAfter:   30 * 24 * time.Hour,
	}
}

// Validate checks limits before starting any consumers or database work.
func (o Options) Validate() error {
	if o.BatchRecords <= 0 || o.BatchBytes <= 0 || o.BatchWait <= 0 ||
		o.QueueMessages <= 0 || o.QueueBytes <= 0 ||
		o.LoadPageSize <= 0 ||
		o.MaxScopes < 0 || o.MaxValues < 0 ||
		o.DBTimeout <= 0 || o.RetryInterval <= 0 || o.DrainTimeout <= o.DBTimeout ||
		(o.KafkaOffsetReset != KafkaOffsetNewest && o.KafkaOffsetReset != KafkaOffsetOldest) ||
		o.MaintenanceInterval <= 0 || o.StateRefreshInterval <= 0 || o.TouchInterval <= 0 || o.ExpireAfter <= o.TouchInterval {
		return fmt.Errorf("invalid metric metadata capacity or timeout configuration")
	}
	return nil
}
