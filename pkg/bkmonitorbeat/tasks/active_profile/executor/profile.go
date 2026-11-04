// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"bufio"
	"context"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pprofprofile "github.com/google/pprof/profile"
)

type profileStats struct {
	Samples                 int64
	TotalFrames             int64
	UnknownFrames           int64
	UnknownDSOTop           []dsoCount
	SymbolizationStatus     string
	SymbolizerBackend       string
	SymbolizationCandidates int64
	SymbolizationAttempts   int64
	SymbolizedLocations     int64
	InlineLocations         int64
	SymbolizationFailures   int64
	SymbolizationSkipped    int64
	SymbolizationUnresolved int64
	SymbolizationError      string
}

type perfFrame struct {
	IP           uint64
	DSO          string
	DSOOffset    uint64
	HasDSOOffset bool
	Symbol       string
}

type perfSample struct {
	Period int64
	Comm   string
	PID    int64
	Frames []perfFrame
}

var sampleHeaderPattern = regexp.MustCompile(`^(.*?)\s+(\d+)\s+\d+(?:\.\d+)?:`)

func convertPerfScript(scriptPath, outputPath string, conf perfConfig) (profileStats, error) {
	fp, err := os.Open(scriptPath)
	if err != nil {
		return profileStats{}, err
	}
	defer fp.Close()
	return convertPerfScriptReader(fp, outputPath, conf)
}

func convertPerfScriptReader(reader io.Reader, outputPath string, conf perfConfig) (profileStats, error) {
	return convertPerfScriptReaderContext(context.Background(), reader, outputPath, conf)
}

func convertPerfScriptReaderContext(
	ctx context.Context,
	reader io.Reader,
	outputPath string,
	conf perfConfig,
) (profileStats, error) {
	builder := newProfileBuilder(ctx, conf)
	defer builder.close()

	stats, err := parsePerfScriptReaderContext(ctx, reader, conf, builder.addSample)
	if err != nil {
		return stats, err
	}
	if stats.Samples == 0 {
		return stats, fmt.Errorf("perf script contains no samples: %w", ErrEmptyProfile)
	}

	stats.UnknownFrames = builder.unknownFrames
	stats.TotalFrames = builder.totalFrames
	stats.UnknownDSOTop = builder.topUnknownDSOs(20)
	stats.SymbolizationStatus = builder.symbolizationStatus()
	stats.SymbolizerBackend = builder.symbolizerBackend()
	stats.SymbolizationCandidates = builder.symbolizationCandidates
	stats.SymbolizationAttempts = int64(builder.symbolizationAttempts)
	stats.SymbolizedLocations = builder.symbolizedLocations
	stats.InlineLocations = builder.inlineLocations
	stats.SymbolizationFailures = builder.symbolizationFailures
	stats.SymbolizationSkipped = builder.symbolizationSkipped
	stats.SymbolizationUnresolved = builder.symbolizationUnresolved
	stats.SymbolizationError = builder.symbolizationError()

	if err := contextCause(ctx); err != nil {
		return stats, err
	}
	if err := builder.profile.CheckValid(); err != nil {
		return stats, fmt.Errorf("validate pprof: %w", err)
	}
	if err := contextCause(ctx); err != nil {
		return stats, err
	}
	if err := writeProfileAtomicContext(ctx, builder.profile, outputPath); err != nil {
		return stats, err
	}
	return stats, nil
}

func convertPerfScriptReaderSafely(
	reader io.Reader,
	outputPath string,
	conf perfConfig,
) (stats profileStats, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("convert perf script panic: %v", recovered)
		}
	}()
	return convertPerfScriptReader(reader, outputPath, conf)
}

func convertPerfScriptReaderSafelyContext(
	ctx context.Context,
	reader io.Reader,
	outputPath string,
	conf perfConfig,
) (stats profileStats, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("convert perf script panic: %v", recovered)
		}
	}()
	return convertPerfScriptReaderContext(ctx, reader, outputPath, conf)
}

