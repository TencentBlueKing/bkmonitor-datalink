// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

const (
	ScopeMessage TerminalScope = "message"
	ScopePlan    TerminalScope = "plan"
	ScopeLevel   TerminalScope = "level"
	ScopeRecord  TerminalScope = "record"
)

type TerminalScope string

// Terminal carries only bounded contract coordinates. It deliberately omits
// the source payload and dynamic decoder text.
type Terminal struct {
	Scope             TerminalScope
	ReasonCode        string
	FieldPath         string
	PlanOrdinal       *uint32
	PlanID            string
	LevelID           *uint32
	RecordOrdinal     *uint32
	RecordID          string
	PlanFromOrdinal   *uint32
	RecordFromOrdinal *uint32
}

type TerminalSet struct {
	items []Terminal
}

func newTerminalSet(items []Terminal) TerminalSet {
	return TerminalSet{items: cloneTerminals(items)}
}

func (set TerminalSet) Len() int {
	return len(set.items)
}

func (set TerminalSet) Items() []Terminal {
	return cloneTerminals(set.items)
}

func cloneTerminals(items []Terminal) []Terminal {
	cloned := make([]Terminal, len(items))
	for index := range items {
		cloned[index] = items[index]
		cloned[index].PlanOrdinal = cloneUint32(items[index].PlanOrdinal)
		cloned[index].LevelID = cloneUint32(items[index].LevelID)
		cloned[index].RecordOrdinal = cloneUint32(items[index].RecordOrdinal)
		cloned[index].PlanFromOrdinal = cloneUint32(items[index].PlanFromOrdinal)
		cloned[index].RecordFromOrdinal = cloneUint32(items[index].RecordFromOrdinal)
	}
	return cloned
}

func cloneUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
