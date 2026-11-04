// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pprofprofile "github.com/google/pprof/profile"
)

type fakeInlineSymbolizer struct {
	frames []symbolizedFrame
	err    error
	calls  int
}

func (s *fakeInlineSymbolizer) Available() bool { return true }

func (s *fakeInlineSymbolizer) Backend() string { return "fake" }

func (s *fakeInlineSymbolizer) LastError() string {
	if s.err == nil {
		return ""
	}
	return s.err.Error()
}

func (s *fakeInlineSymbolizer) Symbolize(_ string, _ uint64) ([]symbolizedFrame, error) {
	s.calls++
	return s.frames, s.err
}

func (s *fakeInlineSymbolizer) Close() error { return nil }

type panicReader struct{}

type unavailableInlineSymbolizer struct{}

func (unavailableInlineSymbolizer) Available() bool { return false }

func (unavailableInlineSymbolizer) Backend() string { return "basic" }

func (unavailableInlineSymbolizer) LastError() string {
	return "native symbolizer is unavailable"
}

func (unavailableInlineSymbolizer) Symbolize(string, uint64) ([]symbolizedFrame, error) {
	return nil, errors.New("must not be called")
}

func (unavailableInlineSymbolizer) Close() error { return nil }

func (panicReader) Read(_ []byte) (int, error) {
	panic("broken perf stream")
}

var _ io.Reader = panicReader{}

func TestConvertPerfScript(t *testing.T) {
	script := `perf-busy 123 1.000: 10101010 cpu-clock:
                  40040f busy3+0xf (/tmp/perf-busy) 0x40f
                  400439 busy2+0x9 (/tmp/perf-busy) 0x439
                  400449 busy1+0x9 (/tmp/perf-busy) 0x449

perf-busy 123 1.010: 10101010 cpu-clock:
                  40040f busy3+0xf (/tmp/perf-busy) 0x40f
                  400439 busy2+0x9 (/tmp/perf-busy) 0x439
                  400449 busy1+0x9 (/tmp/perf-busy) 0x449
`
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "perf.script")
	outputPath := filepath.Join(dir, "profile.pprof")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	conf := validPerfConfig()
	conf.DurationSec = 1
	stats, err := convertPerfScript(scriptPath, outputPath, conf)
	if err != nil {
		t.Fatalf("convertPerfScript() error = %v", err)
	}
	if stats.Samples != 2 {
		t.Fatalf("samples = %d, want 2", stats.Samples)
	}
	if stats.TotalFrames != 6 || stats.UnknownFrames != 0 {
		t.Fatalf("frames total=%d unknown=%d", stats.TotalFrames, stats.UnknownFrames)
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := pprofprofile.ParseData(raw)
	if err != nil {
		t.Fatalf("parse generated pprof: %v", err)
	}
	if len(profile.Sample) != 1 {
		t.Fatalf("aggregated samples = %d, want 1", len(profile.Sample))
	}
	if profile.Sample[0].Value[0] != 2 {
		t.Fatalf("sample count = %d, want 2", profile.Sample[0].Value[0])
	}
	if got := profile.Sample[0].Label["comm"]; len(got) != 1 || got[0] != "perf-busy" {
		t.Fatalf("comm label = %v", got)
	}
	if got := profile.Sample[0].Label["pid"]; len(got) != 1 || got[0] != "123" {
		t.Fatalf("pid label = %v", got)
	}
	// 执行定位标签：业务、执行时间、主机、执行时 IP 与计划任务；立即执行必须没有
	// task_id。ip 是主机地址，不是采集器 bind 地址。
	for key, want := range map[string]string{
		"bk_biz_id":    "11",
		"scheduled_at": "1786521405518",
		"bk_host_id":   "456",
		"task_id":      "1001",
		"ip":           "192.0.2.10",
	} {
		if got := profile.Sample[0].Label[key]; len(got) != 1 || got[0] != want {
			t.Fatalf("%s label = %v, want %q", key, got, want)
		}
	}
	if _, exists := profile.Sample[0].Label["profile_id"]; exists {
		t.Fatal("pprof samples must not carry profile_id")
	}

	names := make(map[string]bool)
	for _, function := range profile.Function {
		names[function.Name] = true
	}
	for _, expected := range []string{"busy1", "busy2", "busy3"} {
		if !names[expected] {
			t.Fatalf("missing function %s", expected)
		}
	}
}