func parsePerfScriptReader(reader io.Reader, conf perfConfig, consume func(perfSample) error) (profileStats, error) {
	return parsePerfScriptReaderContext(context.Background(), reader, conf, consume)
}

func parsePerfScriptReaderContext(
	ctx context.Context,
	reader io.Reader,
	conf perfConfig,
	consume func(perfSample) error,
) (profileStats, error) {
	var (
		current *perfSample
		stats   profileStats
		lines   uint64
	)
	defaultPeriod := int64(time.Second) / int64(conf.FrequencyHz)
	eventToken := conf.Event + ":"

	limits := conf.ProfileBuildLimits.normalized()
	flush := func() error {
		if current == nil || len(current.Frames) == 0 {
			current = nil
			return nil
		}
		if err := consume(*current); err != nil {
			return err
		}
		stats.Samples++
		current = nil
		return nil
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		lines++
		// A context check on every perf-script line is measurable on large
		// system profiles. Checking every 256 lines keeps cancellation prompt
		// while preserving streaming throughput.
		if lines&255 == 0 {
			if err := contextCause(ctx); err != nil {
				return stats, err
			}
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return stats, err
			}
			continue
		}
		if !startsWithSpace(line) && strings.Contains(line, eventToken) {
			if err := flush(); err != nil {
				return stats, err
			}
			comm, pid := parseSampleIdentity(line)
			if len(comm) > limits.MaxStringBytes {
				return stats, newProfileConversionLimitError(
					"comm_string_bytes",
					limits.MaxStringBytes,
					len(comm),
				)
			}
			current = &perfSample{
				Period: parseSamplePeriod(line, eventToken, defaultPeriod),
				Comm:   comm,
				PID:    pid,
			}
			continue
		}
		if current == nil || !startsWithSpace(line) {
			continue
		}
		frame, ok := parseFrame(line, conf.OffsetMode)
		if ok {
			if len(frame.DSO) > limits.MaxStringBytes {
				return stats, newProfileConversionLimitError(
					"dso_string_bytes",
					limits.MaxStringBytes,
					len(frame.DSO),
				)
			}
			if len(frame.Symbol) > limits.MaxStringBytes {
				return stats, newProfileConversionLimitError(
					"symbol_string_bytes",
					limits.MaxStringBytes,
					len(frame.Symbol),
				)
			}
			if len(current.Frames) >= limits.MaxFramesPerSample {
				return stats, newProfileConversionLimitError(
					"sample_stack_depth",
					limits.MaxFramesPerSample,
					len(current.Frames)+1,
				)
			}
			current.Frames = append(current.Frames, frame)
		}
	}
	if err := scanner.Err(); err != nil {
		return stats, err
	}
	if err := contextCause(ctx); err != nil {
		return stats, err
	}
	if err := flush(); err != nil {
		return stats, err
	}
	return stats, nil
}

func parseSampleIdentity(line string) (string, int64) {
	match := sampleHeaderPattern.FindStringSubmatch(line)
	if len(match) != 3 {
		return "", 0
	}
	pid, err := strconv.ParseInt(match[2], 10, 64)
	if err != nil {
		return strings.TrimSpace(match[1]), 0
	}
	return strings.TrimSpace(match[1]), pid
}

