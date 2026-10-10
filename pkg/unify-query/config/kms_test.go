package config_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/kmstest"
	redisService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/trace"
)

func TestKMSConsumersAndTransactionalReload(t *testing.T) {
	t.Setenv("UNIFY-QUERY_TEST", "")
	viper.Reset()
	previous := config.CustomConfigFilePath
	t.Cleanup(func() { viper.Reset(); config.CustomConfigFilePath = previous })
	payload := `{"schema_version":1,"service":"unify-query","redis":{"default":{"password":"node-secret"}},"redis_sentinel":{"default":{"password":"sentinel-secret"}},"bkapp_id_secret":{"default":{"app_code":"example-app","app_secret":"app-secret"}},"bkdata":{"default":{"token":"data-secret"}},"telemetry":{"default":{"token":"trace-secret","headers":{}}}}`
	envelope, key := kmstest.Files(t, payload)
	config.CustomConfigFilePath = filepath.Join(t.TempDir(), "config.yaml")
	write := func(mode, suffix string) {
		t.Helper()
		data := fmt.Sprintf("kms:\n  enabled: %s\n  envelope_file: %s\n  private_key_file: %s\nredis:\n  mode: sentinel\n  master_name: test-master\n  sentinel_address: [127.0.0.1:6379]\nbk_api:\n  address: http://127.0.0.1:12001\n  code: example-app\nbk_data:\n  authentication_method: token\nquery:\n  marker: %s\n", mode, envelope, key, suffix)
		require.NoError(t, os.WriteFile(config.CustomConfigFilePath, []byte(data), 0600))
	}
	viper.SetDefault("test.defaults", map[string]any{"marker": "default-value"})
	viper.Set("test.override", map[string]any{"marker": "override-value"})
	flags := pflag.NewFlagSet("config-test", pflag.ContinueOnError)
	flags.String("setting", "flag-default", "")
	require.NoError(t, flags.Set("setting", "flag-value"))
	require.NoError(t, viper.BindPFlag("test.flag", flags.Lookup("setting")))
	t.Setenv("UNIFY-QUERY_TEST_ENV", "env-value")
	write("true", "first")
	require.NoError(t, config.InitConfigWithWriter(io.Discard))
	options := redisService.ClientOptions()
	require.Equal(t, "node-secret", options.Password)
	require.Equal(t, "sentinel-secret", options.SentinelPassword)
	require.Equal(t, "test-master", options.MasterName)
	var auth map[string]string
	require.NoError(t, json.Unmarshal([]byte(bkapi.GetBkAPI().Headers(nil)[bkapi.BkAPIAuthorization]), &auth))
	require.Equal(t, "app-secret", auth[bkapi.BkSecretKey])
	require.Equal(t, "example-app", auth[bkapi.BkAppCodeKey])
	require.Equal(t, "data-secret", bkapi.GetBkDataAPI().GetDataAuth()[bkapi.BkDataDataTokenKey])
	for k, want := range map[string]string{"test.defaults.marker": "default-value", "test.override.marker": "override-value", "test.flag": "flag-value", "test.env": "env-value"} {
		require.Equal(t, want, viper.GetString(k))
	}
	settingsBefore := viper.AllSettings()
	write("false", "rejected")
	require.Error(t, config.ReloadConfigWithWriter(io.Discard))
	require.Equal(t, settingsBefore, viper.AllSettings(), "a rejected candidate must preserve config, shared defaults and overrides")
	require.Equal(t, "first", viper.GetString("query.marker"))
	require.Equal(t, "node-secret", redisService.ClientOptions().Password)
	write("true", "second")
	require.NoError(t, os.Remove(envelope))
	require.NoError(t, os.Remove(key))
	require.NoError(t, config.ReloadConfigWithWriter(io.Discard))
	require.Equal(t, "second", viper.GetString("query.marker"))
	require.Equal(t, "node-secret", redisService.ClientOptions().Password)
	write("true", "third")
	data, err := os.ReadFile(config.CustomConfigFilePath)
	require.NoError(t, err)
	data = append(data, []byte("consul:\n  token: forbidden-secret\n")...)
	require.NoError(t, os.WriteFile(config.CustomConfigFilePath, data, 0600))
	require.Error(t, config.ReloadConfigWithWriter(io.Discard))
	require.Equal(t, "second", viper.GetString("query.marker"))
	require.Equal(t, "node-secret", viper.GetString("redis.password"))
	// A new startup reads the new Secret, unlike SIGUSR1.
	newEnvelope, newKey := kmstest.Files(t, `{"schema_version":1,"service":"unify-query","redis":{"default":{"password":"new-node-secret"}},"telemetry":{"default":{"token":"","headers":{}}}}`)
	data = []byte(fmt.Sprintf("kms:\n  enabled: true\n  envelope_file: %s\n  private_key_file: %s\n", newEnvelope, newKey))
	require.NoError(t, os.WriteFile(config.CustomConfigFilePath, data, 0600))
	viper.Reset()
	require.NoError(t, config.InitConfigWithWriter(io.Discard))
	require.Equal(t, "new-node-secret", redisService.ClientOptions().Password)
}

func TestKMSStartupFailureDoesNotPublishConsumers(t *testing.T) {
	viper.Reset()
	previous := config.CustomConfigFilePath
	t.Cleanup(func() { viper.Reset(); config.CustomConfigFilePath = previous })
	config.CustomConfigFilePath = filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(config.CustomConfigFilePath, []byte("redis:\n  password: legacy-secret\nquery:\n  marker: old\n"), 0600))
	require.NoError(t, config.InitConfigWithWriter(io.Discard))
	before := redisService.ClientOptions().Password
	require.NoError(t, os.WriteFile(config.CustomConfigFilePath, []byte("kms:\n  enabled: true\n  envelope_file: /missing-envelope\n  private_key_file: /missing-key\nquery:\n  marker: rejected\n"), 0600))
	require.Error(t, config.InitConfigWithWriter(io.Discard))
	require.Equal(t, "old", viper.GetString("query.marker"))
	require.Equal(t, before, redisService.ClientOptions().Password)
}
