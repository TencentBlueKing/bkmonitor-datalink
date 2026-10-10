// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

package credential

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func testPayload(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/payload.json")
	require.NoError(t, err)
	var p map[string]any
	require.NoError(t, json.Unmarshal(data, &p))
	return p
}

func prepareTestPayload(t *testing.T, v *viper.Viper, p map[string]any) (*Snapshot, error) {
	t.Helper()
	data, err := json.Marshal(p)
	require.NoError(t, err)
	return preparePayload(v, string(data))
}

func TestPayloadBindsEmptyValuesAndDistinctRedisPasswords(t *testing.T) {
	v := viper.New()
	for _, key := range []string{"broker.redis", "store.redis", "store.dependentRedis"} {
		v.Set(key+".mode", "sentinel")
	}
	p := testPayload(t)
	p["redis"].(map[string]any)["broker"].(map[string]any)["password"] = ""
	s, err := prepareTestPayload(t, v, p)
	require.NoError(t, err)
	s.Apply(v)
	require.True(t, v.IsSet("broker.redis.standalone.password"))
	require.Empty(t, v.GetString("broker.redis.standalone.password"))
	for role, key := range map[string]string{"store": "store.redis", "dependent": "store.dependentRedis"} {
		require.Equal(t, "test-"+role+"-data", v.GetString(key+".standalone.password"))
	}
	for role, key := range map[string]string{"broker": "broker.redis", "store": "store.redis", "dependent": "store.dependentRedis"} {
		require.Equal(t, "test-"+role+"-sentinel", v.GetString(key+".sentinel.password"))
	}
	for _, key := range []string{"aes.key", "aes.bkdataAESKey", "taskConfig.apmPreCalculate.hashSecret"} {
		require.True(t, v.IsSet(key))
		require.Empty(t, v.GetString(key))
	}
	require.Equal(t, "test-mysql-password", v.GetString("store.mysql.password"))
	require.Equal(t, "test-api-secret", v.GetString("taskConfig.common.bkapi.appSecret"))
	require.Equal(t, "test-log-token", v.GetString("taskConfig.logSearch.metric.reportAccessToken"))
	require.Equal(t, "test-slo-token", v.GetString("taskConfig.metadata.slo.sloPushGatewayToken"))
	require.Equal(t, "test-profile-token", v.GetString("taskConfig.apmPreCalculate.metrics.profile.token"))
	require.Equal(t, "test-prometheus-secret", v.GetStringMapString("taskConfig.apmPreCalculate.metrics.report.prometheus.headers")["Authorization"])
}

func TestPayloadRejectsInvalidSchemaWithoutPublishing(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"unknown field": func(p map[string]any) { p["SECRET_MARKER"] = "SECRET_MARKER" },
		"wrong version": func(p map[string]any) { p["schema_version"] = 2 },
		"wrong service": func(p map[string]any) { p["service"] = "unify-query" },
		"wrong type":    func(p map[string]any) { p["redis"].(map[string]any)["broker"].(map[string]any)["password"] = 7 },
		"missing redis": func(p map[string]any) { delete(p, "redis") },
		"null redis":    func(p map[string]any) { p["redis"].(map[string]any)["broker"].(map[string]any)["password"] = nil },
		"unknown alias": func(p map[string]any) {
			p["redis"].(map[string]any)["SECRET_MARKER"] = map[string]any{"password": "SECRET_MARKER"}
		},
		"unpaired app": func(p map[string]any) {
			delete(p["bkapp_id_secret"].(map[string]any)["default"].(map[string]any), "app_secret")
		},
		"missing encryption": func(p map[string]any) { delete(p, "encryption") },
		"unpaired consul":    func(p map[string]any) { delete(p["consul"].(map[string]any)["default"].(map[string]any), "password") },
		"wrong headers": func(p map[string]any) {
			p["prometheus"] = map[string]any{"headers": map[string]any{"Authorization": 3}}
		},
		"null header": func(p map[string]any) {
			p["prometheus"] = map[string]any{"headers": map[string]any{"Authorization": nil}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			v.Set("store.mysql.password", "old")
			p := testPayload(t)
			change(p)
			s, err := prepareTestPayload(t, v, p)
			require.Error(t, err)
			require.Nil(t, s)
			require.NotContains(t, err.Error(), "SECRET_MARKER")
			require.Equal(t, "old", v.GetString("store.mysql.password"))
		})
	}
	_, err := preparePayload(viper.New(), `{} {}`)
	require.Error(t, err)
}

