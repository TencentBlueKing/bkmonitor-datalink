// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build !soak

package main

import "time"

// descentProbationRounds and descentEdgeProbeSpan are the probation and the
// span of a probe near a floor a probe failed at that a group taken down
// through its probes on the production wiring reads (startDescentFixture).
// Each of their rounds is a Slot the run executes one by one, and on
// production's - lookback.ProbationRounds, and lookback.EdgeProbeSpan, eight
// hours of minute Slots - one descent runs about two thousand. How many rounds
// a probe needs is the lookback's to count, held to production's by
// TestAProbeNearAFailedFloorNeedsEightHoursOfItsRounds; here they are
// shorter, the span still more than the probation, and production's under
// -tags soak.
const (
	descentProbationRounds = 20
	descentEdgeProbeSpan   = time.Hour
)
