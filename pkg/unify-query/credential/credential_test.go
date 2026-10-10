package credential

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/kmstest"
)

const minimalPayload = `{"schema_version":1,"service":"unify-query","redis":{"default":{"password":"redis-secret"}}}`

func configFor(t *testing.T, payload string, extra string) (*viper.Viper, *viper.Viper) {
	t.Helper()
	envelope, key := kmstest.Files(t, payload)
	data := fmt.Sprintf("kms:\n  enabled: true\n  envelope_file: %s\n  private_key_file: %s\n%s", envelope, key, extra)
	raw, runtime := viper.New(), viper.New()
	for _, v := range []*viper.Viper{raw, runtime} {
		v.SetConfigType("yaml")
		require.NoError(t, v.ReadConfig(strings.NewReader(data)))
	}
	runtime.SetDefault("trace.enable", false)
	return raw, runtime
}

func TestSDKLoadAndFrozenSnapshot(t *testing.T) {
	raw, runtime := configFor(t, `{"schema_version":1,"service":"unify-query","redis":{"default":{"password":""}},"redis_sentinel":{"default":{"password":"sentinel-secret"}},"bkapp_id_secret":{"default":{"app_code":"example-app","app_secret":"app-secret"}},"bkdata":{"default":{"token":"data-secret"}},"telemetry":{"default":{"token":"trace-secret","headers":{"Authorization":"header-secret"}}},"consul":{"default":{"token":"acl-secret","username":"example-user","password":"basic-secret"}}}`, "redis:\n  mode: sentinel\nbk_api:\n  address: http://127.0.0.1\n  code: example-app\nbk_data:\n  authentication_method: token\n")
	snapshot, err := Load(raw, runtime)
	require.NoError(t, err)
	require.False(t, runtime.IsSet("redis.password"), "validation must not partially publish")
	snapshot.Apply(runtime)
	require.Equal(t, "", runtime.GetString("redis.password"))
	require.Equal(t, "sentinel-secret", runtime.GetString("redis.sentinel_password"))
	require.Equal(t, "app-secret", runtime.GetString("bk_api.secret"))
	require.Equal(t, "acl-secret", runtime.GetString("consul.token"))
	require.Equal(t, "header-secret", runtime.GetStringMapString("trace.otlp.headers")["Authorization"])
	runtime.Set("trace.otlp.headers", map[string]string{"Authorization": "modified"})
	snapshot.Apply(runtime)
	require.Equal(t, "header-secret", runtime.GetStringMapString("trace.otlp.headers")["Authorization"])
	require.NoError(t, os.Remove(raw.GetString("kms.envelope_file")))
	require.NoError(t, os.Remove(raw.GetString("kms.private_key_file")))
	_, err = snapshot.Reuse(raw, runtime)
	require.NoError(t, err, "reload must not read updated Secret files")
}