func startsWithSpace(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

func parseSamplePeriod(line, eventToken string, fallback int64) int64 {
	fields := strings.Fields(line)
	for i, field := range fields {
		if field == eventToken && i > 0 {
			value := strings.TrimSuffix(fields[i-1], ":")
			if period, err := strconv.ParseInt(value, 10, 64); err == nil && period > 0 {
				return period
			}
		}
	}
	return fallback
}

// isKernelDSO 判断该帧是否来自内核或无法归属的映射。perf 对这类 DSO 打印的始终是
// raw ip，不是文件偏移。
func isKernelDSO(dso string) bool {
	return dso == "" || strings.HasPrefix(dso, "[")
}

func parseFrame(line string, mode scriptOffsetMode) (perfFrame, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return perfFrame{}, false
	}
	ip, err := strconv.ParseUint(strings.TrimPrefix(fields[0], "0x"), 16, 64)
	if err != nil {
		return perfFrame{}, false
	}

	frame := perfFrame{IP: ip}
	dsoIndex := -1
	for i := 1; i < len(fields); i++ {
		if strings.HasPrefix(fields[i], "(") && strings.HasSuffix(fields[i], ")") {
			dsoIndex = i
			dsoValue := strings.TrimSuffix(strings.TrimPrefix(fields[i], "("), ")")
			frame.DSO = dsoValue
			if offsetIndex := strings.LastIndex(dsoValue, "+0x"); offsetIndex > 0 {
				offset, parseErr := strconv.ParseUint(dsoValue[offsetIndex+3:], 16, 64)
				if parseErr == nil {
					frame.DSO = dsoValue[:offsetIndex]
					frame.DSOOffset = offset
					frame.HasDSOOffset = true
				}
			}
			break
		}
	}
	if dsoIndex == -1 {
		return perfFrame{}, false
	}

	if fields[1] != "[unknown]" {
		frame.Symbol = strings.SplitN(fields[1], "+0x", 2)[0]
	}
	for i := dsoIndex + 1; i < len(fields); i++ {
		value := strings.TrimPrefix(fields[i], "+")
		if strings.HasPrefix(value, "0x") {
			offset, parseErr := strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 64)
			if parseErr == nil {
				frame.DSOOffset = offset
				frame.HasDSOOffset = true
				break
			}
		}
	}
	// perf < 6.4 没有 dsoff 字段，callchain 第一列对非内核 DSO 本身就是文件偏移。
	// 内核模块（.ko）同样被 perf 视为内核 DSO 而打印 raw ip，这里按 DSO 名无法区分，
	// 属于已知的次要偏差：用户态 CPU profile 中内核模块占比极低，且其符号通常已由
	// perf 自己的 sym 字段解出。
	if mode == offsetModeLegacyIP && !frame.HasDSOOffset && !isKernelDSO(frame.DSO) {
		frame.DSOOffset = frame.IP
		frame.HasDSOOffset = true
	}
	return frame, true
}

type profileBuilder struct {
	profile *pprofprofile.Profile
	// baseSampleLabels 是本次执行的定位标签，与具体样本无关，因此只构造一次并在
	// 所有样本间共享。
	baseSampleLabels      map[string][]string
	resolvers             map[string]*elfResolver
	mappings              map[string]*pprofprofile.Mapping
	functions             map[string]*pprofprofile.Function
	locations             map[string]*pprofprofile.Location
	sampleIndex           map[string]*pprofprofile.Sample
	nextMappingID         uint64
	nextFuncID            uint64
	nextLocID             uint64
	totalFrames           int64
	unknownFrames         int64
	unknownByDSO          map[string]int64
	limits                profileBuildLimits
	uniqueSampleFrameRefs int
	// checkMemory 在开启内存保护时按新增唯一样本同步复查内存，未开启时为 nil。
	checkMemory func() error

	symbolizer               inlineSymbolizer
	symbolCache              map[string][]symbolizedFrame
	resolveAddress           func(perfFrame) (uint64, bool)
	maxSymbolizedLocations   int
	maxSymbolizedBytes       int
	maxELFResolvers          int
	symbolizationAttempts    int
	symbolizationCandidates  int64
	symbolizedBytes          int
	symbolizedLocations      int64
	inlineLocations          int64
	symbolizationFailures    int64
	symbolizationSkipped     int64
	symbolizationUnresolved  int64
	symbolizationLimitReason string
	closeOnce                sync.Once
}

func newProfileBuilder(ctx context.Context, conf perfConfig) *profileBuilder {
	var symbolizer inlineSymbolizer
	if conf.InlineSymbolization == inlineSymbolizationAuto {
		symbolizer = newAutoSymbolizer(ctx, discoverSymbolizerOptions())
	}
	return newProfileBuilderWithSymbolizer(conf, symbolizer)
}

