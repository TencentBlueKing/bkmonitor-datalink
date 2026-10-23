// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/noderunner"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("expected serve, control or host-child")
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	socket := flags.String("socket", noderunner.DefaultSocket, "local control socket")
	id := flags.String("execution-id", "", "bound execution ID")
	mode := flags.String("mode", "filesystem", "bound execution mode")
	retention := flags.Int("retention-seconds", 900, "terminal evidence retention")
	idle := flags.Int("idle-seconds", 60, "unstarted runner lifetime")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	switch os.Args[1] {
	case "serve":
		if *retention < 1 || *retention > 900 || *idle < 1 || *idle > 300 {
			return fmt.Errorf("invalid lifetime")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return noderunner.Serve(ctx, *socket, *id, *mode, time.Duration(*retention)*time.Second, time.Duration(*idle)*time.Second)
	case "control":
		return noderunner.Control(context.Background(), *socket, os.Stdin, os.Stdout)
	case "host-child":
		return noderunner.HostChild(os.Stdin)
	default:
		return fmt.Errorf("unknown command")
	}
}
