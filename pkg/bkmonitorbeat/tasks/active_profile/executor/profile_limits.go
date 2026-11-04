// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"errors"
	"fmt"
)

const (
	defaultMaxFramesPerSample       = 1_024
	defaultMaxProfileStringBytes    = 4_096
	defaultMaxProfileMappings       = 8_192
	defaultMaxProfileLocations      = 1_000_000
	defaultMaxProfileFunctions      = 1_000_000
	defaultMaxUniqueProfileSamples  = 500_000
	defaultMaxUniqueSampleFrameRefs = 8_000_000
)

// uniqueSamplesPerMemoryCheck 是 perf 转换两次同步复查内存之间新增的唯一样本数。新的
// Location 必然带来新的唯一样本，转换内存随唯一样本增长，实测每个约 4 KB，两次复查之间
// 最多增长约 4 MB。
const uniqueSamplesPerMemoryCheck = 1_024

type profileBuildLimits struct {
	MaxFramesPerSample       int
	MaxStringBytes           int
	MaxMappings              int
	MaxLocations             int
	MaxFunctions             int
	MaxUniqueSamples         int
	MaxUniqueSampleFrameRefs int
}

func defaultProfileBuildLimits() profileBuildLimits {
	return profileBuildLimits{
		MaxFramesPerSample:       defaultMaxFramesPerSample,
		MaxStringBytes:           defaultMaxProfileStringBytes,
		MaxMappings:              defaultMaxProfileMappings,
		MaxLocations:             defaultMaxProfileLocations,
		MaxFunctions:             defaultMaxProfileFunctions,
		MaxUniqueSamples:         defaultMaxUniqueProfileSamples,
		MaxUniqueSampleFrameRefs: defaultMaxUniqueSampleFrameRefs,
	}
}

func (l profileBuildLimits) normalized() profileBuildLimits {
	defaults := defaultProfileBuildLimits()
	if l.MaxFramesPerSample <= 0 {
		l.MaxFramesPerSample = defaults.MaxFramesPerSample
	}
	if l.MaxStringBytes <= 0 {
		l.MaxStringBytes = defaults.MaxStringBytes
	}
	if l.MaxMappings <= 0 {
		l.MaxMappings = defaults.MaxMappings
	}
	if l.MaxLocations <= 0 {
		l.MaxLocations = defaults.MaxLocations
	}
	if l.MaxFunctions <= 0 {
		l.MaxFunctions = defaults.MaxFunctions
	}
	if l.MaxUniqueSamples <= 0 {
		l.MaxUniqueSamples = defaults.MaxUniqueSamples
	}
	if l.MaxUniqueSampleFrameRefs <= 0 {
		l.MaxUniqueSampleFrameRefs = defaults.MaxUniqueSampleFrameRefs
	}
	return l
}

type profileConversionLimitError struct {
	Resource  string
	Limit     int
	Attempted int
}

func (e *profileConversionLimitError) Error() string {
	if e == nil {
		return "pprof conversion limit reached"
	}
	return fmt.Sprintf(
		"pprof conversion %s limit %d reached before insertion (attempted %d)",
		e.Resource,
		e.Limit,
		e.Attempted,
	)
}

func newProfileConversionLimitError(resource string, limit, attempted int) error {
	return &profileConversionLimitError{Resource: resource, Limit: limit, Attempted: attempted}
}

func isProfileConversionLimitError(err error) bool {
	var limitErr *profileConversionLimitError
	return errors.As(err, &limitErr)
}
