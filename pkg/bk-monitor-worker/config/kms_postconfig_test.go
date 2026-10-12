// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/kmstest"
)

// Each child uses the same InitConfig entry point as controller, worker, task
// and file-based APM startup. Commands only inspect the cached process state.
func TestKMSPostConfigProcess(t *testing.T) {
	if path := os.Getenv("BMW_POSTCONFIG_TEST_FILE"); path != "" {
		FilePath = path
		InitConfig()
		for scanner := bufio.NewScanner(os.Stdin); scanner.Scan(); {
			data, err := json.Marshal(postConfigState())
			require.NoError(t, err)
			fmt.Printf("KMS_STATE:%s\n", data)
		}
		return
	}

	payload := kmstest.Payload(t, "../credential/testdata/payload.json")
	rabbitAccounts := payload["rabbitmq"]
	delete(payload, "report_tokens")
	delete(payload, "prometheus")
	delete(payload, "rabbitmq")
	dir := t.TempDir()
	values := map[string]any{
		"kms":        kmstest.Write(t, dir, payload),
		"broker":     map[string]any{"redis": map[string]any{"mode": "sentinel"}},
		"store":      map[string]any{"redis": map[string]any{"mode": "sentinel"}, "dependentRedis": map[string]any{"mode": "sentinel"}},
		"taskConfig": map[string]any{"rabbitmqMetric": map[string]any{"enabled": false}},
	}
	path := kmstest.ConfigFile(t, values)
	initial := startConfigProcess(t, path)
	base := initial()
	require.Empty(t, base["log_token"])
	require.Empty(t, base["slo_token"])
	require.Empty(t, base["prom_headers"])
	require.Empty(t, base["log_url"])
	require.Empty(t, base["slo_endpoint"])
	require.Empty(t, base["prom_url"])
	require.Equal(t, false, base["rabbit_enabled"])
	require.Equal(t, false, base["profile_enabled"])

	// A enables JSON reporting, while leaving SLO explicitly empty despite an
	// existing endpoint. All core credentials and RabbitMQ aliases are retained.
	payload["report_tokens"] = map[string]any{"log_search": "post-log-a", "rabbitmq": "post-rabbit-a", "slo": "", "profile": "post-profile-a"}
	payload["prometheus"] = map[string]any{"headers": map[string]any{}}
	payload["rabbitmq"] = rabbitAccounts
	kmstest.Write(t, dir, payload)
	values["taskConfig"] = map[string]any{
		"logSearch": map[string]any{"metric": map[string]any{"reportUrl": "http://127.0.0.1/v2/push", "reportDataId": 123}},
		"rabbitmqMetric": map[string]any{"enabled": true, "reportUrl": "http://127.0.0.1/v2/push", "reportDataId": 456, "instances": []any{
			map[string]any{"name": "b", "credentialsRef": "alias-b", "domainName": "localhost"},
			map[string]any{"name": "a", "credentialsRef": "alias-a", "domainName": "localhost", "vhosts": []string{"/"}, "queueIncludes": []string{"important.*"}},
		}},
		"metadata": map[string]any{"slo": map[string]any{"sloPushGatewayEndpoint": "http://127.0.0.1"}},
		"apmPreCalculate": map[string]any{"metrics": map[string]any{
			"profile": map[string]any{"enabled": true},
			"report":  map[string]any{"prometheus": map[string]any{"url": "http://127.0.0.1/write"}},
		}},
	}
	kmstest.WriteConfig(t, path, values)
	require.Equal(t, base, initial(), "mounted files must not mutate the initial process")
	aProcess := startConfigProcess(t, path)
	a := aProcess()
	require.Equal(t, base["core"], a["core"])
	require.Equal(t, "post-log-a", a["log_token"])
	require.Equal(t, "post-rabbit-a", a["rabbit_token"])
	require.Equal(t, "post-profile-a", a["profile_token"])
	require.Empty(t, a["slo_token"])
	instances := a["rabbit_instances"].([]any)
	require.Equal(t, "user-b", instances[0].(map[string]any)["Username"])
	require.Equal(t, "password-a", instances[1].(map[string]any)["Password"])
	require.Equal(t, []any{"important.*"}, instances[1].(map[string]any)["QueueIncludes"])

	// B adds only SLO and fixed Prometheus authentication.
	payload["report_tokens"].(map[string]any)["slo"] = "post-slo-b"
	payload["prometheus"] = map[string]any{"headers": map[string]any{"Authorization": "post-prom-b", "X-BK-TOKEN": "post-fixed-b"}}
	kmstest.Write(t, dir, payload)
	bProcess := startConfigProcess(t, path)
	b := bProcess()
	require.Equal(t, a, aProcess(), "the A process keeps its cached token and headers")
	require.Equal(t, "post-slo-b", b["slo_token"])
	require.Equal(t, "post-fixed-b", b["prom_headers"].(map[string]any)["X-BK-TOKEN"])
	for _, key := range []string{"core", "rabbit_instances", "log_token", "rabbit_token", "profile_token"} {
		require.Equal(t, a[key], b[key], key)
	}

	// Revert only B's leaves, using the current complete aggregate.
	payload["report_tokens"].(map[string]any)["slo"] = ""
	payload["prometheus"] = map[string]any{"headers": map[string]any{}}
	kmstest.Write(t, dir, payload)
	reverted := startConfigProcess(t, path)()
	require.Equal(t, a, reverted)
	require.Equal(t, b, bProcess(), "reverting files does not change the B process")
	require.Equal(t, base, initial())
}