func TestParseSampleIdentity(t *testing.T) {
	comm, pid := parseSampleIdentity("security agent 4321 10.500: 101 cpu-clock:")
	if comm != "security agent" || pid != 4321 {
		t.Fatalf("identity comm=%q pid=%d", comm, pid)
	}
}

func TestParseFrameUnknownWithDSOOffset(t *testing.T) {
	frame, ok := parseFrame("  7f00 [unknown] (/usr/lib64/libc.so.6+0x1234)", offsetModeDSOFF)
	if !ok {
		t.Fatal("parseFrame returned false")
	}
	if frame.IP != 0x7f00 ||
		frame.DSO != "/usr/lib64/libc.so.6" ||
		frame.DSOOffset != 0x1234 ||
		!frame.HasDSOOffset {
		t.Fatalf("unexpected frame: %+v", frame)
	}
}

// TestParseFrameLegacyOffsetMode 覆盖 perf < 6.4 的输出：没有 dsoff 字段，callchain
// 第一列对非内核 DSO 本身就是文件偏移。
func TestParseFrameLegacyOffsetMode(t *testing.T) {
	t.Run("userspace frame uses the first column as a file offset", func(t *testing.T) {
		frame, ok := parseFrame("  235d3 memset+0x53 (/usr/lib64/ld-2.31.so)", offsetModeLegacyIP)
		if !ok {
			t.Fatal("parseFrame returned false")
		}
		if !frame.HasDSOOffset || frame.DSOOffset != 0x235d3 || frame.IP != 0x235d3 {
			t.Fatalf("unexpected frame: %+v", frame)
		}
	})

	t.Run("kernel frame keeps the raw ip", func(t *testing.T) {
		frame, ok := parseFrame(
			"  ffffffff81234567 native_write_msr+0x7 ([kernel.kallsyms])",
			offsetModeLegacyIP,
		)
		if !ok {
			t.Fatal("parseFrame returned false")
		}
		if frame.HasDSOOffset {
			t.Fatalf("kernel frames must not be treated as file offsets: %+v", frame)
		}
	})

	// 同一行在 dsoff 模式下不应该被当成偏移：那时第一列是 raw ip。
	t.Run("dsoff mode keeps the first column as an address", func(t *testing.T) {
		frame, ok := parseFrame("  235d3 memset+0x53 (/usr/lib64/ld-2.31.so)", offsetModeDSOFF)
		if !ok {
			t.Fatal("parseFrame returned false")
		}
		if frame.HasDSOOffset {
			t.Fatalf("dsoff mode must not synthesize an offset: %+v", frame)
		}
	})
}

func TestScriptFieldCandidatesCoverLegacyPerf(t *testing.T) {
	if len(scriptFieldCandidates) < 2 {
		t.Fatal("a legacy candidate is required to support perf < 6.4")
	}
	first := scriptFieldCandidates[0]
	if !strings.Contains(first.Fields, "dsoff") || first.Mode != offsetModeDSOFF {
		t.Fatalf("the preferred candidate must request dsoff: %+v", first)
	}
	for _, candidate := range scriptFieldCandidates[1:] {
		if strings.Contains(candidate.Fields, "dsoff") {
			t.Fatalf("fallback candidates must not request dsoff: %+v", candidate)
		}
		if candidate.Mode != offsetModeLegacyIP {
			t.Fatalf("fallback candidates must read the first column as an offset: %+v", candidate)
		}
		// 覆盖到 4.x：period 在这些版本上可用，保留它以获得准确的样本权重。
		if !strings.Contains(candidate.Fields, "period") {
			t.Fatalf("fallback candidates should still request period: %+v", candidate)
		}
	}
}

