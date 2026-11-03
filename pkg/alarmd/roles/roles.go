// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package roles defines the responsibilities one alarmd process runs.
package roles

import (
	"errors"
	"fmt"
	"strings"
)

type Role string

const (
	Control Role = "control"
	Worker  Role = "worker"
	Channel Role = "channel"
)

// registry is the binary's static role contract. Adding a supported role here
// also includes it in the default shared process; explicit selections keep
// exactly the roles the deployment named.
var registry = [...]Role{Control, Worker, Channel}

// Set preserves declaration order. A nil Set is an omitted selection; an
// allocated, empty Set is an explicitly empty selection and is refused.
type Set []Role

// Resolve validates a selection and returns a copy. Omission selects all
// registered roles; unknown names, duplicates and explicit emptiness fail.
func Resolve(selected Set) (Set, error) {
	if selected == nil {
		return append(Set{}, registry[:]...), nil
	}
	if len(selected) == 0 {
		return nil, errors.New("roles must select at least one role")
	}
	seen := make(map[Role]bool, len(selected))
	for _, role := range selected {
		known := false
		for _, supported := range registry {
			if role == supported {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("roles contains unknown role %q", role)
		}
		if seen[role] {
			return nil, fmt.Errorf("roles contains duplicate role %q", role)
		}
		seen[role] = true
	}
	return append(Set{}, selected...), nil
}

// Parse reads the comma-separated --roles value. Whitespace around a CLI
// name is allowed; an empty value or an empty component is refused.
func Parse(value string) (Set, error) {
	if strings.TrimSpace(value) == "" {
		return Resolve(Set{})
	}
	var selected Set
	for index, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("roles contains an empty role at position %d", index+1)
		}
		selected = append(selected, Role(name))
	}
	return Resolve(selected)
}

func (s Set) Has(role Role) bool {
	for _, selected := range s {
		if selected == role {
			return true
		}
	}
	return false
}

func (s Set) String() string {
	names := make([]string, len(s))
	for index, role := range s {
		names[index] = string(role)
	}
	return strings.Join(names, ",")
}
