// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"errors"
	"fmt"
)

// NoDataDimensionTag is the dimension Python adds to every no-data group so a
// no-data anomaly is a different object from the threshold anomaly on the same
// series. Its value is the Python boolean True, which is what count_md5 sees;
// this package states the name, and the identity the value takes is settled
// where the identity is built, not here.
const NoDataDimensionTag = "__NO_DATA_DIMENSION__"

// NoDataPeriodFactField carries, on a synthetic no-data point, how many periods
// the group has been without data. The output layer states it in the alert
// text; nothing detects on it.
//
// It travels with the point's values because that is the only channel a
// synthetic point has to the converter, and because it is a fact about this
// point rather than configuration shared by the Plan. It is named apart from
// the detected value so that the one thing the detector reads stays the one
// thing it reads: a second number under a name the detector recognises is how
// an output-only fact becomes an input nobody meant.
const NoDataPeriodFactField = "__no_data_periods__"

// NoDataConfigV1 is an item's no-data detection setting, frozen with the Plan
// that carries it.
//
// Enablement is the presence of this section, not a field inside it. Python
// stores no_data_config as a dict with is_enabled beside the rest, and a Plan
// that carried NoDataConfigV1{...} with enablement switched off would read as
// configured while doing nothing - the shape that makes a reader look for the
// load that would wake it. Compilation therefore attaches this section only
// when Python says the item is enabled, and absent means no-data detection is
// not part of this Plan.
//
// An absent section is disabled, which is what the backend this replaces
// already does. Its three read sites all treat a missing no_data_config as off:
// core/control/item.py takes it with .get("no_data_config", {}), the nodata
// scenario falls back to {"is_enabled": False, ...}, and the strategy cache
// selects nodata strategies with `if no_data_config and
// no_data_config.get("is_enabled")`. The model's is_enabled: True default
// applies when a row is created through the product, not when the cache is
// read, so there is no difference here to keep an eye on.
type NoDataConfigV1 struct {
	// Continuous is how many consecutive absent periods raise the alert. It is
	// the trigger window and the threshold at once - the backend sets
	// check_window_size and trigger_count to the same number - so the layer
	// that builds the synthetic series reads it for both.
	//
	// It has no default. The backend subscripts it, int(cfg["continuous"]),
	// beside a level it reads with .get("level", NO_DATA_LEVEL): the same dict
	// literal defaults one and not the other, so an item that omits continuous
	// raises there and detects nothing. Supplying 5 here would turn an item
	// that reports nothing into one that alerts after five periods, which is a
	// missing value read as a benign one.
	Continuous uint32 `json:"continuous"`
	// AggDimension names the dimensions a series is reduced to before absence
	// is judged. Empty is a setting rather than a gap: it reduces every series
	// to one group, which is how Python expresses "tell me when this item has
	// no data at all". It is Python's model default.
	AggDimension []string `json:"agg_dimension,omitempty"`
	// Level is the severity of a no-data anomaly, independent of the levels the
	// item's thresholds declare. Python defaults it to 2 when the field is
	// absent, and compilation applies that default rather than passing zero on.
	Level uint32 `json:"level"`
	// TrackingHorizonSeconds is how long one group's absence goes on being
	// tracked before this item stops tracking it, frozen into the Plan by
	// compilation from the platform default and the item's own override.
	//
	// Zero means no horizon - absences are tracked for as long as they last -
	// and that is the meaning the evaluator already gives it, so an item that
	// says nothing keeps the behaviour it had before the horizon existed. The
	// field carries seconds rather than periods because the evaluator compares
	// it against evaluation times, and a period-valued horizon would change
	// length whenever the item's interval changed.
	//
	// It lives here rather than anywhere else in the object because this
	// section is already inside the digest domain, is omitempty so an item
	// without a horizon is byte-identical to before, and its serialised name
	// does not produce a second "no_data": token - the no-data census counts
	// that token in the stored bytes rather than parsing it, so a second one
	// would break the ledger's equality in a way that reads as a no-data
	// defect rather than a naming one.
	TrackingHorizonSeconds int64 `json:"no_data_tracking_horizon_seconds,omitempty"`
	// TrackingHorizonSource says where the horizon above came from, frozen
	// beside it: PLATFORM for a Plan that inherited the deployment's default,
	// STRATEGY for one whose item stated its own. Empty when there is no
	// horizon, and on a Plan compiled before the source was frozen - which
	// the next compilation replaces, since a new build is a new compiler
	// identity and the candidate cache recompiles under it.
	//
	// Frozen rather than inferred because the number alone cannot say: a
	// strategy that states exactly the platform's value is indistinguishable
	// from one that inherited it until the platform's value moves, and a
	// reader comparing against the platform's current value reads a Plan
	// compiled under the previous value as STRATEGY. The same digest domain
	// as the horizon, so a Plan whose source changed with its value unchanged
	// is a different object, as it should be: it will follow a different
	// value next.
	TrackingHorizonSource NoDataHorizonSource `json:"no_data_tracking_horizon_source,omitempty"`
}