func TestConvertPerfScriptReaderRecoversPanic(t *testing.T) {
	_, err := convertPerfScriptReaderSafely(
		panicReader{},
		filepath.Join(t.TempDir(), "profile.pprof"),
		validPerfConfig(),
	)
	if err == nil || !strings.Contains(err.Error(), "broken perf stream") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProfileBuilderAddsAndCachesInlineFrames(t *testing.T) {
	symbolizer := &fakeInlineSymbolizer{frames: []symbolizedFrame{
		{Function: "heavy_math_work", File: "/src/test.c", Line: 14, StartLine: 10},
		{Function: "run_pipeline_alpha", File: "/src/test.c", Line: 43, StartLine: 40},
		{Function: "main", File: "/src/test.c", Line: 57, StartLine: 55},
	}}
	builder := newProfileBuilderWithSymbolizer(validPerfConfig(), symbolizer)
	defer builder.close()
	builder.resolveAddress = func(frame perfFrame) (uint64, bool) { return frame.IP, true }

	sample := perfSample{
		Period: 10,
		Comm:   "cpu-test",
		PID:    123,
		Frames: []perfFrame{{IP: 0x40038d, DSO: "/tmp/cpu-test", Symbol: "main"}},
	}
	builder.addSample(sample)
	builder.addSample(sample)

	if symbolizer.calls != 1 {
		t.Fatalf("symbolizer calls = %d, want 1", symbolizer.calls)
	}
	if builder.symbolizedLocations != 1 || builder.inlineLocations != 1 {
		t.Fatalf("symbolized=%d inline=%d", builder.symbolizedLocations, builder.inlineLocations)
	}
	if got := builder.symbolizationStatus(); got != symbolizationComplete {
		t.Fatalf("symbolization status = %q, want %q", got, symbolizationComplete)
	}
	if len(builder.profile.Location) != 1 {
		t.Fatalf("locations = %d, want 1", len(builder.profile.Location))
	}
	location := builder.profile.Location[0]
	if len(location.Line) != 3 {
		t.Fatalf("inline lines = %d, want 3", len(location.Line))
	}
	for index, expected := range []string{"heavy_math_work", "run_pipeline_alpha", "main"} {
		if location.Line[index].Function.Name != expected {
			t.Fatalf("line[%d] function = %q, want %q", index, location.Line[index].Function.Name, expected)
		}
	}
	if !location.Mapping.HasInlineFrames || !location.Mapping.HasFilenames || !location.Mapping.HasLineNumbers {
		t.Fatalf("mapping symbol flags were not populated: %+v", location.Mapping)
	}
	if len(builder.profile.Sample) != 1 || builder.profile.Sample[0].Value[0] != 2 {
		t.Fatalf("duplicate samples were not aggregated: %+v", builder.profile.Sample)
	}
}

func TestProfileBuilderSymbolizationLimitFallsBackToPerfSymbol(t *testing.T) {
	symbolizer := &fakeInlineSymbolizer{frames: []symbolizedFrame{{Function: "resolved"}}}
	builder := newProfileBuilderWithSymbolizer(validPerfConfig(), symbolizer)
	defer builder.close()
	builder.resolveAddress = func(frame perfFrame) (uint64, bool) { return frame.IP, true }
	builder.maxSymbolizedLocations = 1

	builder.addSample(perfSample{
		Period: 10,
		Comm:   "cpu-test",
		PID:    123,
		Frames: []perfFrame{
			{IP: 0x1, DSO: "/tmp/cpu-test", Symbol: "first"},
			{IP: 0x2, DSO: "/tmp/cpu-test", Symbol: "second"},
		},
	})

	if symbolizer.calls != 1 || builder.symbolizationSkipped != 1 {
		t.Fatalf("calls=%d skipped=%d", symbolizer.calls, builder.symbolizationSkipped)
	}
	if got := builder.symbolizationStatus(); got != symbolizationDegraded {
		t.Fatalf("symbolization status = %q, want %q", got, symbolizationDegraded)
	}
	if message := builder.symbolizationError(); !strings.Contains(message, "unique-location limit") {
		t.Fatalf("symbolization message = %q", message)
	}
	if got := builder.profile.Location[1].Line[0].Function.Name; got != "second" {
		t.Fatalf("fallback function = %q, want second", got)
	}
}

func TestProfileBuilderReportsDisabledAndUnavailableSymbolization(t *testing.T) {
	conf := validPerfConfig()
	disabled := newProfileBuilderWithSymbolizer(conf, nil)
	if got := disabled.symbolizationStatus(); got != symbolizationDisabled {
		t.Fatalf("disabled status = %q", got)
	}
	disabled.close()

	unavailable := newProfileBuilderWithSymbolizer(conf, unavailableInlineSymbolizer{})
	unavailable.addSample(perfSample{
		Period: 10,
		Comm:   "cpu-test",
		PID:    123,
		Frames: []perfFrame{{IP: 0x1, DSO: "/tmp/cpu-test", Symbol: "main"}},
	})
	if got := unavailable.symbolizationStatus(); got != symbolizationUnavailable {
		t.Fatalf("unavailable status = %q", got)
	}
	if unavailable.symbolizationSkipped != 1 {
		t.Fatalf("unavailable skipped = %d, want 1", unavailable.symbolizationSkipped)
	}
	unavailable.close()
}

func TestProfileBuilderSymbolizationByteLimitFallsBackToPerfSymbol(t *testing.T) {
	symbolizer := &fakeInlineSymbolizer{frames: []symbolizedFrame{{
		Function: strings.Repeat("resolved", 32),
		File:     "/src/test.c",
	}}}
	builder := newProfileBuilderWithSymbolizer(validPerfConfig(), symbolizer)
	defer builder.close()
	builder.resolveAddress = func(frame perfFrame) (uint64, bool) { return frame.IP, true }
	builder.maxSymbolizedBytes = 1

	builder.addSample(perfSample{
		Period: 10,
		Comm:   "cpu-test",
		PID:    123,
		Frames: []perfFrame{{IP: 0x1, DSO: "/tmp/cpu-test", Symbol: "main"}},
	})

	if symbolizer.calls != 1 || builder.symbolizationSkipped != 1 || builder.symbolizedBytes != 0 {
		t.Fatalf(
			"calls=%d skipped=%d bytes=%d",
			symbolizer.calls,
			builder.symbolizationSkipped,
			builder.symbolizedBytes,
		)
	}
	if got := builder.profile.Location[0].Line[0].Function.Name; got != "main" {
		t.Fatalf("fallback function = %q, want main", got)
	}
}

func TestNormalizeSymbolizedFramesBoundsInlineDepthAndKeepsOutermost(t *testing.T) {
	input := make([]symbolizedFrame, maxInlineFramesPerLocation+10)
	for index := range input {
		input[index] = symbolizedFrame{Function: fmt.Sprintf("frame-%d", index)}
	}

	frames := normalizeSymbolizedFrames(input)
	if len(frames) != maxInlineFramesPerLocation {
		t.Fatalf("frames = %d, want %d", len(frames), maxInlineFramesPerLocation)
	}
	if frames[0].Function != "frame-0" {
		t.Fatalf("leaf frame = %q", frames[0].Function)
	}
	wantOutermost := fmt.Sprintf("frame-%d", len(input)-1)
	if frames[len(frames)-1].Function != wantOutermost {
		t.Fatalf("outermost frame = %q, want %q", frames[len(frames)-1].Function, wantOutermost)
	}
}

func TestConvertPerfScriptContextCancelledStopsConversion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	script := `cpu-test 123 1.000: 10 cpu-clock:
                  40040f main+0xf (/missing/cpu-test) 0x40f

`
	conf := validPerfConfig()
	outputPath := filepath.Join(t.TempDir(), "profile.pprof")
	_, err := convertPerfScriptReaderSafelyContext(
		ctx,
		strings.NewReader(script),
		outputPath,
		conf,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("conversion error = %v, want context cancellation", err)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("cancelled conversion wrote a profile: %v", statErr)
	}
}

