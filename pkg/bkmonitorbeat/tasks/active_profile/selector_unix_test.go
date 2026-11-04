// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build !windows

package active_profile

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// 路径指向 FIFO 或设备文件时必须立即按非普通文件拒绝：阻塞式打开 FIFO 会一直等写端。
func TestReadPIDFileRejectsSpecialFilesWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo.pid")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, "/dev/null"} {
		done := make(chan error, 1)
		go func() {
			_, err := readPIDFile(path)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "not a regular file") {
				t.Fatalf("readPIDFile(%s) error = %v, want a non-regular file error", path, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("readPIDFile(%s) blocked", path)
		}
	}
}
