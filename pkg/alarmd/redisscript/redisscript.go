// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package redisscript names the module's Lua scripts, so that what a script
// costs can be read script by script. A Redis client sees a script only as its
// text (EVAL, SCRIPT LOAD) or its SHA-1 (EVALSHA), and neither may be a metric
// label: this package holds the one table from those to a short name.
//
// A script is registered where it is defined, as the package's variables are
// initialised, so the table is complete before anything runs and is only read
// after that, without a lock. Registering a script does not change it: the text
// and so the SHA-1 stay what they were, and a call goes out as it did.
package redisscript

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/go-redis/redis/v8"
)

const (
	// Other is the name of a script that was not registered, which no script
	// of this module should be (the scan in metric keeps it so).
	Other = "other"

	// The commands a script goes out as.
	CommandEval    = "eval"
	CommandEvalSHA = "evalsha"
	CommandLoad    = "load"

	// MaxScripts bounds the table, and so every metric labelled by a script.
	// A constant rather than the table's size, which depends on the packages
	// a binary happens to link.
	MaxScripts = 64
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

type registry struct {
	nameOfBody map[string]string
	nameOfSHA  map[string]string
	bodyOfName map[string]string
}

func newRegistry() *registry {
	return &registry{nameOfBody: map[string]string{}, nameOfSHA: map[string]string{}, bodyOfName: map[string]string{}}
}

// register panics on anything that would make a label wrong or unbounded, at
// initialisation, where a test or the first start finds it.
//
// One text has one name. A client sees only the text or its SHA-1 and cannot
// tell who sent it, so two names for one text would read as whichever was
// registered last: the same text used twice takes one name that describes it.
func (r *registry) register(name, body string) {
	if !validName.MatchString(name) || name == Other {
		panic(fmt.Sprintf("redisscript: invalid script name %q", name))
	}
	if body == "" {
		panic(fmt.Sprintf("redisscript: script %q has no text", name))
	}
	if existing, ok := r.bodyOfName[name]; ok {
		if existing != body {
			panic(fmt.Sprintf("redisscript: script name %q is registered for two different texts", name))
		}
		return
	}
	if existing, ok := r.nameOfBody[body]; ok {
		panic(fmt.Sprintf("redisscript: scripts %q and %q have the same text, which needs one name", existing, name))
	}
	if len(r.bodyOfName) >= MaxScripts {
		panic(fmt.Sprintf("redisscript: more than %d scripts registered, at %q", MaxScripts, name))
	}
	sum := sha1.Sum([]byte(body))
	r.bodyOfName[name] = body
	r.nameOfBody[body] = name
	r.nameOfSHA[hex.EncodeToString(sum[:])] = name
}

func (r *registry) resolve(command string, args []interface{}) (script, scriptCommand string, isScript bool) {
	switch command {
	case CommandEvalSHA:
		return lookup(r.nameOfSHA, args, 1), CommandEvalSHA, true
	case CommandEval:
		return lookup(r.nameOfBody, args, 1), CommandEval, true
	case "script":
		if len(args) > 2 {
			if sub, ok := args[1].(string); ok && strings.EqualFold(sub, "load") {
				return lookup(r.nameOfBody, args, 2), CommandLoad, true
			}
		}
	}
	return "", "", false
}

func lookup(names map[string]string, args []interface{}, index int) string {
	if index < len(args) {
		if key, ok := args[index].(string); ok {
			if name, known := names[key]; known {
				return name
			}
		}
	}
	return Other
}

var scripts = newRegistry()

// Register names a script defined as a string constant and returns its text.
func Register(name, body string) string {
	scripts.register(name, body)
	return body
}

// New names a script and returns it as redis.NewScript would.
func New(name, body string) *redis.Script {
	scripts.register(name, body)
	return redis.NewScript(body)
}

// Resolve reads one command: whether it is a script going out, as which
// command, and under which name. command is the command's lowercase name
// (redis.Cmder.Name) and args its arguments. A script that is not registered
// is Other, so neither its text nor its SHA-1 can reach a label.
func Resolve(command string, args []interface{}) (script, scriptCommand string, isScript bool) {
	return scripts.resolve(command, args)
}

// Names is every name registered in this binary, sorted.
func Names() []string {
	names := make([]string, 0, len(scripts.bodyOfName))
	for name := range scripts.bodyOfName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