func TestDefaultProfileBuildLimits(t *testing.T) {
	limits := defaultProfileBuildLimits()
	if limits.MaxFramesPerSample != 1_024 ||
		limits.MaxStringBytes != 4_096 ||
		limits.MaxMappings != 8_192 ||
		limits.MaxLocations != 1_000_000 ||
		limits.MaxFunctions != 1_000_000 ||
		limits.MaxUniqueSamples != 500_000 ||
		limits.MaxUniqueSampleFrameRefs != 8_000_000 {
		t.Fatalf("unexpected default profile build limits: %+v", limits)
	}
}

func TestProfileConversionLimitsFailBeforeAggregateInsertion(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		limits   profileBuildLimits
		resource string
	}{
		{
			name: "stack depth",
			script: `cpu 1 1.000: 10 cpu-clock:
  1 first (/tmp/a)
  2 second (/tmp/a)

`,
			limits:   profileBuildLimits{MaxFramesPerSample: 1},
			resource: "sample_stack_depth",
		},
		{
			name: "comm string",
			script: `long-command 1 1.000: 10 cpu-clock:
  1 first (/tmp/a)

`,
			limits:   profileBuildLimits{MaxStringBytes: 4},
			resource: "comm_string_bytes",
		},
		{
			name: "dso string",
			script: `cpu 1 1.000: 10 cpu-clock:
  1 first (/tmp/long-dso)

`,
			limits:   profileBuildLimits{MaxStringBytes: 4},
			resource: "dso_string_bytes",
		},
		{
			name: "symbol string",
			script: `cpu 1 1.000: 10 cpu-clock:
  1 long-symbol (/a)

`,
			limits:   profileBuildLimits{MaxStringBytes: 4},
			resource: "symbol_string_bytes",
		},
		{
			name: "mappings",
			script: `cpu 1 1.000: 10 cpu-clock:
  1 first (/tmp/a)
  2 second (/tmp/b)

`,
			limits:   profileBuildLimits{MaxMappings: 1},
			resource: "mappings",
		},
		{
			name: "locations",
			script: `cpu 1 1.000: 10 cpu-clock:
  1 first (/tmp/a)
  2 second (/tmp/a)

`,
			limits:   profileBuildLimits{MaxLocations: 1},
			resource: "locations",
		},
		{
			name: "functions",
			script: `cpu 1 1.000: 10 cpu-clock:
  1 first (/tmp/a)
  2 second (/tmp/a)

`,
			limits:   profileBuildLimits{MaxFunctions: 1},
			resource: "functions",
		},
		{
			name: "unique samples",
			script: `cpu 1 1.000: 10 cpu-clock:
  1 first (/tmp/a)

cpu 2 1.100: 10 cpu-clock:
  1 first (/tmp/a)

`,
			limits:   profileBuildLimits{MaxUniqueSamples: 1},
			resource: "unique_samples",
		},
		{
			name: "unique sample frame references",
			script: `cpu 1 1.000: 10 cpu-clock:
  1 first (/tmp/a)
  2 second (/tmp/a)

cpu 2 1.100: 10 cpu-clock:
  1 first (/tmp/a)
  2 second (/tmp/a)

`,
			limits:   profileBuildLimits{MaxUniqueSampleFrameRefs: 2},
			resource: "unique_sample_frame_refs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conf := validPerfConfig()
			conf.InlineSymbolization = inlineSymbolizationOff
			conf.ProfileBuildLimits = test.limits
			outputPath := filepath.Join(t.TempDir(), "profile.pprof")
			_, err := convertPerfScriptReaderSafely(
				strings.NewReader(test.script),
				outputPath,
				conf,
			)
			var limitErr *profileConversionLimitError
			if !errors.As(err, &limitErr) || limitErr.Resource != test.resource {
				t.Fatalf("conversion error = %v, want resource %q", err, test.resource)
			}
			if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
				t.Fatalf("limited conversion wrote a partial profile: %v", statErr)
			}
		})
	}
}

