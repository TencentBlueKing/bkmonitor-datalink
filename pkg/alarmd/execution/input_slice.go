// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// InputSlice is one slice of a Query Group's series for one Slot (decision-020
// batch 8 §7.3): conditions every physical query of the Slot runs with, each
// ANDed into every OR group of the query's own conditions, so a series of the
// Slot lands in exactly one slice of a set of slices that partition them. A
// slice is not an identity: it is the shape of one read of this Slot on this
// replica, and enters no key, digest or event. The zero slice is the whole
// Query Group, as every Slot was read before slicing.
type InputSlice struct {
	Conditions []InputSliceCondition
}

// InputSliceCondition is one dimension's condition in a slice: the series'
// value of Dimension matches Pattern (req), or, Negated, does not (nreq). A
// series without the dimension has the empty value: it never matches a
// pattern that needs a character and always satisfies a negated one, as both
// UQ's own filter and PromQL read a missing label. Pattern is a Go RE2
// expression anchored at both ends, because UQ's own filter does not anchor.
type InputSliceCondition struct {
	Dimension string
	Negated   bool
	Pattern   string
}

// IsZero is whether the slice is the whole Query Group.
func (slice InputSlice) IsZero() bool { return len(slice.Conditions) == 0 }

// Validate checks that every condition names a dimension and carries an
// anchored pattern that compiles.
func (slice InputSlice) Validate() error {
	for index, condition := range slice.Conditions {
		if strings.TrimSpace(condition.Dimension) == "" {
			return fmt.Errorf("alarmd execution: input slice condition %d names no dimension", index)
		}
		if !strings.HasPrefix(condition.Pattern, "^") || !strings.HasSuffix(condition.Pattern, "$") {
			return fmt.Errorf("alarmd execution: input slice condition %d is not anchored at both ends", index)
		}
		if _, err := regexp.Compile(condition.Pattern); err != nil {
			return fmt.Errorf("alarmd execution: input slice condition %d: %w", index, err)
		}
	}
	return nil
}

// Matches is whether a series whose dimensions read values (a missing one
// absent from the map) is in the slice, by the semantics the conditions are
// read with downstream. For the tree's own checks and for tests; the query
// path filters at the source.
func (slice InputSlice) Matches(values map[string]string) (bool, error) {
	for _, condition := range slice.Conditions {
		pattern, err := regexp.Compile(condition.Pattern)
		if err != nil {
			return false, err
		}
		if pattern.MatchString(values[condition.Dimension]) == condition.Negated {
			return false, nil
		}
	}
	return true, nil
}

// ConditionBytes is how many bytes the slice's patterns take, the figure the
// tree's budget bounds.
func (slice InputSlice) ConditionBytes() int {
	var total int
	for _, condition := range slice.Conditions {
		total += len(condition.Pattern)
	}
	return total
}

// Digest names the slice on completion rows and in tests: the zero slice is
// the empty string.
func (slice InputSlice) Digest() string {
	if slice.IsZero() {
		return ""
	}
	hash := sha256.New()
	for _, condition := range slice.Conditions {
		negated := "req"
		if condition.Negated {
			negated = "nreq"
		}
		_, _ = fmt.Fprintf(hash, "%d:%s|%s|%d:%s\n", len(condition.Dimension), condition.Dimension, negated, len(condition.Pattern), condition.Pattern)
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// InputSliceUnsupportedError is a Slot that has to be read in slices and
// cannot be, for the named reason: the store's regular expressions are not
// the anchored RE2 the slices' partition rests on, the query is a PromQL
// expression the conditions cannot be put into, or a dimension sliced on is
// not a raw group-by label of every physical query.
type InputSliceUnsupportedError struct {
	Reason string
}

func (err *InputSliceUnsupportedError) Error() string {
	return "alarmd execution: input cannot be sliced: " + err.Reason
}

// InputSliceUnsupportedOf reports the reason err names, when it is an
// InputSliceUnsupportedError.
func InputSliceUnsupportedOf(err error) (string, bool) {
	var unsupported *InputSliceUnsupportedError
	if errors.As(err, &unsupported) {
		return unsupported.Reason, true
	}
	return "", false
}

type inputSliceKey struct{}

// WithInputSlice carries slice to a read made outside a Slot's own query -
// the lookback's recheck of a slice it captured - so the read asks for the
// same part of the Query Group the first read did.
func WithInputSlice(ctx context.Context, slice InputSlice) context.Context {
	if slice.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, inputSliceKey{}, slice)
}

// InputSliceFrom is the slice ctx carries, the zero slice - the whole Query
// Group - when it carries none.
func InputSliceFrom(ctx context.Context) InputSlice {
	slice, _ := ctx.Value(inputSliceKey{}).(InputSlice)
	return slice
}

// ErrSliceOverflow is a slice's input past its limit while it streams: the
// worker lets the slice go and splits it, and the read of it is no read -
// what the lookback captured of it is discarded (lookback.Read.Discard).
var ErrSliceOverflow = errors.New("alarmd worker: the slice's input passed its limit")
