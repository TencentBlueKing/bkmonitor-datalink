// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build !windows

package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeExecutableTestScript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool.sh")
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLLVMSymbolizerParsesInlineJSON(t *testing.T) {
	tool := writeExecutableTestScript(t, `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' '{"Address":"0x40038d","ModuleName":"/tmp/cpu-test","Symbol":[{"FunctionName":"heavy_math_work","FileName":"/src/test.c","Line":14,"StartLine":10},{"FunctionName":"main","FileName":"/src/test.c","Line":57,"StartLine":55}]}'
done
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	symbolizer, err := newLLVMSymbolizer(ctx, tool)
	if err != nil {
		t.Fatal(err)
	}
	defer symbolizer.Close()

	frames, err := symbolizer.Symbolize("/tmp/cpu-test", 0x40038d)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[0].Function != "heavy_math_work" || frames[1].Function != "main" {
		t.Fatalf("unexpected frames: %+v", frames)
	}
}

func TestSymbolizerProcessRoundTrip(t *testing.T) {
	tool := writeExecutableTestScript(t, `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line"
done
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	process, err := startSymbolizerProcess(ctx, tool, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer process.close()
	if err := process.writeLine("hello"); err != nil {
		t.Fatal(err)
	}
	line, err := process.readLine()
	if err != nil {
		t.Fatal(err)
	}
	if line != "hello" {
		t.Fatalf("line = %q", line)
	}
}

func TestSafeSymbolizerEnvironmentDoesNotForwardBeatSecretsOrLoaderOptions(t *testing.T) {
	t.Setenv("BKMONITOR_TEST_SECRET", "must-not-leak")
	t.Setenv("LD_PRELOAD", "/tmp/untrusted.so")
	t.Setenv("LLVM_SYMBOLIZER_OPTS", "--functions=none")
	t.Setenv("DEBUGINFOD_URLS", "https://example.invalid")

	environment := strings.Join(safeSymbolizerEnvironment(), "\n")
	for _, forbidden := range []string{
		"BKMONITOR_TEST_SECRET=",
		"LD_PRELOAD=",
		"LLVM_SYMBOLIZER_OPTS=",
		"https://example.invalid",
	} {
		if strings.Contains(environment, forbidden) {
			t.Fatalf("safe environment contains %q: %s", forbidden, environment)
		}
	}
	if !strings.Contains(environment, "DEBUGINFOD_URLS=") ||
		!strings.Contains(environment, "LC_ALL=C") {
		t.Fatalf("safe environment is missing fixed settings: %s", environment)
	}
}

// trustedTestDir 返回祖先目录都不能被其他账号写入的临时目录。Linux 上 t.TempDir()
// 位于 /tmp，信任检查会拒绝其中的可执行文件。
func trustedTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".trusted-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	absolute, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func TestResolveTrustedExternalCommandRejectsGroupOrWorldWritableExecutable(t *testing.T) {
	tool := filepath.Join(trustedTestDir(t), "tool.sh")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if resolved := resolveTrustedExternalCommand(tool); resolved == "" {
		t.Fatal("current-user-owned executable should be trusted")
	}
	if err := os.Chmod(tool, 0o777); err != nil {
		t.Fatal(err)
	}
	if resolved := resolveTrustedExternalCommand(tool); resolved != "" {
		t.Fatalf("writable executable was trusted as %q", resolved)
	}
}

func TestDiscoverSymbolizerFindsVersionSuffixedLLVM(t *testing.T) {
	directory := trustedTestDir(t)
	tool := filepath.Join(directory, "llvm-symbolizer-17")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	options := discoverSymbolizerOptions()
	resolved, err := filepath.EvalSymlinks(tool)
	if err != nil {
		t.Fatal(err)
	}
	if options.LLVMPath != resolved {
		t.Fatalf("LLVM path = %q, want %q", options.LLVMPath, resolved)
	}
	if options.Addr2linePath != "" {
		t.Fatalf("unexpected addr2line path %q", options.Addr2linePath)
	}
}

func TestAutoSymbolizerFallsBackFromLLVMToAddr2line(t *testing.T) {
	invalidLLVM := writeExecutableTestScript(t, `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' 'not-json'
done
`)
	addr2line := writeExecutableTestScript(t, `#!/bin/sh
while IFS= read -r address; do
  printf '0x%s\n' "$address"
  if [ "$address" = "ffffffffffffffff" ]; then
    printf '%s\n' '??' '??:0'
  else
    printf '%s\n' 'inline_leaf' '/src/test.c:12' 'main' '/src/test.c:20'
  fi
done
`)
	symbolizer := newAutoSymbolizer(context.Background(), symbolizerOptions{
		LLVMPath:         invalidLLVM,
		Addr2linePath:    addr2line,
		Timeout:          5 * time.Second,
		MaxAddr2lineDSOs: 1,
	})
	defer symbolizer.Close()

	frames, err := symbolizer.Symbolize("/tmp/cpu-test", 0x40038d)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[0].Function != "inline_leaf" || frames[1].Function != "main" {
		t.Fatalf("unexpected fallback frames: %+v", frames)
	}
	if symbolizer.Backend() != "llvm->addr2line" {
		t.Fatalf("backend = %q", symbolizer.Backend())
	}
	if !strings.Contains(symbolizer.LastError(), "parse llvm-symbolizer JSON") {
		t.Fatalf("last error = %q", symbolizer.LastError())
	}
}

func TestAutoSymbolizerUsesAddr2lineForDSOPathWithWhitespace(t *testing.T) {
	llvm := writeExecutableTestScript(t, `#!/bin/sh
exit 99
`)
	addr2line := writeExecutableTestScript(t, `#!/bin/sh
while IFS= read -r address; do
  printf '0x%s\n' "$address"
  if [ "$address" = "ffffffffffffffff" ]; then
    printf '%s\n' '??' '??:0'
  else
    printf '%s\n' 'main' '/src/test.c:20'
  fi
done
`)
	symbolizer := newAutoSymbolizer(context.Background(), symbolizerOptions{
		LLVMPath:         llvm,
		Addr2linePath:    addr2line,
		Timeout:          5 * time.Second,
		MaxAddr2lineDSOs: 1,
	})
	defer symbolizer.Close()

	frames, err := symbolizer.Symbolize("/tmp/cpu test", 0x40038d)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0].Function != "main" {
		t.Fatalf("unexpected fallback frames: %+v", frames)
	}
	if symbolizer.Backend() != "llvm->addr2line" {
		t.Fatalf("backend = %q", symbolizer.Backend())
	}
}

func TestSymbolizerTimeoutReturnsWithoutHanging(t *testing.T) {
	tool := writeExecutableTestScript(t, `#!/bin/sh
while IFS= read -r line; do
  /bin/sleep 10
done
`)
	symbolizer := newAutoSymbolizer(context.Background(), symbolizerOptions{
		LLVMPath: tool,
		Timeout:  50 * time.Millisecond,
	})
	defer symbolizer.Close()

	started := time.Now()
	if _, err := symbolizer.Symbolize("/tmp/cpu-test", 0x40038d); err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("symbolizer timeout took too long: %s", elapsed)
	}
}

func TestAddr2lineDSOProcessLimit(t *testing.T) {
	tool := writeExecutableTestScript(t, `#!/bin/sh
while IFS= read -r address; do
  printf '0x%s\n' "$address"
  printf '%s\n' 'main' '/src/test.c:20'
done
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	symbolizer := newAddr2lineSymbolizer(ctx, tool, 1)
	defer symbolizer.Close()

	if _, err := symbolizer.Symbolize("/tmp/one", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := symbolizer.Symbolize("/tmp/two", 2); !errors.Is(err, errSymbolizerDSOLimit) {
		t.Fatalf("second DSO error = %v", err)
	}
}