// 开启内存保护时，转换每新增 uniqueSamplesPerMemoryCheck 个唯一样本同步复查一次，触发时
// 取消本次执行并让转换以同一个原因失败。
func TestProfileBuilderChecksMemoryEveryUniqueSamples(t *testing.T) {
	probe := &fakeResourceProbe{memoryAvailable: 1024 * bytesPerMB}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()
	conf := validPerfConfig()
	conf.OutputDir = t.TempDir()
	conf.ResourceGuard = guard
	conf.ResourceGuardPolicy = ResourceGuardPolicy{RunDir: conf.OutputDir, ProtectBeat: true}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	unregister, err := guard.Register(conf.OutputDir, time.Now(), conf.ResourceGuardPolicy, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	builder := newProfileBuilderWithSymbolizer(conf, nil)
	defer builder.close()

	probe.set(64*bytesPerMB, 0)
	for pid := 1; pid <= uniqueSamplesPerMemoryCheck; pid++ {
		err = builder.addSample(perfSample{
			Period: 10,
			Comm:   "cpu-test",
			PID:    int64(pid),
			Frames: []perfFrame{{IP: 1, DSO: "/tmp/a", Symbol: "first"}},
		})
		if pid < uniqueSamplesPerMemoryCheck && err != nil {
			t.Fatalf("sample %d checked memory before the interval: %v", pid, err)
		}
	}
	if !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("addSample() error = %v, want ErrMemoryLimit", err)
	}
	assertGuardCause(t, ctx, resourceMemory, guardKindPressure)
}

