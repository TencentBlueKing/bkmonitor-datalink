// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Command pack builds alarmd-cli for every release target and writes the
// release alarmd embeds. pkg/alarmd/Makefile and the Dockerfile run it before
// building alarmd:
//
//	go run ./clirelease/pack -cli ../alarmd-cli -version 0.4.123 -out clirelease/assets
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/clirelease"
)

func main() {
	source := flag.String("cli", "../alarmd-cli", "the alarmd-cli module directory")
	version := flag.String("version", "", "the version alarmd is stamped with; the CLI gets the same")
	out := flag.String("out", "clirelease/assets", "the release root alarmd embeds")
	flag.Parse()
	build := func(target clirelease.Target, dst string) error {
		command := exec.Command("go", "build", "-buildvcs=false", "-trimpath",
			"-ldflags", "-s -w -X main.version="+*version, "-o", dst, ".")
		command.Dir = *source
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.GOOS, "GOARCH="+target.GOARCH, "GOWORK=off")
		command.Stdout, command.Stderr = os.Stderr, os.Stderr
		return command.Run()
	}
	if err := clirelease.Pack(*out, *version, build); err != nil {
		fmt.Fprintf(os.Stderr, "pack alarmd-cli release: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "packed alarmd-cli %s for %d targets into %s\n", *version, len(clirelease.Targets), *out)
}
