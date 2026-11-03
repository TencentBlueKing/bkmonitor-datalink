// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// recoveryStatusSetterValues are the values a recovery_config.status_setter
// is written with: how the alert ends (recovery or close) and whether no data
// through the recovery window ends it too (-nodata).
var recoveryStatusSetterValues = map[string]bool{
	"recovery": true, "close": true, "recovery-nodata": true, "close-nodata": true,
}

// recoveryStatusSetterValueLimit bounds how much of a written value a
// disposition's detail repeats.
const recoveryStatusSetterValueLimit = 64

// legacyRecoveryStatusSetter is one detect's recovery_config.status_setter as
// the recovery checker reads it. get_recovery_configs takes
// .get("status_setter", "recovery") (strategy.py:375-376); the checker splits
// a value holding "-" into the ending and the case (recover.py:136-138),
// ends the alert closed when the ending is "close" and recovered otherwise
// (:396-403), and recovers on no data when the case is "nodata". A value it
// cannot split - a number, a boolean, null, or more than one "-" - raises
// inside the same try as the window, so that alert's level takes the default
// window and "recovery" (:139-142): malformed. A list or an object is a
// membership test there: one without a "-" member is neither "close" nor
// split and recovers on its own window; one with it reaches .split and
// raises an AttributeError the try does not catch, so that level's recovery
// check fails each time - read here as malformed, the nearest thing this
// build can run.
type legacyRecoveryStatusSetter struct {
	written   string
	close     bool
	noData    bool
	malformed bool
}

// legacyRecoveryDisabled is whether a detect's recovery_config states
// disable_recovery as true: its owner turned automatic recovery off, and the
// level's alerts end only by a control-plane close. Only an explicit JSON
// true disables. The default where the key is absent is the writer's to
// state - a writer whose page defaults it by strategy type writes the value
// itself, since the type is not in the document - so absent, false or
// anything else keeps the level recovering, which is also how every document
// that does not state the key reads.
func legacyRecoveryDisabled(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	var disabled bool
	return json.Unmarshal(fields["disable_recovery"], &disabled) == nil && disabled
}

func readLegacyRecoveryStatusSetter(raw json.RawMessage) legacyRecoveryStatusSetter {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return legacyRecoveryStatusSetter{}
	}
	value, stated := fields["status_setter"]
	if !stated {
		return legacyRecoveryStatusSetter{written: "recovery"}
	}
	var text string
	if json.Unmarshal(value, &text) != nil || strings.TrimSpace(string(value)) == "null" {
		var members []any
		var keys map[string]json.RawMessage
		switch {
		case strings.TrimSpace(string(value)) == "null":
		case json.Unmarshal(value, &members) == nil:
			return legacyRecoveryStatusSetter{written: string(value), malformed: slices.Contains(members, any("-"))}
		case json.Unmarshal(value, &keys) == nil:
			_, dash := keys["-"]
			return legacyRecoveryStatusSetter{written: string(value), malformed: dash}
		}
		return legacyRecoveryStatusSetter{written: string(value), malformed: true}
	}
	setter := legacyRecoveryStatusSetter{written: text}
	ending, kind, split := strings.Cut(text, "-")
	if split && strings.Contains(kind, "-") {
		setter.malformed = true
		return setter
	}
	setter.close, setter.noData = ending == "close", kind == "nodata"
	return setter
}

// notes are the dispositions this build gives the level for what it wrote:
// a malformed value takes the default window (RECOVERY_CONFIG_DEFAULTED,
// said by the caller, which owns the window), and a value outside the four
// is read as the checker reads it. A close and a -nodata are executed - the
// caller freezes both into the recovery plan - and say nothing.
func (setter legacyRecoveryStatusSetter) notes(sourceID string, levelID uint32) []ObjectDisposition {
	if setter.malformed {
		return nil
	}
	written := strconv.Quote(boundedRecoveryStatusSetter(setter.written))
	note := func(reason, detail string) ObjectDisposition {
		return ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigNoted,
			Reason: reason, FieldPath: "detects[*].recovery_config.status_setter", Detail: detail}
	}
	var notes []ObjectDisposition
	if !recoveryStatusSetterValues[setter.written] {
		read := "recovery"
		if setter.close {
			read = "close"
		}
		notes = append(notes, note(ReasonRecoveryStatusSetterDefaulted, fmt.Sprintf("status_setter=%s read_as=%s", written, read)))
	}
	return notes
}

func boundedRecoveryStatusSetter(value string) string {
	if len(value) <= recoveryStatusSetterValueLimit {
		return value
	}
	return value[:recoveryStatusSetterValueLimit] + "..."
}