// NoDataHorizonSource is where a Plan's effective no-data horizon came from.
type NoDataHorizonSource string

const (
	NoDataHorizonSourcePlatform NoDataHorizonSource = "PLATFORM"
	NoDataHorizonSourceStrategy NoDataHorizonSource = "STRATEGY"
)

// Validate rejects a section that cannot produce a decision, and normalises the
// one thing that is a restatement rather than a defect. A zero Continuous would
// make the trigger window empty and a level outside the contract's range would
// produce an event no downstream stage can file; a dimension listed twice is
// the same item either way and is deduplicated in place.
//
// What it deliberately does not reject is a dimension no series carries. That
// is a live strategy producing no groups, which the projection counts and a
// reader can act on; refusing the Plan for it would replace a visible
// misconfiguration with a strategy that detects nothing at all.
func (config *NoDataConfigV1) Validate() error {
	if config == nil {
		return nil
	}
	if config.Continuous == 0 {
		return errors.New("no_data_config continuous must be positive")
	}
	if config.Level < 1 || config.Level > 3 {
		return fmt.Errorf("no_data_config level %d is outside 1..3", config.Level)
	}
	// A negative horizon is refused rather than clamped. Zero already means
	// "no horizon", so clamping a negative to zero would turn a configuration
	// mistake into the setting that disables the feature, silently and in the
	// direction that looks healthy.
	if config.TrackingHorizonSeconds < 0 {
		return fmt.Errorf("no_data_config tracking horizon %d must not be negative", config.TrackingHorizonSeconds)
	}
	// The source is a closed word beside a positive horizon. An empty source
	// beside a positive horizon is accepted: that is a Plan compiled before
	// the source was frozen, read until it is recompiled. A source beside no
	// horizon says where nothing came from, and is refused.
	switch config.TrackingHorizonSource {
	case "", NoDataHorizonSourcePlatform, NoDataHorizonSourceStrategy:
	default:
		return fmt.Errorf("no_data_config tracking horizon source %q is not PLATFORM or STRATEGY", config.TrackingHorizonSource)
	}
	if config.TrackingHorizonSource != "" && config.TrackingHorizonSeconds == 0 {
		return fmt.Errorf("no_data_config tracking horizon source %q beside no horizon", config.TrackingHorizonSource)
	}
	seen := make(map[string]struct{}, len(config.AggDimension))
	deduplicated := config.AggDimension[:0]
	for _, dimension := range config.AggDimension {
		if dimension == "" {
			return errors.New("no_data_config agg_dimension must not name an empty dimension")
		}
		if dimension == NoDataDimensionTag {
			// The tag is added by the projection. Naming it as a source
			// dimension would make the group's own label an input to itself.
			return fmt.Errorf("no_data_config agg_dimension must not name %s", NoDataDimensionTag)
		}
		// A repeat is deduplicated rather than refused: the backend holds these
		// in a set, so an item that lists one twice means what it would mean
		// there. Refusing it would withhold the whole Plan over something that
		// changes nothing.
		if _, duplicate := seen[dimension]; duplicate {
			continue
		}
		seen[dimension] = struct{}{}
		deduplicated = append(deduplicated, dimension)
	}
	config.AggDimension = deduplicated
	// A name with surrounding whitespace is not refused. The backend does not
	// trim either, so such a name is simply one no series carries - which makes
	// every series invalid and shows up as the projection's dropped count. That
	// is a strategy to go and fix, and the count is how it is found; withholding
	// the Plan at compile time would turn a visible misconfiguration into a
	// strategy that stopped detecting anything at all.
	return nil
}