func TestSchemaFailures(t *testing.T) {
	for name, payload := range map[string]string{
		"bad JSON":              "{fake-secret",
		"wrong version":         strings.Replace(minimalPayload, `"schema_version":1`, `"schema_version":2`, 1),
		"wrong service":         strings.Replace(minimalPayload, "unify-query", "wrong-service", 1),
		"wrong type":            strings.Replace(minimalPayload, `"redis-secret"`, `123`, 1),
		"unknown key":           strings.TrimSuffix(minimalPayload, "}") + `,"fake-secret":"fake-secret"}`,
		"unknown alias":         strings.Replace(minimalPayload, "default", "other", 1),
		"missing password":      `{"schema_version":1,"service":"unify-query","redis":{"default":{}}}`,
		"missing redis":         `{"schema_version":1,"service":"unify-query"}`,
		"multiple objects":      minimalPayload + `{}`,
		"incomplete app":        strings.TrimSuffix(minimalPayload, "}") + `,"bkapp_id_secret":{"default":{"app_code":"fake-secret"}}}`,
		"incomplete basic auth": strings.TrimSuffix(minimalPayload, "}") + `,"consul":{"default":{"username":"fake-secret"}}}`,
		"bad tls":               strings.TrimSuffix(minimalPayload, "}") + `,"consul":{"default":{"tls_private_key_base64":"fake-secret"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			raw, runtime := configFor(t, payload, "")
			_, err := Load(raw, runtime)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "fake-secret")
			require.False(t, runtime.IsSet("redis.password"))
		})
	}
}

func TestRequiredConsumersAndInputGuards(t *testing.T) {
	for _, extra := range []string{
		"redis:\n  mode: sentinel\n",
		"bk_api:\n  address: http://127.0.0.1\n",
		"bk_data:\n  authentication_method: token\n",
		"trace:\n  enable: true\n",
		"redis:\n  password: fake-secret\n",
		"bk_api:\n  address: http://user:fake-secret@localhost\n",
		"bk_data:\n  address: http://localhost?access_token=fake-secret\n",
		"bk_data:\n  address: http://localhost?auth=fake-secret\n",
		"bk_data:\n  address: http://localhost?key=fake-secret\n",
	} {
		t.Run(extra, func(t *testing.T) {
			raw, runtime := configFor(t, minimalPayload, extra)
			_, err := Load(raw, runtime)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "fake-secret")
		})
	}
	for _, env := range []string{"UNIFY-QUERY_REDIS_PASSWORD", "CONSUL_HTTP_TOKEN", "CONSUL_HTTP_TOKEN_FILE", "CONSUL_HTTP_AUTH", "CONSUL_CLIENT_KEY", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "fake-secret")
			raw, runtime := configFor(t, minimalPayload, "")
			_, err := Load(raw, runtime)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "fake-secret")
		})
	}
}

func TestDisabledAndReservedControl(t *testing.T) {
	v := viper.New()
	v.Set("redis.password", "legacy-secret")
	snapshot, err := Load(v, v)
	require.NoError(t, err)
	snapshot.Apply(v)
	require.Equal(t, "legacy-secret", v.GetString("redis.password"))
	for _, key := range []string{"KMS_ENABLED", "KMS_ENVELOPE_FILE", "KMS_PRIVATE_KEY_FILE"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("UNIFY-QUERY_"+key, "false")
			_, err := Load(v, v)
			require.Error(t, err, "reserved env must be checked before deciding enabled")
		})
	}
}

func TestFileAndDecryptFailures(t *testing.T) {
	for _, field := range []string{"kms.envelope_file", "kms.private_key_file"} {
		t.Run(field, func(t *testing.T) {
			raw, runtime := configFor(t, minimalPayload, "")
			require.NoError(t, os.Remove(raw.GetString(field)))
			_, err := Load(raw, runtime)
			require.Error(t, err)
		})
	}
	raw, runtime := configFor(t, minimalPayload, "")
	require.NoError(t, os.WriteFile(raw.GetString("kms.envelope_file"), []byte("fake-secret"), 0600))
	_, err := Load(raw, runtime)
	require.EqualError(t, err, "kms decrypt failed")
	_, otherKey := kmstest.Files(t, minimalPayload)
	raw, runtime = configFor(t, minimalPayload, "")
	other, err := os.ReadFile(otherKey)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(raw.GetString("kms.private_key_file"), other, 0600))
	_, err = Load(raw, runtime)
	require.EqualError(t, err, "kms decrypt failed")
}

func TestRedactionDoesNotMutateCredentials(t *testing.T) {
	settings := map[string]any{"redis": map[string]any{"password": "fake-secret"}, "consul": map[string]any{"username": "fake-secret"}, "headers": map[string]string{"custom": "fake-secret"}, "url": "https://user:fake-secret@localhost/query?token=fake-secret&auth=fake-secret&key=fake-secret&route=keep", "nested": []any{map[string]any{"private_key_file": "fake-secret"}}}
	out := RedactSettings(settings)
	var encoded bytes.Buffer
	require.NoError(t, json.NewEncoder(&encoded).Encode(out))
	require.NotContains(t, encoded.String(), "fake-secret")
	require.Equal(t, "fake-secret", settings["redis"].(map[string]any)["password"])
	require.Contains(t, out["url"], "route=keep")
}
