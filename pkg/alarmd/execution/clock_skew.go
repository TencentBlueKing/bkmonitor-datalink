// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "time"

// MaxClockSkew is the largest difference alarmd assumes between the clocks of
// its processes - the Control Leader's, a Worker's - and the Redis server's.
// It is one documented assumption, as a lease-based system states one: the
// Kubernetes leader election's defaults (a 15 s lease renewed within 10 s)
// tolerate 5 s, and nodes of one cluster under NTP stay well inside it.
//
// Every margin that exists because two clocks may disagree is this bound,
// plus whatever else that margin covers, so a deployment whose clocks are
// worse than this has one number to know it is outside of:
//
//   - a pending content scope takes effect this long after the lease
//     deadline (ownership.ContentSwitchMargin);
//   - an output context revision takes effect this long past the write
//     bound after its activation boundary (the content cutover's floor).
//
// The lease fence itself does not depend on it: every lease deadline is
// minted and judged on the Redis server's clock alone.
const MaxClockSkew = 5 * time.Second
