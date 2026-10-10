// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestKMSBindsBeforeAllKeysForEveryEntryPoint(t *testing.T) {
	old := FilePath
	t.Cleanup(func() { FilePath = old; viper.Reset() })
	dir, err := filepath.Abs("../credential/testdata")
	require.NoError(t, err)
	config := map[string]any{
		"kms":        map[string]any{"enabled": true, "envelope_file": filepath.Join(dir, "envelope"), "private_key_file": filepath.Join(dir, "private-key")},
		"broker":     map[string]any{"redis": map[string]any{"mode": "sentinel"}},
		"store":      map[string]any{"redis": map[string]any{"mode": "sentinel"}, "dependentRedis": map[string]any{"mode": "sentinel"}},
		"taskConfig": map[string]any{"rabbitmqMetric": map[string]any{"instances": []any{map[string]any{"name": "b", "credentialsRef": "alias-b"}, map[string]any{"name": "a", "credentialsRef": "alias-a", "vhosts": []string{"/"}, "bkTenantId": "test-tenant", "queueIncludes": []string{"important.*"}}}}},
	}
	data, err := json.Marshal(config)
	require.NoError(t, err)
	FilePath = filepath.Join(t.TempDir(), "bmw.json")
	require.NoError(t, os.WriteFile(FilePath, data, 0600))
	viper.Reset()
	require.NoError(t, loadConfig())
	// controller, worker, task and APM start_from_file all call this same API.
	require.Equal(t, "test-broker-data", BrokerRedisStandalonePassword)
	require.Equal(t, "test-broker-sentinel", BrokerRedisSentinelPassword)
	require.Equal(t, "test-store-data", StorageRedisStandalonePassword)
	require.Equal(t, "test-store-sentinel", StorageRedisSentinelPassword)
	require.Equal(t, "test-dependent-data", StorageDependentRedisStandalonePassword)
	require.Equal(t, "test-dependent-sentinel", StorageDependentRedisSentinelPassword)
	require.Equal(t, "test-user", StorageMysqlUser)
	require.Equal(t, "test-mysql-password", StorageMysqlPassword)
	require.Equal(t, "test-app", BkApiAppCode)
	require.Equal(t, "test-api-secret", BkApiAppSecret)
	require.Empty(t, AesKey)
	require.Empty(t, BkdataAESKey)
	require.Equal(t, "bkbkbkbkbkbkbkbk", BkdataAESIv)
	require.Empty(t, HashSecret)
	require.Equal(t, "test-log-token", ESClusterMetricReportAccessToken)
	require.Equal(t, "test-rabbit-report-token", RabbitMQClusterMetricReportAccessToken)
	require.Equal(t, "test-slo-token", SloPushGatewayToken)
	require.Equal(t, "test-profile-token", ProfileToken)
	require.Equal(t, "test-prometheus-secret", PromRemoteWriteHeaders["Authorization"])
	require.Equal(t, "test-consul-token", StorageConsulClientOptions.Token)
	require.Len(t, RabbitMQClusterMetricInstances, 2)
	require.Equal(t, "user-b", RabbitMQClusterMetricInstances[0].Username)
	require.Equal(t, "user-a", RabbitMQClusterMetricInstances[1].Username)
	require.Equal(t, "test-tenant", RabbitMQClusterMetricInstances[1].BkTenantID)
	require.Equal(t, []string{"important.*"}, RabbitMQClusterMetricInstances[1].QueueIncludes)
}

func TestKMSFailureDoesNotInitializeVariables(t *testing.T) {
	old := FilePath
	oldUser := StorageMysqlUser
	t.Cleanup(func() { FilePath = old; StorageMysqlUser = oldUser; viper.Reset() })
	viper.Reset()
	StorageMysqlUser = "unchanged"
	FilePath = filepath.Join(t.TempDir(), "bmw.yaml")
	require.NoError(t, os.WriteFile(FilePath, []byte("kms:\n  enabled: true\n  envelope_file: /absent-test-envelope\n  private_key_file: /absent-test-key\n"), 0600))
	require.Error(t, loadConfig())
	require.Equal(t, "unchanged", StorageMysqlUser)
}
