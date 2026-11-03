// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestFeatureFlagCRUDCommands(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("test-secret")
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "unify-query.yaml")
	flagFile := filepath.Join(tempDir, "flag.json")
	configData := fmt.Sprintf("logger:\n  level: debug\nredis:\n  host: %s\n  port: %s\n  password: test-secret\n  database: 3\n  kv_base_path: test:crud\n", mr.Host(), mr.Port())
	if err := os.WriteFile(configFile, []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
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
	run := func(args ...string) (string, error) {
		t.Helper()
		output.Reset()
		diagnostics.Reset()
		rootCmd.SetArgs(append([]string{"--config", configFile, "config"}, args...))
		err := rootCmd.Execute()
		if strings.Contains(output.String()+diagnostics.String(), "test-secret") {
			t.Fatal("command leaked Redis credentials")
		}
		return output.String(), err
	}
	const key = "test:crud:data:feature_flag"
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr(), Password: "test-secret"})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	subscription := client.Subscribe(ctx, key+":feature_flag_channel")
	defer subscription.Close()
	if _, err := subscription.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := run("get-feature-flags"); err == nil || mr.DB(3).Exists(key) {
		t.Fatal("query of missing Redis snapshot must fail without creating it")
	}
	if err := os.WriteFile(flagFile, []byte(`{"variations":{"enabled":true},"defaultRule":{"variation":"enabled"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("add-feature-flag", "--name", "first", "--file", flagFile); err == nil || mr.DB(3).Exists(key) {
		t.Fatal("single-flag add must not intercept a pending Consul migration")
	}
	if err := os.WriteFile(flagFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("set-feature-flags", "--file", flagFile); err != nil {
		t.Fatal(err)
	}
	if _, err := subscription.ReceiveMessage(ctx); err != nil {
		t.Fatal(err)
	}
	write := func(action, name, definition string) {
		t.Helper()
		args := []string{action + "-feature-flag", "--name", name}
		if action != "delete" {
			if err := os.WriteFile(flagFile, []byte(definition), 0o600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--file", flagFile)
		}
		if _, err := run(args...); err != nil {
			t.Fatal(err)
		}
		if _, err := subscription.ReceiveMessage(ctx); err != nil {
			t.Fatalf("missing change notification: %v", err)
		}
		if mr.DB(3).TTL(key) != 0 || mr.DB(0).Exists(key) {
			t.Fatal("mutation must use the configured DB without TTL")
		}
	}
	const enabled = `{"variations":{"enabled":true,"disabled":false},"defaultRule":{"variation":"enabled"}}`
	const disabled = `{"variations":{"enabled":true,"disabled":false},"defaultRule":{"variation":"disabled"}}`
	const legacy = `{"rule":"key eq \"test-user\"","percentage":100,"true":false,"false":false,"default":false}`
	write("add", "first", enabled)
	write("add", "second", legacy)
	data, err := run("get-feature-flags")
	var snapshot map[string]json.RawMessage
	if err != nil || json.Unmarshal([]byte(data), &snapshot) != nil || len(snapshot) != 2 {
		t.Fatalf("expected JSON-only snapshot with both flags, got %q, error: %v", data, err)
	}
	data, err = run("get-feature-flags", "--name", "first")
	if err != nil || strings.TrimSpace(data) != enabled {
		t.Fatalf("expected exportable single flag definition, got %q, error: %v", data, err)
	}
	for _, args := range [][]string{
		{"add-feature-flag", "--name", "first", "--file", flagFile},
		{"update-feature-flag", "--name", "missing", "--file", flagFile},
		{"delete-feature-flag", "--name", "missing"},
	} {
		before, _ := mr.DB(3).Get(key)
		if _, err := run(args...); err == nil {
			t.Fatalf("expected failure for %v", args)
		}
		if after, _ := mr.DB(3).Get(key); after != before {
			t.Fatalf("failed command changed snapshot: %v", args)
		}
	}
	write("update", "first", disabled)
	data, err = run("get-feature-flags", "--name", "first")
	if err != nil || strings.TrimSpace(data) != disabled {
		t.Fatalf("updated flag was not applied: %q, error: %v", data, err)
	}
	data, err = run("get-feature-flags", "--name", "second")
	if err != nil || strings.TrimSpace(data) != legacy {
		t.Fatalf("another flag was changed: %q, error: %v", data, err)
	}
	write("delete", "first", "")
	if _, err := run("get-feature-flags", "--name", "first"); err == nil {
		t.Fatal("deleted flag must not be returned")
	}
	write("delete", "second", "")
	if data, err := mr.DB(3).Get(key); err != nil || data != "{}" {
		t.Fatalf("deleting the last flag must keep an empty snapshot, got %q, error: %v", data, err)
	}
}