func startConfigProcess(t *testing.T, path string) func() map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKMSPostConfigProcess$")
	cmd.Env = append(os.Environ(), "BMW_POSTCONFIG_TEST_FILE="+path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	in, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = in.Close()
		require.NoError(t, cmd.Wait(), stderr.String())
		cancel()
	})
	scanner := bufio.NewScanner(out)
	return func() map[string]any {
		t.Helper()
		_, err := fmt.Fprintln(in, "snapshot")
		require.NoError(t, err)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "KMS_STATE:"); ok {
				var state map[string]any
				require.NoError(t, json.Unmarshal([]byte(data), &state))
				return state
			}
		}
		t.Fatalf("configuration child stopped: %v; %s", scanner.Err(), stderr.String())
		return nil
	}
}

func postConfigState() map[string]any {
	return map[string]any{
		"core": []any{StorageMysqlUser, StorageMysqlPassword, BkApiAppCode, BkApiAppSecret,
			BrokerRedisStandalonePassword, BrokerRedisSentinelPassword, StorageRedisStandalonePassword, StorageRedisSentinelPassword,
			StorageDependentRedisStandalonePassword, StorageDependentRedisSentinelPassword,
			AesKey, BkdataAESKey, BkdataAESIv, BkdataTokenSalt, HashSecret, StorageConsulClientOptions},
		"log_token": ESClusterMetricReportAccessToken, "rabbit_token": RabbitMQClusterMetricReportAccessToken,
		"slo_token": SloPushGatewayToken, "profile_token": ProfileToken, "prom_headers": PromRemoteWriteHeaders,
		"log_url": ESClusterMetricReportUrl, "slo_endpoint": SloPushGatewayEndpoint, "prom_url": PromRemoteWriteUrl,
		"rabbit_instances": RabbitMQClusterMetricInstances, "rabbit_enabled": RabbitMQClusterMetricEnabled, "profile_enabled": ProfileEnabled,
	}
}

func TestKMSPostConfigRejectsIncompleteOrConflictingUpdates(t *testing.T) {
	if kmstest.Isolate(t) {
		return
	}
	old := FilePath
	t.Cleanup(func() { FilePath = old; viper.Reset() })
	for _, name := range []string{"partial aggregate", "missing enabled token", "mismatched key", "invalid envelope", "plaintext conflict"} {
		t.Run(name, func(t *testing.T) {
			payload := kmstest.Payload(t, "../credential/testdata/payload.json")
			control := kmstest.Write(t, t.TempDir(), payload)
			values := map[string]any{"kms": control}
			FilePath = kmstest.ConfigFile(t, values)
			viper.Reset()
			require.NoError(t, loadConfig())
			before := postConfigState()
			switch name {
			case "partial aggregate":
				control = kmstest.Write(t, t.TempDir(), map[string]any{"report_tokens": map[string]any{"slo": "new-token"}})
			case "missing enabled token":
				delete(payload["report_tokens"].(map[string]any), "slo")
				control = kmstest.Write(t, t.TempDir(), payload)
				values["taskConfig"] = map[string]any{"metadata": map[string]any{"slo": map[string]any{"sloPushGatewayEndpoint": "http://127.0.0.1"}}}
			case "mismatched key":
				other := kmstest.Write(t, t.TempDir(), payload)
				control["private_key_file"] = other["private_key_file"]
			case "invalid envelope":
				require.NoError(t, os.WriteFile(control["envelope_file"].(string), []byte("invalid-test-envelope"), 0600))
			case "plaintext conflict":
				values["taskConfig"] = map[string]any{"metadata": map[string]any{"slo": map[string]any{"sloPushGatewayToken": "plaintext-test-token"}}}
			}
			values["kms"] = control
			kmstest.WriteConfig(t, FilePath, values)
			viper.Reset()
			require.Error(t, loadConfig())
			require.Equal(t, before, postConfigState(), "failed validation must not publish cached configuration")
		})
	}
}