func TestOptionalGroupsAndSentinelRequirement(t *testing.T) {
	p := testPayload(t)
	for _, key := range []string{"report_tokens", "prometheus", "rabbitmq", "redis_sentinel"} {
		delete(p, key)
	}
	s, err := prepareTestPayload(t, viper.New(), p)
	require.NoError(t, err)
	require.NotNil(t, s)
	for _, key := range []string{"broker.redis.mode", "store.redis.mode", "store.dependentRedis.mode"} {
		v := viper.New()
		v.Set(key, "sentinel")
		_, err := prepareTestPayload(t, v, p)
		require.Error(t, err)
	}
	for _, item := range []struct {
		key   string
		value any
	}{
		{"taskConfig.logSearch.metric.reportUrl", "http://localhost"},
		{"taskConfig.metadata.slo.sloPushGatewayEndpoint", "http://localhost"}, {"taskConfig.apmPreCalculate.metrics.profile.enabled", true},
		{"taskConfig.apmPreCalculate.metrics.report.prometheus.url", "http://localhost"},
	} {
		v := viper.New()
		v.Set(item.key, item.value)
		_, err := prepareTestPayload(t, v, p)
		require.Error(t, err, item.key)
	}
}

func TestAppIdentityRequiredWithMissingOrFalseLegacyEnabled(t *testing.T) {
	for _, explicitFalse := range []bool{false, true} {
		v := viper.New()
		if explicitFalse {
			v.Set("taskConfig.common.bkapi.enabled", false)
		}
		for _, field := range []string{"group", "app_code", "app_secret"} {
			p := testPayload(t)
			if field == "group" {
				delete(p, "bkapp_id_secret")
			} else {
				delete(p["bkapp_id_secret"].(map[string]any)["default"].(map[string]any), field)
			}
			_, err := prepareTestPayload(t, v, p)
			require.Error(t, err)
			require.Contains(t, err.Error(), "taskConfig.common.bkapi")
		}
		s, err := prepareTestPayload(t, v, testPayload(t))
		require.NoError(t, err)
		s.Apply(v)
		require.Equal(t, "test-app", v.GetString("taskConfig.common.bkapi.appCode"))
		require.Equal(t, "test-api-secret", v.GetString("taskConfig.common.bkapi.appSecret"))
	}
}

func TestHashSecretRequiresExplicitValueWithoutAPMConfiguration(t *testing.T) {
	p := testPayload(t)
	delete(p["encryption"].(map[string]any), "apm_hash_secret")
	_, err := prepareTestPayload(t, viper.New(), p)
	require.ErrorContains(t, err, "taskConfig.apmPreCalculate.hashSecret")
	p["encryption"].(map[string]any)["apm_hash_secret"] = ""
	s, err := prepareTestPayload(t, viper.New(), p)
	require.NoError(t, err)
	v := viper.New()
	s.Apply(v)
	require.True(t, v.IsSet("taskConfig.apmPreCalculate.hashSecret"))
	require.Empty(t, v.GetString("taskConfig.apmPreCalculate.hashSecret"))
}

func rabbitConfig(t *testing.T, instances string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigType("json")
	require.NoError(t, v.ReadConfig(strings.NewReader(`{"taskConfig":{"rabbitmqMetric":{"enabled":true,"instances":`+instances+`}}}`)))
	return v
}

