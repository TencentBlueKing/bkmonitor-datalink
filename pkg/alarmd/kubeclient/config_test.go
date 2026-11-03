// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kubeclient

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// client-go's defaults are 5 requests a second with a burst of 10; a config
// that names none gets the generous rate, one that names its own keeps it.
func TestWithRateReplacesOnlyTheUnsetDefault(t *testing.T) {
	base := &rest.Config{Host: "https://192.0.2.1"}
	got := WithRate(base)
	if got.QPS != QPS || got.Burst != Burst {
		t.Fatalf("rate=%v/%d, want %v/%d", got.QPS, got.Burst, float32(QPS), Burst)
	}
	if base.QPS != 0 || base.Burst != 0 {
		t.Fatalf("the caller's config was changed: %v/%d", base.QPS, base.Burst)
	}
	if QPS < 100 || Burst < 2*QPS {
		t.Fatalf("rate %v/%d is not generous", float32(QPS), Burst)
	}

	own := WithRate(&rest.Config{Host: "https://192.0.2.1", QPS: 7, Burst: 9})
	if own.QPS != 7 || own.Burst != 9 {
		t.Fatalf("an explicit rate was replaced: %v/%d", own.QPS, own.Burst)
	}
	limiter := flowcontrol.NewFakeAlwaysRateLimiter()
	paced := WithRate(&rest.Config{Host: "https://192.0.2.1", RateLimiter: limiter})
	if paced.RateLimiter != limiter || paced.QPS != 0 {
		t.Fatalf("an explicit limiter was replaced: %v/%v", paced.RateLimiter, paced.QPS)
	}
}

// Every Kubernetes client alarmd builds starts from InCluster or WithRate: a
// configuration taken from rest.InClusterConfig directly would carry
// client-go's 5 a second again, a gate nobody chose.
func TestEveryInClusterConfigGoesThroughThisPackage(t *testing.T) {
	root := ".."
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Dir(path) == filepath.Join(root, "kubeclient") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), "rest.InClusterConfig(") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("rest.InClusterConfig used outside kubeclient: %v", found)
	}
}
