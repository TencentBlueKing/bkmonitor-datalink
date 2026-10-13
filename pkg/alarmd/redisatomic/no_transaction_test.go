// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisatomic

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// transactionCalls are the two ways go-redis sends a MULTI/EXEC.
var transactionCalls = []string{"TxPipelined(", ".TxPipeline()"}

// No production code in this module sends a MULTI/EXEC: an atomic write is a
// script through this package, a read of several keys is a plain pipeline
// (see the package comment and the reply-alignment tests for why). Tests may
// still use one - they stand in for another writer, or count a transaction
// as the metrics see it - since a misaligned reply there fails the test
// rather than a deployment.
func TestNoProductionCodeSendsARedisTransaction(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the module root: %v", root, err)
	}
	var found []string
	scanned := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		scanned++
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for line := 1; scanner.Scan(); line++ {
			for _, call := range transactionCalls {
				if strings.Contains(scanner.Text(), call) {
					relative, _ := filepath.Rel(root, path)
					found = append(found, relative+":"+strconv.Itoa(line)+": "+strings.TrimSpace(scanner.Text()))
				}
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 100 {
		t.Fatalf("scanned %d production files under %s, too few to be the module", scanned, root)
	}
	if len(found) > 0 {
		t.Fatalf("production code sends a Redis transaction; make it a redisatomic script, or a plain pipeline for a read:\n%s",
			strings.Join(found, "\n"))
	}
}
