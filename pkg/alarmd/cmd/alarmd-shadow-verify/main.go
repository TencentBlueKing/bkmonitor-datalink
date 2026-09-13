// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// alarmd-shadow-verify checks a bounded local fixture. It has no network,
// production configuration or runtime side effects.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
)

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("alarmd-shadow-verify", flag.ContinueOnError)
	path := flags.String("input", "", "local offline fixture JSON")
	maxBytes := flags.Int("max-bytes", 0, "required input and individual record byte bound")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || *maxBytes <= 0 || *maxBytes >= int(^uint(0)>>1) {
		return fmt.Errorf("input and positive max-bytes are required")
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, int64(*maxBytes)+1))
	if err != nil {
		return err
	}
	input, err := shadow.DecodeOfflineInput(payload, *maxBytes)
	if err != nil {
		return err
	}
	output, err := shadow.ValidateOfflineInput(input, *maxBytes)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(output)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
