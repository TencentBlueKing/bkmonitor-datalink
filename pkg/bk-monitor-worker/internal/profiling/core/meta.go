// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	consulStore "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
)

// ConsulClient provides the DataID metadata operations used by the metadata center.
type ConsulClient interface {
	Get(key string) (uint64, []byte, error)
	ListKeysWithPrefix(prefix string) ([]string, error)
}

// KafkaInfo contains the Kafka connection values stored in a DataID Consul key.
type KafkaInfo struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
	Topic    string `json:"topic"`
}

// DataIDInfo is the Active Profiling metadata stored at one Consul key.
type DataIDInfo struct {
	TenantID  string    `json:"bk_tenant_id"`
	BizID     int       `json:"bk_biz_id"`
	DataID    uint64    `json:"bk_data_id"`
	KafkaInfo KafkaInfo `json:"kafka_info"`
}

// MetadataCenter loads DataID configuration from BMW's Consul KV tree.
type MetadataCenter struct {
	Consul    ConsulClient
	keyPrefix string
}

// NewMetadataCenter creates a metadata center using the complete DataID key prefix.
func NewMetadataCenter(client ConsulClient, keyPrefix string) (*MetadataCenter, error) {
	if client == nil {
		return nil, fmt.Errorf("BMW Consul client is required")
	}
	keyPrefix = strings.Trim(keyPrefix, "/")
	if keyPrefix == "" {
		return nil, fmt.Errorf("Active Profiling Consul key prefix is empty")
	}
	return &MetadataCenter{
		Consul:    client,
		keyPrefix: keyPrefix + "/",
	}, nil
}

// NewConfiguredMetadataCenter combines the configured Consul root and Active Profiling path prefixes.
func NewConfiguredMetadataCenter() (*MetadataCenter, error) {
	client, err := consulStore.GetInstance()
	if err != nil {
		return nil, fmt.Errorf("get BMW Consul client: %w", err)
	}
	consulRoot := strings.Trim(config.StorageConsulPathPrefix, "/")
	profilingPath := strings.Trim(config.ActiveProfilingConsulPathPrefix, "/")
	if consulRoot == "" {
		return nil, fmt.Errorf("BMW Consul pathPrefix is empty")
	}
	if profilingPath == "" {
		return nil, fmt.Errorf("Active Profiling Consul pathPrefix is empty")
	}
	return NewMetadataCenter(client, consulRoot+"/"+profilingPath)
}

// ListDataIDs returns DataIDs stored directly beneath the Active Profiling KV prefix.
// An empty result means the prefix currently has no DataID keys.
func (c *MetadataCenter) ListDataIDs() ([]uint64, error) {
	if c == nil || c.Consul == nil || c.keyPrefix == "" {
		return nil, fmt.Errorf("Active Profiling metadata center is not initialized")
	}
	keys, err := c.Consul.ListKeysWithPrefix(c.keyPrefix)
	if err != nil {
		return nil, fmt.Errorf("list Active Profiling DataIDs from Consul prefix %q: %w", c.keyPrefix, err)
	}

	dataIDs := make([]uint64, 0, len(keys))
	seen := make(map[uint64]struct{}, len(keys))
	for _, key := range keys {
		if strings.HasSuffix(key, "/") {
			continue
		}
		fullKey := key
		key = strings.TrimPrefix(key, c.keyPrefix)
		if key == fullKey || key == "" || strings.Contains(key, "/") {
			return nil, fmt.Errorf("unexpected Active Profiling DataID Consul key %q", fullKey)
		}
		dataID, err := strconv.ParseUint(key, 10, 64)
		if err != nil || dataID == 0 {
			return nil, fmt.Errorf("invalid Active Profiling DataID Consul key %q", fullKey)
		}
		if _, exists := seen[dataID]; exists {
			continue
		}
		seen[dataID] = struct{}{}
		dataIDs = append(dataIDs, dataID)
	}
	sort.Slice(dataIDs, func(i, j int) bool { return dataIDs[i] < dataIDs[j] })
	return dataIDs, nil
}

// GetDataIDInfo reads and validates one DataID's JSON value from Consul.
func (c *MetadataCenter) GetDataIDInfo(ctx context.Context, dataID uint64) (DataIDInfo, error) {
	if c == nil || c.Consul == nil || c.keyPrefix == "" || dataID == 0 {
		return DataIDInfo{}, fmt.Errorf("Active Profiling metadata center and DataID are required")
	}
	if err := ctx.Err(); err != nil {
		return DataIDInfo{}, err
	}
	key := fmt.Sprintf("%s%d", c.keyPrefix, dataID)
	_, value, err := c.Consul.Get(key)
	if err != nil {
		return DataIDInfo{}, fmt.Errorf("get Active Profiling DataID %d config from Consul: %w", dataID, err)
	}
	if len(value) == 0 {
		return DataIDInfo{}, fmt.Errorf("Active Profiling DataID %d config does not exist at Consul key %q", dataID, key)
	}
	return decodeDataIDInfo(dataID, value)
}

func decodeDataIDInfo(dataID uint64, value []byte) (DataIDInfo, error) {
	var info DataIDInfo
	if err := json.Unmarshal(value, &info); err != nil {
		return DataIDInfo{}, fmt.Errorf("decode Active Profiling DataID %d Consul config: %w", dataID, err)
	}
	if info.DataID != dataID {
		return DataIDInfo{}, fmt.Errorf("Active Profiling DataID key %d does not match Consul config bk_data_id %d", dataID, info.DataID)
	}
	if info.TenantID == "" || info.BizID <= 0 {
		return DataIDInfo{}, fmt.Errorf("Active Profiling DataID %d config requires bk_tenant_id and positive bk_biz_id", dataID)
	}
	if info.KafkaInfo.Host == "" || info.KafkaInfo.Topic == "" {
		return DataIDInfo{}, fmt.Errorf("Active Profiling DataID %d config requires Kafka host and topic", dataID)
	}
	if (info.KafkaInfo.Username == "") != (info.KafkaInfo.Password == "") {
		return DataIDInfo{}, fmt.Errorf("Active Profiling DataID %d config requires both Kafka username and password", dataID)
	}
	return info, nil
}