func newProfileBuilderWithSymbolizer(conf perfConfig, symbolizer inlineSymbolizer) *profileBuilder {
	period := int64(time.Second) / int64(conf.FrequencyHz)
	builder := &profileBuilder{
		profile: &pprofprofile.Profile{
			SampleType: []*pprofprofile.ValueType{
				{Type: "samples", Unit: "count"},
				{Type: "cpu", Unit: "nanoseconds"},
			},
			PeriodType:    &pprofprofile.ValueType{Type: periodTypeCPU, Unit: "nanoseconds"},
			Period:        period,
			TimeNanos:     time.Now().Add(-time.Duration(conf.DurationSec) * time.Second).UnixNano(),
			DurationNanos: int64(time.Duration(conf.DurationSec) * time.Second),
		},
		baseSampleLabels:       newBaseSampleLabels(conf.Labels),
		resolvers:              make(map[string]*elfResolver),
		mappings:               make(map[string]*pprofprofile.Mapping),
		functions:              make(map[string]*pprofprofile.Function),
		locations:              make(map[string]*pprofprofile.Location),
		sampleIndex:            make(map[string]*pprofprofile.Sample),
		nextMappingID:          1,
		nextFuncID:             1,
		nextLocID:              1,
		unknownByDSO:           make(map[string]int64),
		limits:                 conf.ProfileBuildLimits.normalized(),
		symbolizer:             symbolizer,
		symbolCache:            make(map[string][]symbolizedFrame),
		maxSymbolizedLocations: defaultMaxSymbolizedLocations,
		maxSymbolizedBytes:     defaultMaxSymbolizedBytes,
		maxELFResolvers:        defaultMaxELFResolvers,
	}
	builder.resolveAddress = builder.symbolAddress
	if conf.ResourceGuard != nil && conf.ResourceGuardPolicy.memoryEnabled() {
		guard, runID := conf.ResourceGuard, conf.OutputDir
		builder.checkMemory = func() error { return guard.checkMemory(runID) }
	}
	return builder
}

func (b *profileBuilder) close() {
	b.closeOnce.Do(func() {
		if b.symbolizer != nil {
			_ = b.symbolizer.Close()
		}
		for _, resolver := range b.resolvers {
			if resolver != nil {
				_ = resolver.close()
			}
		}
	})
}

func (b *profileBuilder) addSample(sample perfSample) error {
	locations := make([]*pprofprofile.Location, 0, len(sample.Frames))
	keys := make([]string, 0, len(sample.Frames))
	for _, frame := range sample.Frames {
		b.totalFrames++
		location, known, err := b.location(frame)
		if err != nil {
			return err
		}
		if !known {
			b.unknownFrames++
			dso := frame.DSO
			if dso == "" {
				dso = "[unknown]"
			}
			b.unknownByDSO[dso]++
		}
		locations = append(locations, location)
		keys = append(keys, strconv.FormatUint(location.ID, 10))
	}

	key := sample.Comm + "\x00" + strconv.FormatInt(sample.PID, 10) + "\x00" + strings.Join(keys, ";")
	if existing, ok := b.sampleIndex[key]; ok {
		existing.Value[0]++
		existing.Value[1] += sample.Period
		return nil
	}
	if len(b.sampleIndex) >= b.limits.MaxUniqueSamples {
		return newProfileConversionLimitError(
			"unique_samples",
			b.limits.MaxUniqueSamples,
			len(b.sampleIndex)+1,
		)
	}
	attemptedFrameRefs := b.uniqueSampleFrameRefs + len(locations)
	if attemptedFrameRefs > b.limits.MaxUniqueSampleFrameRefs {
		return newProfileConversionLimitError(
			"unique_sample_frame_refs",
			b.limits.MaxUniqueSampleFrameRefs,
			attemptedFrameRefs,
		)
	}
	entry := &pprofprofile.Sample{
		Location: locations,
		Value:    []int64{1, sample.Period},
		Label:    b.sampleLabels(sample),
	}
	b.sampleIndex[key] = entry
	b.profile.Sample = append(b.profile.Sample, entry)
	b.uniqueSampleFrameRefs = attemptedFrameRefs
	if b.checkMemory != nil && len(b.sampleIndex)%uniqueSamplesPerMemoryCheck == 0 {
		return b.checkMemory()
	}
	return nil
}

