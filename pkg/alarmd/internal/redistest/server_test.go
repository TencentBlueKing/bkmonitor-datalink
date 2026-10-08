// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package redistest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// recordingTB keeps what Server asked of the test instead of stopping it.
type recordingTB struct {
	testing.TB
	fatal, skip string
}

func (tb *recordingTB) Helper() {}

func (tb *recordingTB) Fatalf(format string, args ...any) { tb.fatal = fmt.Sprintf(format, args...) }

func (tb *recordingTB) Skip(args ...any) { tb.skip = fmt.Sprint(args...) }

func executable(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "redis-server")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The server a test starts: the named one, else the one on PATH; none is a
// skip, or a failure when the gate requires Redis. A name that does not lead
// to an executable fails whether or not Redis is required -- it was asked
// for by name -- and so does a requirement that is neither 1 nor unset, found
// server or not.
func TestServerIsTheNamedOneThenPathAndMissingIsASkipUnlessRequired(t *testing.T) {
	named := executable(t, t.TempDir())
	onPath := t.TempDir()
	onPathServer := executable(t, onPath)
	plain := filepath.Join(t.TempDir(), "redis-server")
	if err := os.WriteFile(plain, []byte("not a program"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	for _, test := range []struct {
		name, server, path, require string
		want                        string
		fatal, skip                 bool
	}{
		{name: "named", server: named, path: empty, want: named},
		{name: "named wins over PATH", server: named, path: onPath, want: named},
		{name: "named but missing", server: filepath.Join(empty, "absent"), path: onPath, fatal: true},
		{name: "named but not executable", server: plain, path: onPath, fatal: true},
		{name: "named but a directory", server: empty, path: onPath, fatal: true},
		{name: "on PATH", path: onPath, want: onPathServer},
		{name: "on PATH while required", path: onPath, require: "1", want: onPathServer},
		{name: "none", path: empty, skip: true},
		{name: "none while required", path: empty, require: "1", fatal: true},
		{name: "none, required set to something else", path: empty, require: "true", fatal: true},
		{name: "on PATH, required set to something else", path: onPath, require: "yes", fatal: true},
		{name: "named, required set to something else", server: named, path: empty, require: "0", fatal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(ServerEnv, test.server)
			t.Setenv(RequireEnv, test.require)
			t.Setenv("PATH", test.path)
			tb := &recordingTB{TB: t}
			got := Server(tb)
			if got != test.want || (tb.fatal != "") != test.fatal || (tb.skip != "") != test.skip {
				t.Fatalf("Server() = %q, fatal %q, skip %q; want %q, fatal %v, skip %v", got, tb.fatal, tb.skip, test.want, test.fatal, test.skip)
			}
		})
	}
}
