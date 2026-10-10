package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/kmstest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestKMSCLIEntrypoints(t *testing.T) {
	viper.Reset()
	mr := miniredis.RunT(t)
	mr.RequireAuth("kms-node-secret")
	dir := t.TempDir()
	configFile, flagsFile, flagFile := filepath.Join(dir, "config.yaml"), filepath.Join(dir, "flags.json"), filepath.Join(dir, "flag.json")
	envelope, key := kmstest.Files(t, `{"schema_version":1,"service":"unify-query","redis":{"default":{"password":"kms-node-secret"}}}`)
	data := fmt.Sprintf("kms:\n  enabled: true\n  envelope_file: %s\n  private_key_file: %s\ntrace:\n  enable: false\nredis:\n  host: %s\n  port: %s\n  kv_base_path: test:kms\nquery:\n  marker: unchanged\n", envelope, key, mr.Host(), mr.Port())
	require.NoError(t, os.WriteFile(configFile, []byte(data), 0600))
	require.NoError(t, os.WriteFile(flagsFile, []byte("{}"), 0600))
	require.NoError(t, os.WriteFile(flagFile, []byte(`{"variations":{"on":true},"defaultRule":{"variation":"on"}}`), 0600))
	previousConfig := config.CustomConfigFilePath
	previousOut, previousErr := rootCmd.OutOrStdout(), rootCmd.ErrOrStderr()
	t.Cleanup(func() {
		config.CustomConfigFilePath = previousConfig
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(previousOut)
		rootCmd.SetErr(previousErr)
		log.SetOutput(nil)
		viper.Reset()
	})
	var output, diagnostics bytes.Buffer
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&diagnostics)
	run := func(args ...string) error {
		t.Helper()
		output.Reset()
		diagnostics.Reset()
		rootCmd.SetArgs(append([]string{"--config", configFile}, args...))
		err := rootCmd.Execute()
		require.NotContains(t, output.String()+diagnostics.String(), "kms-node-secret")
		return err
	}
	require.NoError(t, run("config"))
	require.Contains(t, output.String(), "unchanged")
	require.Contains(t, output.String(), "[redacted]")
	require.Equal(t, "kms-node-secret", viper.GetString("redis.password"), "output must redact a copy")
	require.NoError(t, run("config", "set-feature-flags", "--file", flagsFile))
	require.NoError(t, run("config", "add-feature-flag", "--name", "first", "--file", flagFile))
	require.NoError(t, run("config", "update-feature-flag", "--name", "first", "--file", flagFile))
	require.NoError(t, run("config", "get-feature-flags"))
	require.NoError(t, run("config", "delete-feature-flag", "--name", "first"))
	require.NoError(t, run("config", "reset-feature-flags"))
	// Every entry must fail before starting services or changing Redis.
	require.NoError(t, os.WriteFile(envelope, []byte("invalid-test-envelope"), 0600))
	commands := [][]string{{}, {"config"}, {"config", "set-feature-flags", "--file", flagsFile}, {"config", "reset-feature-flags"}, {"config", "get-feature-flags"}, {"config", "add-feature-flag", "--name", "first", "--file", flagFile}, {"config", "update-feature-flag", "--name", "first", "--file", flagFile}, {"config", "delete-feature-flag", "--name", "first"}}
	for _, args := range commands {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			before := mr.CommandCount()
			err := run(args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), "kms decrypt failed")
			require.Equal(t, before, mr.CommandCount(), "failed init must not contact Redis")
		})
	}
}