func TestRabbitMQAliasesPreserveCompleteInstancesAcrossReorder(t *testing.T) {
	for _, instances := range []string{
		`[{"name":"a","credentialsRef":"alias-a","domainName":"localhost","vhosts":["/"],"queueIncludes":["important.*"],"bkTenantId":"tenant-a"},{"name":"b","credentialsRef":"alias-b"},{"name":"shared","credentialsRef":"alias-a"},{"name":"anonymous","credentialsRef":"anonymous"}]`,
		`[{"name":"b","credentialsRef":"alias-b"},{"name":"a","credentialsRef":"alias-a","domainName":"localhost","vhosts":["/"],"queueIncludes":["important.*"],"bkTenantId":"tenant-a"}]`,
	} {
		v := rabbitConfig(t, instances)
		s, err := prepareTestPayload(t, v, testPayload(t))
		require.NoError(t, err)
		s.Apply(v)
		var result []map[string]any
		require.NoError(t, v.UnmarshalKey("taskConfig.rabbitmqMetric.instances", &result))
		for _, instance := range result {
			switch instance["name"] {
			case "a", "shared":
				require.Equal(t, "user-a", instance["username"])
				require.Equal(t, "password-a", instance["password"])
			case "b":
				require.Equal(t, "user-b", instance["username"])
			case "anonymous":
				require.Empty(t, instance["username"])
				require.Empty(t, instance["password"])
			}
			if instance["name"] == "a" {
				require.Equal(t, "localhost", instance["domainname"])
				require.Equal(t, "tenant-a", instance["bktenantid"])
				require.Len(t, instance["vhosts"], 1)
				require.Len(t, instance["queueincludes"], 1)
			}
		}
	}
}

func TestRabbitMQRejectsReferencesAndPlaintext(t *testing.T) {
	for _, instances := range []string{`[{"name":"missing"}]`, `[{"credentialsRef":"typo"}]`, `[{"credentialsRef":"alias-a","password":"SECRET_MARKER"}]`, `[{"credentialsRef":"alias-a","username":"SECRET_MARKER"}]`} {
		v := rabbitConfig(t, instances)
		s, err := prepareTestPayload(t, v, testPayload(t))
		require.Error(t, err)
		require.Nil(t, s)
		require.NotContains(t, err.Error(), "SECRET_MARKER")
	}
	v := rabbitConfig(t, `[{"credentialsRef":"alias-a"}]`)
	p := testPayload(t)
	delete(p["rabbitmq"].(map[string]any)["alias-a"].(map[string]any), "password")
	_, err := prepareTestPayload(t, v, p)
	require.Error(t, err)
	p = testPayload(t)
	delete(p["report_tokens"].(map[string]any), "rabbitmq")
	_, err = prepareTestPayload(t, v, p)
	require.Error(t, err)
	v.Set("taskConfig.rabbitmqMetric.enabled", false)
	delete(p, "rabbitmq")
	v.Set("taskConfig.rabbitmqMetric.instances", []any{})
	_, err = prepareTestPayload(t, v, p)
	require.NoError(t, err)
}

func sdkConfig(t *testing.T) (*viper.Viper, *viper.Viper) {
	t.Helper()
	dir, err := filepath.Abs("testdata")
	require.NoError(t, err)
	data, err := json.Marshal(map[string]any{"kms": map[string]any{"enabled": true, "envelope_file": filepath.Join(dir, "envelope"), "private_key_file": filepath.Join(dir, "private-key")}})
	require.NoError(t, err)
	v, raw := viper.New(), viper.New()
	for _, config := range []*viper.Viper{v, raw} {
		config.SetConfigType("json")
		require.NoError(t, config.ReadConfig(bytes.NewReader(data)))
	}
	v.AutomaticEnv()
	v.SetEnvPrefix("bmw")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	return v, raw
}

