// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReader struct {
	usages  []uint64
	usageID int
	cores   float64
	coresOK bool
	mem     uint64
	memOK   bool
	limit   uint64
	limitOK bool
}

func (r *fakeReader) EffectiveCores() (float64, bool) {
	return r.cores, r.coresOK
}

func (r *fakeReader) CPUUsageNanos() (uint64, error) {
	if r.usageID >= len(r.usages) {
		return r.usages[len(r.usages)-1], nil
	}
	usage := r.usages[r.usageID]
	r.usageID++
	return usage, nil
}

func (r *fakeReader) MemWorkingSet() (uint64, bool) {
	return r.mem, r.memOK
}

func (r *fakeReader) MemLimit() (uint64, bool) {
	return r.limit, r.limitOK
}

func TestResourceSampler(t *testing.T) {
	config := normalizeConfig(Config{
		Enabled: true,
		Signal: SignalConfig{
			CPUSlowBeta:   0.95,
			CPUFastBeta:   0.7,
			FallbackCores: 1,
		},
	})
	manager := newManager(config)
	reader := &fakeReader{
		usages:  []uint64{0, uint64(2 * time.Second), uint64(2 * time.Second)},
		coresOK: false,
		mem:     50,
		memOK:   true,
		limit:   100,
		limitOK: true,
	}
	sampler := NewResourceSampler(reader, config, manager)

	now := time.Unix(0, 0)
	sampler.tickAt(now)
	sampler.tickAt(now.Add(time.Second))

	level := manager.Level()
	require.NotNil(t, level)
	assert.InDelta(t, 2.0, level.CPUSlow, 0.001)
	assert.InDelta(t, 2.0, level.CPUFast, 0.001)
	assert.True(t, level.MemValid)
	assert.InDelta(t, 0.5, level.Mem, 0.001)

	sampler.tickAt(now.Add(2 * time.Second))
	level = manager.Level()
	require.NotNil(t, level)
	assert.InDelta(t, 1.9, level.CPUSlow, 0.001)
	assert.InDelta(t, 1.4, level.CPUFast, 0.001)
	assert.Greater(t, level.CPUSlow, level.CPUFast)
}