// newBaseSampleLabels 生成 pprof 的执行定位标签。ip 取主机执行时地址，解析不到时
// 保持标签不存在。
func newBaseSampleLabels(labels SampleLabels) map[string][]string {
	base := map[string][]string{
		"bk_biz_id":    {strconv.FormatInt(int64(labels.BizID), 10)},
		"task_id":      {strconv.FormatInt(int64(labels.TaskID), 10)},
		"scheduled_at": {strconv.FormatInt(labels.ScheduledAt, 10)},
		"bk_host_id":   {strconv.FormatInt(labels.BKHostID, 10)},
	}
	if labels.IP != "" {
		base["ip"] = []string{labels.IP}
	}
	return base
}

// sampleLabels 在执行定位标签之外附加进程标签，多进程合并产物依赖 pid/comm 区分。
func (b *profileBuilder) sampleLabels(sample perfSample) map[string][]string {
	label := make(map[string][]string, len(b.baseSampleLabels)+2)
	for key, value := range b.baseSampleLabels {
		label[key] = value
	}
	label["pid"] = []string{strconv.FormatInt(sample.PID, 10)}
	label["comm"] = []string{sample.Comm}
	return label
}

func (b *profileBuilder) topUnknownDSOs(limit int) []dsoCount {
	counts := make([]dsoCount, 0, len(b.unknownByDSO))
	for dso, count := range b.unknownByDSO {
		counts = append(counts, dsoCount{DSO: dso, Count: count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count == counts[j].Count {
			return counts[i].DSO < counts[j].DSO
		}
		return counts[i].Count > counts[j].Count
	})
	if len(counts) > limit {
		counts = counts[:limit]
	}
	return counts
}

func (b *profileBuilder) resolve(frame perfFrame) (string, string, int64, bool) {
	if frame.DSO == "" || strings.HasPrefix(frame.DSO, "[") {
		return "", "", 0, false
	}
	resolver := b.getELFResolver(frame.DSO)
	if resolver == nil {
		return "", "", 0, false
	}
	return resolver.resolve(frame)
}

func (b *profileBuilder) getELFResolver(dso string) *elfResolver {
	if dso == "" || strings.HasPrefix(dso, "[") {
		return nil
	}
	resolver, ok := b.resolvers[dso]
	if !ok {
		if len(b.resolvers) >= b.maxELFResolvers {
			return nil
		}
		var err error
		resolver, err = newELFResolver(dso)
		if err != nil {
			b.resolvers[dso] = nil
			return nil
		}
		b.resolvers[dso] = resolver
	}
	return resolver
}

func (b *profileBuilder) symbolAddress(frame perfFrame) (uint64, bool) {
	resolver := b.getELFResolver(frame.DSO)
	if resolver == nil {
		return 0, false
	}
	return resolver.objectAddress(frame)
}

func (b *profileBuilder) mapping(dso string) (*pprofprofile.Mapping, error) {
	if mapping, ok := b.mappings[dso]; ok {
		return mapping, nil
	}
	if len(b.mappings) >= b.limits.MaxMappings {
		return nil, newProfileConversionLimitError(
			"mappings",
			b.limits.MaxMappings,
			len(b.mappings)+1,
		)
	}
	mapping := &pprofprofile.Mapping{
		ID:           b.nextMappingID,
		File:         dso,
		HasFunctions: true,
	}
	b.nextMappingID++
	b.mappings[dso] = mapping
	b.profile.Mapping = append(b.profile.Mapping, mapping)
	return mapping, nil
}

func (b *profileBuilder) location(frame perfFrame) (*pprofprofile.Location, bool, error) {
	key := fmt.Sprintf("%s:%x", frame.DSO, frame.IP)
	if location, ok := b.locations[key]; ok {
		return location, locationHasFunction(location), nil
	}
	if len(b.locations) >= b.limits.MaxLocations {
		return nil, false, newProfileConversionLimitError(
			"locations",
			b.limits.MaxLocations,
			len(b.locations)+1,
		)
	}
	frames := b.locationFrames(frame)
	newFunctions := make(map[string]struct{}, len(frames))
	for _, frameLine := range frames {
		if len(frameLine.Function) > b.limits.MaxStringBytes {
			return nil, false, newProfileConversionLimitError(
				"function_string_bytes",
				b.limits.MaxStringBytes,
				len(frameLine.Function),
			)
		}
		if len(frameLine.File) > b.limits.MaxStringBytes {
			return nil, false, newProfileConversionLimitError(
				"file_string_bytes",
				b.limits.MaxStringBytes,
				len(frameLine.File),
			)
		}
		if frameLine.Function == "" {
			continue
		}
		functionKey := frameLine.Function + "\x00" + frameLine.File
		if _, exists := b.functions[functionKey]; exists {
			continue
		}
		newFunctions[functionKey] = struct{}{}
	}
	if len(b.functions)+len(newFunctions) > b.limits.MaxFunctions {
		return nil, false, newProfileConversionLimitError(
			"functions",
			b.limits.MaxFunctions,
			len(b.functions)+len(newFunctions),
		)
	}
	mapping, err := b.mapping(frame.DSO)
	if err != nil {
		return nil, false, err
	}
	location := &pprofprofile.Location{
		ID:      b.nextLocID,
		Mapping: mapping,
		Address: frame.IP,
	}
	b.nextLocID++
	for _, frameLine := range frames {
		if frameLine.Function == "" {
			continue
		}
		function, err := b.function(frameLine.Function, frameLine.File, frameLine.StartLine)
		if err != nil {
			return nil, false, err
		}
		location.Line = append(location.Line, pprofprofile.Line{Function: function, Line: frameLine.Line})
		if frameLine.File != "" && frameLine.File != frame.DSO {
			mapping.HasFilenames = true
		}
		if frameLine.Line > 0 {
			mapping.HasLineNumbers = true
		}
	}
	if len(location.Line) > 1 {
		mapping.HasInlineFrames = true
	}
	b.locations[key] = location
	b.profile.Location = append(b.profile.Location, location)
	return location, locationHasFunction(location), nil
}

func (b *profileBuilder) locationFrames(frame perfFrame) []symbolizedFrame {
	if b.symbolizer == nil || frame.DSO == "" || strings.HasPrefix(frame.DSO, "[") {
		return b.basicLocationFrames(frame)
	}
	b.symbolizationCandidates++
	if !b.symbolizer.Available() {
		b.symbolizationSkipped++
		if b.symbolizationLimitReason == "" {
			b.symbolizationLimitReason = "symbolizer unavailable or its deadline was reached"
		}
		return b.basicLocationFrames(frame)
	}
	address, ok := b.resolveAddress(frame)
	if !ok {
		b.symbolizationUnresolved++
		return b.basicLocationFrames(frame)
	}
	cacheKey := fmt.Sprintf("%s:%x", frame.DSO, address)
	if cached, exists := b.symbolCache[cacheKey]; exists {
		if len(cached) > 0 {
			return cached
		}
		return b.basicLocationFrames(frame)
	}
	if b.symbolizationAttempts >= b.maxSymbolizedLocations {
		b.symbolizationSkipped++
		b.symbolizationLimitReason = fmt.Sprintf(
			"symbolization unique-location limit %d reached",
			b.maxSymbolizedLocations,
		)
		return b.basicLocationFrames(frame)
	}
	b.symbolizationAttempts++
	frames, err := b.symbolizer.Symbolize(frame.DSO, address)
	if err != nil {
		b.symbolizationFailures++
		b.symbolCache[cacheKey] = nil
		return b.basicLocationFrames(frame)
	}
	if len(frames) == 0 {
		b.symbolizationUnresolved++
		b.symbolCache[cacheKey] = nil
		return b.basicLocationFrames(frame)
	}
	frameBytes := symbolizedFramesSize(frames)
	if b.symbolizedBytes+frameBytes > b.maxSymbolizedBytes {
		b.symbolizationSkipped++
		b.symbolizationLimitReason = fmt.Sprintf(
			"symbolization cache limit %d bytes reached",
			b.maxSymbolizedBytes,
		)
		b.symbolCache[cacheKey] = nil
		return b.basicLocationFrames(frame)
	}
	b.symbolizedBytes += frameBytes
	b.symbolizedLocations++
	if len(frames) > 1 {
		b.inlineLocations++
	}
	b.symbolCache[cacheKey] = frames
	return frames
}

func symbolizedFramesSize(frames []symbolizedFrame) int {
	const frameOverheadEstimate = 64
	total := 0
	for _, frame := range frames {
		total += len(frame.Function) + len(frame.File) + frameOverheadEstimate
	}
	return total
}

func (b *profileBuilder) basicLocationFrames(frame perfFrame) []symbolizedFrame {
	functionName := frame.Symbol
	fileName := frame.DSO
	line := int64(0)
	if functionName == "" {
		if resolvedName, resolvedFile, resolvedLine, ok := b.resolve(frame); ok {
			functionName = resolvedName
			if resolvedFile != "" {
				fileName = resolvedFile
			}
			line = resolvedLine
		}
	}
	if functionName == "" {
		return nil
	}
	return []symbolizedFrame{{Function: functionName, File: fileName, Line: line}}
}

func locationHasFunction(location *pprofprofile.Location) bool {
	for _, line := range location.Line {
		if line.Function != nil && line.Function.Name != "" {
			return true
		}
	}
	return false
}

func (b *profileBuilder) symbolizerBackend() string {
	if b.symbolizer == nil {
		return "off"
	}
	return b.symbolizer.Backend()
}

func (b *profileBuilder) symbolizationError() string {
	if b.symbolizer == nil {
		return ""
	}
	messages := make([]string, 0, 2)
	if lastError := strings.TrimSpace(b.symbolizer.LastError()); lastError != "" {
		messages = append(messages, lastError)
	}
	if b.symbolizationLimitReason != "" {
		messages = append(messages, b.symbolizationLimitReason)
	}
	return limitString(strings.Join(messages, "; "), maxSymbolizerStringBytes)
}

func (b *profileBuilder) symbolizationStatus() string {
	if b.symbolizer == nil {
		return symbolizationDisabled
	}
	if b.symbolizerBackend() == "basic" && b.symbolizationAttempts == 0 {
		return symbolizationUnavailable
	}
	if b.symbolizationFailures > 0 || b.symbolizationSkipped > 0 ||
		b.symbolizationUnresolved > 0 || b.symbolizationError() != "" {
		return symbolizationDegraded
	}
	return symbolizationComplete
}

func (b *profileBuilder) function(name, file string, startLine int64) (*pprofprofile.Function, error) {
	key := name + "\x00" + file
	if function, ok := b.functions[key]; ok {
		if function.StartLine == 0 && startLine > 0 {
			function.StartLine = startLine
		}
		return function, nil
	}
	if len(b.functions) >= b.limits.MaxFunctions {
		return nil, newProfileConversionLimitError(
			"functions",
			b.limits.MaxFunctions,
			len(b.functions)+1,
		)
	}
	function := &pprofprofile.Function{
		ID:         b.nextFuncID,
		Name:       name,
		SystemName: name,
		Filename:   file,
		StartLine:  startLine,
	}
	b.nextFuncID++
	b.functions[key] = function
	b.profile.Function = append(b.profile.Function, function)
	return function, nil
}

type elfResolver struct {
	file        *elf.File
	rawFile     *os.File
	symbols     []elf.Symbol
	symbolsOnce sync.Once
}

func newELFResolver(path string) (*elfResolver, error) {
	rawFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	stat, err := rawFile.Stat()
	if err != nil {
		_ = rawFile.Close()
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		_ = rawFile.Close()
		return nil, fmt.Errorf("ELF path is not a regular file")
	}
	if stat.Size() <= 0 || stat.Size() > maxSymbolizerDSOFileBytes {
		_ = rawFile.Close()
		return nil, fmt.Errorf("ELF size %d is outside the supported range", stat.Size())
	}
	file, err := elf.NewFile(rawFile)
	if err != nil {
		_ = rawFile.Close()
		return nil, err
	}
	return &elfResolver{file: file, rawFile: rawFile}, nil
}

func (r *elfResolver) close() error {
	if r == nil || r.rawFile == nil {
		return nil
	}
	err := r.rawFile.Close()
	r.rawFile = nil
	return err
}

func (r *elfResolver) loadSymbols() {
	r.symbolsOnce.Do(func() {
		symbols, _ := r.file.Symbols()
		dynamic, _ := r.file.DynamicSymbols()
		symbols = append(symbols, dynamic...)
		functions := make([]elf.Symbol, 0, len(symbols))
		for _, symbol := range symbols {
			if elf.ST_TYPE(symbol.Info) == elf.STT_FUNC && symbol.Value > 0 && symbol.Name != "" {
				functions = append(functions, symbol)
			}
		}
		sort.Slice(functions, func(i, j int) bool {
			return functions[i].Value < functions[j].Value
		})
		r.symbols = functions
	})
}

func (r *elfResolver) resolve(frame perfFrame) (string, string, int64, bool) {
	r.loadSymbols()
	address, ok := r.objectAddress(frame)
	if !ok {
		return "", "", 0, false
	}

	index := sort.Search(len(r.symbols), func(i int) bool {
		return r.symbols[i].Value > address
	}) - 1
	if index < 0 {
		return "", "", 0, false
	}
	symbol := r.symbols[index]
	if symbol.Size > 0 && address >= symbol.Value+symbol.Size {
		return "", "", 0, false
	}
	return symbol.Name, frame.DSO, 0, symbol.Name != ""
}

// objectAddress 把一帧换算成可用于查符号表的 ELF 虚拟地址。
//
// 优先使用文件偏移：它与加载基址无关，两种 perf 输出语义下都正确，也避免了
// "非 PIE 可执行文件在 legacy 模式下第一列是偏移、却被当成虚拟地址查表"的问题。
// 只有拿不到偏移时才退回第一列——此时它只对非 PIE（ET_EXEC）有意义，因为这类镜像
// 链接地址固定、不参与 ASLR，运行时地址本身就等于 ELF 虚拟地址；ET_DYN 缺少偏移
// 则无法反推加载基址，只能放弃。
func (r *elfResolver) objectAddress(frame perfFrame) (uint64, bool) {
	if frame.HasDSOOffset {
		if address, ok := r.offsetToVirtualAddress(frame.DSOOffset); ok {
			return address, true
		}
	}
	if r.file.Type != elf.ET_DYN {
		return frame.IP, true
	}
	return 0, false
}

func (r *elfResolver) offsetToVirtualAddress(offset uint64) (uint64, bool) {
	for _, program := range r.file.Progs {
		if program.Type != elf.PT_LOAD {
			continue
		}
		if offset >= program.Off && offset < program.Off+program.Filesz {
			return program.Vaddr + offset - program.Off, true
		}
	}
	return 0, false
}

func writeProfileAtomic(profile *pprofprofile.Profile, outputPath string) error {
	return writeProfileAtomicContext(context.Background(), profile, outputPath)
}

func writeProfileAtomicContext(ctx context.Context, profile *pprofprofile.Profile, outputPath string) error {
	if err := contextCause(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	tmpPath := outputPath + ".tmp"
	fp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	// 写未压缩的 protobuf：上报载荷要求的就是这个形态，采集端不做 gzip，避免产物
	// 落盘与上报两处形态不一致。
	if err := profile.WriteUncompressed(&contextWriter{ctx: ctx, writer: fp}); err != nil {
		_ = fp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := contextCause(ctx); err != nil {
		_ = fp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := fp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w *contextWriter) Write(value []byte) (int, error) {
	if err := contextCause(w.ctx); err != nil {
		return 0, err
	}
	return w.writer.Write(value)
}

func contextCause(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}