func TestDuplicateStackOnlyAddsWeightNotFrameReferences(t *testing.T) {
	conf := validPerfConfig()
	conf.ProfileBuildLimits = profileBuildLimits{MaxUniqueSampleFrameRefs: 2}
	builder := newProfileBuilderWithSymbolizer(conf, nil)
	defer builder.close()
	sample := perfSample{
		Period: 10,
		Comm:   "cpu-test",
		PID:    123,
		Frames: []perfFrame{
			{IP: 1, DSO: "/tmp/a", Symbol: "first"},
			{IP: 2, DSO: "/tmp/a", Symbol: "second"},
		},
	}
	if err := builder.addSample(sample); err != nil {
		t.Fatal(err)
	}
	if err := builder.addSample(sample); err != nil {
		t.Fatalf("duplicate stack unexpectedly consumed reference budget: %v", err)
	}
	if builder.uniqueSampleFrameRefs != 2 || len(builder.profile.Sample) != 1 ||
		builder.profile.Sample[0].Value[0] != 2 {
		t.Fatalf(
			"refs=%d samples=%d value=%v",
			builder.uniqueSampleFrameRefs,
			len(builder.profile.Sample),
			builder.profile.Sample[0].Value,
		)
	}
}

func TestSymbolizedProfileStringLimits(t *testing.T) {
	tests := []struct {
		name     string
		frame    symbolizedFrame
		resource string
	}{
		{
			name:     "function name",
			frame:    symbolizedFrame{Function: "function-too-long", File: "/a"},
			resource: "function_string_bytes",
		},
		{
			name:     "file name",
			frame:    symbolizedFrame{Function: "fn", File: "/file-too-long"},
			resource: "file_string_bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conf := validPerfConfig()
			conf.ProfileBuildLimits = profileBuildLimits{MaxStringBytes: 4}
			builder := newProfileBuilderWithSymbolizer(
				conf,
				&fakeInlineSymbolizer{frames: []symbolizedFrame{test.frame}},
			)
			defer builder.close()
			builder.resolveAddress = func(perfFrame) (uint64, bool) { return 1, true }

			err := builder.addSample(perfSample{
				Period: 10,
				Comm:   "cpu",
				PID:    123,
				Frames: []perfFrame{{IP: 1, DSO: "/a", Symbol: "fn"}},
			})
			var limitErr *profileConversionLimitError
			if !errors.As(err, &limitErr) || limitErr.Resource != test.resource {
				t.Fatalf("addSample() error = %v, want resource %q", err, test.resource)
			}
			if len(builder.profile.Function) != 0 || len(builder.profile.Location) != 0 {
				t.Fatalf(
					"limited symbolized frame was inserted: functions=%d locations=%d",
					len(builder.profile.Function),
					len(builder.profile.Location),
				)
			}
		})
	}
}
