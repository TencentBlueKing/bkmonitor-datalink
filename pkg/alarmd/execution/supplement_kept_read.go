// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "errors"

// ErrSupplementKeptReadAbsent is a supplement of a Slot whose queries,
// planned again from its frozen plan, are not all held by the read the
// lookback kept for it. It is found before any query is replayed or any
// series is handed to the worker, so the supplement ran nothing and wrote
// nothing: an outcome with a name (SUPPLEMENT_KEPT_READ_ABSENT), not a
// failed query.
var ErrSupplementKeptReadAbsent = errors.New("alarmd execution: the kept read does not hold the Slot's query")
