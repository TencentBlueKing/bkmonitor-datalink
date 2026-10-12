// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package redistest finds the redis-server the real-Redis tests start.
package redistest

import (
	"os"
	"os/exec"
	"testing"
)

const (
	// ServerEnv names the redis-server to start instead of the one on PATH.
	// The scripts are run on the Redis major the deployments run as well as
	// on the local one, and the two are not the same binary.
	ServerEnv = "ALARMD_REDIS_SERVER"
	// RequireEnv set to 1 fails a test that finds no redis-server rather
	// than skipping it. A gate that skipped every real-Redis test reports the
	// same pass as one that ran them all. Unset or empty requires nothing;
	// any other value fails, because the likely misspellings -- true, yes --
	// would otherwise skip exactly as if it were unset.
	RequireEnv = "ALARMD_REQUIRE_REDIS"
)

// Server is the redis-server executable a test starts: the one ServerEnv
// names, which must then exist, or else the one on PATH. Without either the
// test is skipped, or fails when RequireEnv is 1.
func Server(t testing.TB) string {
	t.Helper()
	require := os.Getenv(RequireEnv)
	if require != "" && require != "1" {
		t.Fatalf("%s=%s: want 1 or unset", RequireEnv, require)
		return ""
	}
	if path := os.Getenv(ServerEnv); path != "" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s=%s: %v", ServerEnv, path, err)
			return ""
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			t.Fatalf("%s=%s is not an executable file", ServerEnv, path)
			return ""
		}
		return path
	}
	path, err := exec.LookPath("redis-server")
	if err == nil {
		return path
	}
	if require == "1" {
		t.Fatalf("redis-server is not installed and %s=1: %v", RequireEnv, err)
		return ""
	}
	t.Skip("redis-server is not installed")
	return ""
}
