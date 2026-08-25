// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type DetectorKey struct {
	Kind    string
	Version uint32
}

type AlgorithmFact struct {
	Matched         bool
	MatchedGroup    int
	PredicateDigest string
}

type Detector interface {
	Key() DetectorKey
	Evaluate(context.Context, strategy.DetectorSpec, strategy.NormalizedNumber) (AlgorithmFact, error)
}

type Registry struct {
	detectors map[DetectorKey]Detector
}

func NewRegistry(detectors ...Detector) (*Registry, error) {
	registry := &Registry{detectors: make(map[DetectorKey]Detector, len(detectors))}
	for _, detector := range detectors {
		if detector == nil {
			return nil, errors.New("alarmd detect: nil detector")
		}
		key := detector.Key()
		if key.Kind == "" || key.Version == 0 {
			return nil, errors.New("alarmd detect: invalid detector key")
		}
		if _, exists := registry.detectors[key]; exists {
			return nil, fmt.Errorf("alarmd detect: duplicate detector %s@%d", key.Kind, key.Version)
		}
		registry.detectors[key] = detector
	}
	if len(registry.detectors) == 0 {
		return nil, errors.New("alarmd detect: detector registry is empty")
	}
	return registry, nil
}

func NewDefaultRegistry() *Registry {
	registry, err := NewRegistry(thresholdDetector{})
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry *Registry) resolve(key DetectorKey) (Detector, bool) {
	if registry == nil {
		return nil, false
	}
	detector, ok := registry.detectors[key]
	return detector, ok
}