func TestPrepareUsesOfficialSDKAndFiles(t *testing.T) {
	v, raw := sdkConfig(t)
	s, err := Prepare(v, raw, "bmw")
	require.NoError(t, err)
	s.Apply(v)
	require.Equal(t, "test-broker-data", v.GetString("broker.redis.standalone.password"))
	require.Equal(t, "test-consul-token", s.ConsulOptions.Token)
	for _, key := range []string{"kms.envelope_file", "kms.private_key_file"} {
		_, raw := sdkConfig(t)
		raw.Set(key, filepath.Join(t.TempDir(), "absent"))
		_, err := Prepare(viper.New(), raw, "bmw")
		require.Error(t, err)
		require.Contains(t, err.Error(), key)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	require.NoError(t, os.WriteFile(bad, []byte("SECRET_MARKER"), 0600))
	for _, key := range []string{"kms.envelope_file", "kms.private_key_file"} {
		_, raw := sdkConfig(t)
		raw.Set(key, bad)
		_, err := Prepare(viper.New(), raw, "bmw")
		require.ErrorContains(t, err, "kms decrypt failed")
		require.NotContains(t, err.Error(), "SECRET_MARKER")
	}
}

func TestSnapshotDoesNotRereadUpdatedSecretFiles(t *testing.T) {
	v, raw := sdkConfig(t)
	dir := t.TempDir()
	for _, key := range []string{"kms.envelope_file", "kms.private_key_file"} {
		data, err := os.ReadFile(raw.GetString(key))
		require.NoError(t, err)
		path := filepath.Join(dir, filepath.Base(raw.GetString(key)))
		require.NoError(t, os.WriteFile(path, data, 0600))
		raw.Set(key, path)
	}
	snapshot, err := Prepare(v, raw, "bmw")
	require.NoError(t, err)
	for _, key := range []string{"kms.envelope_file", "kms.private_key_file"} {
		require.NoError(t, os.WriteFile(raw.GetString(key), []byte("updated-invalid-material"), 0600))
	}
	snapshot.Apply(v)
	require.Equal(t, "test-broker-data", v.GetString("broker.redis.standalone.password"))
	_, err = Prepare(v, raw, "bmw")
	require.ErrorContains(t, err, "kms decrypt failed")
}

func TestPrepareRejectsPlaintextEnvironmentAndAddressEntrances(t *testing.T) {
	for _, key := range sensitiveKeys {
		t.Run(key, func(t *testing.T) {
			v, raw := sdkConfig(t)
			raw.Set(key, "SECRET_MARKER")
			_, err := Prepare(v, raw, "bmw")
			require.Error(t, err)
			require.NotContains(t, err.Error(), "SECRET_MARKER")
			raw.Set(key, "")
			t.Setenv(envName("bmw", key), "SECRET_MARKER")
			_, err = Prepare(v, raw, "bmw")
			require.Error(t, err)
		})
	}
	for _, key := range []string{"CONSUL_HTTP_TOKEN", "CONSUL_HTTP_TOKEN_FILE", "CONSUL_HTTP_AUTH", "CONSUL_CLIENT_KEY", "BMW_KMS_ENABLED", "BMW_KMS_ENVELOPE_FILE", "BMW_KMS_PRIVATE_KEY_FILE"} {
		t.Run(key, func(t *testing.T) {
			v, raw := sdkConfig(t)
			t.Setenv(key, "SECRET_MARKER")
			_, err := Prepare(v, raw, "bmw")
			require.Error(t, err)
			require.NotContains(t, err.Error(), "SECRET_MARKER")
		})
	}
	for _, address := range []string{"http://user:SECRET_MARKER@localhost", "localhost?token=SECRET_MARKER", "http://localhost?app_secret=SECRET_MARKER"} {
		v, raw := sdkConfig(t)
		v.Set("store.consul.addr", address)
		_, err := Prepare(v, raw, "bmw")
		require.Error(t, err)
		require.NotContains(t, err.Error(), "SECRET_MARKER")
	}
}

func TestDisabledRetainsLegacyConfiguration(t *testing.T) {
	v := viper.New()
	v.Set("store.mysql.password", "legacy")
	raw := viper.New()
	raw.Set("kms.enabled", false)
	t.Setenv("CONSUL_HTTP_TOKEN", "legacy-token")
	s, err := Prepare(v, raw, "bmw")
	require.NoError(t, err)
	require.Nil(t, s)
	require.Equal(t, "legacy", v.GetString("store.mysql.password"))
	t.Setenv("BMW_KMS_ENABLED", "false")
	_, err = Prepare(v, raw, "bmw")
	require.ErrorContains(t, err, "kms control conflict")
}
