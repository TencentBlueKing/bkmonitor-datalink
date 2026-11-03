// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"strings"
	"testing"
)

// A missing required parameter that says where it is read from is refused
// with that place, so the caller can fetch it; one that says nothing is
// refused by name alone.
func TestAMissingRequiredParameterNamesWhereToReadIt(t *testing.T) {
	op := Operation{Fields: map[string]Field{
		"tenant": {Type: "string", Source: "strategy.board rows[].tenant"},
		"id":     {Type: "string"},
	}, Required: []string{"tenant", "id"}}
	err := validate(op, Params{"id": "7"})
	if err == nil || !strings.Contains(err.Error(), "tenant") || !strings.Contains(err.Error(), "strategy.board rows[].tenant") {
		t.Fatalf("err = %v, want the field and where to read it", err)
	}
	if err := validate(op, Params{"tenant": "t"}); err == nil || err.Error() != "required parameter: id" {
		t.Fatalf("err = %v, want the bare refusal", err)
	}
}
