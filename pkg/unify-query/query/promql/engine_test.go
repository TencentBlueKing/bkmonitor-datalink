// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	prom "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/assert"
)

func TestNewEngineKeepsDefaultLookbackForExistingEngine(t *testing.T) {
	oldEngine := GlobalEngine
	oldLookback := defaultLookbackDelta
	t.Cleanup(func() {
		GlobalEngine = oldEngine
		defaultLookbackDelta = oldLookback
	})

	GlobalEngine = &prom.Engine{}
	defaultLookbackDelta = 10 * time.Minute

	NewEngine(&Params{LookbackDelta: 2 * time.Hour})

	assert.Equal(t, 10*time.Minute, GetDefaultLookbackDelta())
}

func TestNewEngineHandlesNilParams(t *testing.T) {
	oldEngine := GlobalEngine
	oldLookback := defaultLookbackDelta
	oldRegisterer := prometheus.DefaultRegisterer
	t.Cleanup(func() {
		GlobalEngine = oldEngine
		defaultLookbackDelta = oldLookback
		prometheus.DefaultRegisterer = oldRegisterer
	})

	GlobalEngine = nil
	defaultLookbackDelta = 5 * time.Minute
	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	assert.NotPanics(t, func() {
		NewEngine(nil)
	})
	assert.NotNil(t, GlobalEngine)
	assert.Equal(t, 5*time.Minute, GetDefaultLookbackDelta())
}
