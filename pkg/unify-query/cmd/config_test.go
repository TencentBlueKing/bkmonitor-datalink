// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestSetFeatureFlagsCommand(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("test-secret")
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "unify-query.yaml")
	flagsFile := filepath.Join(tempDir, "flags.json")
	snapshot := `{"new-query":{"variations":{"enabled":true,"disabled":false},"defaultRule":{"variation":"disabled"}}}`
	writeFile := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(configFile, fmt.Sprintf("redis:\n  host: %s\n  port: %s\n  password: test-secret\n  database: 3\n  kv_base_path: test:unify-query\n", mr.Host(), mr.Port()))
	writeFile(flagsFile, snapshot)
	previousConfig := config.CustomConfigFilePath
	previousOut := rootCmd.OutOrStdout()
	previousErr := rootCmd.ErrOrStderr()
	t.Cleanup(func() {
		config.CustomConfigFilePath = previousConfig
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(previousOut)
		rootCmd.SetErr(previousErr)
		log.SetOutput(nil)
		viper.Reset()
	})
	var output bytes.Buffer
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&output)

	const key = "test:unify-query:data:feature_flag"
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr(), Password: "test-secret"})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	subscription := client.Subscribe(ctx, key+":feature_flag_channel")
	defer subscription.Close()
	if _, err := subscription.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	rootCmd.SetArgs([]string{"--config", configFile, "config", "set-feature-flags", "--file", flagsFile})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if actual, err := mr.DB(3).Get(key); err != nil || actual != snapshot {
		t.Fatalf("wrong Redis snapshot: %q, error: %v", actual, err)
	}
	if mr.DB(0).Exists(key) {
		t.Fatal("snapshot was written to the wrong database")
	}
	if message, err := subscription.ReceiveMessage(ctx); err != nil || message.Payload != snapshot {
		t.Fatalf("missing change notification: %v, error: %v", message, err)
	}
	if strings.Contains(output.String(), "test-secret") || strings.Contains(output.String(), snapshot) {
		t.Fatal("command printed credentials or snapshot contents")
	}

	for _, test := range []struct {
		name, data, configPath, configData, wantError string
	}{
		{name: "invalid JSON", data: "{broken", configPath: configFile, wantError: "invalid feature flag JSON"},
		{name: "invalid flag", data: `{"new-query":{}}`, configPath: configFile, wantError: "invalid feature flag"},
		{name: "missing default rule", data: `{"new-query":{"variations":{"enabled":true}}}`, configPath: configFile, wantError: "invalid feature flag"},
		{name: "missing config", data: "{}", configPath: filepath.Join(tempDir, "missing.yaml"), wantError: "load config"},
		{name: "invalid config", data: "{}", configPath: configFile, configData: "redis: [", wantError: "load config"},
		{name: "empty prefix", data: "{}", configPath: configFile, configData: "redis:\n  kv_base_path: ''\n", wantError: "redis.kv_base_path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeFile(flagsFile, test.data)
			if test.configData != "" {
				writeFile(configFile, test.configData)
			}
			config.CustomConfigFilePath = test.configPath
			cmd := newSetFeatureFlagsCmd()
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{"--file", flagsFile})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected error %q, got %v", test.wantError, err)
			}
			if actual, err := mr.DB(3).Get(key); err != nil || actual != snapshot {
				t.Fatalf("failed command changed the existing snapshot: %q, error: %v", actual, err)
			}
		})
	}
}

func TestResetFeatureFlagsCommand(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("test-secret")
	configFile := filepath.Join(t.TempDir(), "unify-query.yaml")
	configData := fmt.Sprintf("redis:\n  host: %s\n  port: %s\n  password: test-secret\n  database: 3\n  kv_base_path: test:unify-query\n", mr.Host(), mr.Port())
	if err := os.WriteFile(configFile, []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	previousConfig := config.CustomConfigFilePath
	previousOut := rootCmd.OutOrStdout()
	previousErr := rootCmd.ErrOrStderr()
	t.Cleanup(func() {
		config.CustomConfigFilePath = previousConfig
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(previousOut)
		rootCmd.SetErr(previousErr)
		log.SetOutput(nil)
		viper.Reset()
	})
	var output bytes.Buffer
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&output)

	const key = "test:unify-query:data:feature_flag"
	mr.DB(3).Set(key, "old snapshot")
	mr.DB(0).Set(key, "other database")
	mr.DB(3).Set("test:unify-query:data:storage:1", "other config")
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr(), Password: "test-secret"})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	subscription := client.Subscribe(ctx, key+":feature_flag_channel")
	defer subscription.Close()
	if _, err := subscription.Receive(ctx); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"--config", configFile, "config", "reset-feature-flags"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if mr.DB(3).Exists(key) {
		t.Fatal("reset must delete the configured Redis snapshot")
	}
	if !mr.DB(0).Exists(key) || !mr.DB(3).Exists("test:unify-query:data:storage:1") {
		t.Fatal("reset must not delete other databases or storage config")
	}
	if _, err := subscription.ReceiveMessage(ctx); err != nil {
		t.Fatalf("missing refresh notification: %v", err)
	}
	if !strings.Contains(output.String(), "snapshot deleted") || strings.Contains(output.String(), "test-secret") {
		t.Fatalf("unexpected command output: %q", output.String())
	}
}
