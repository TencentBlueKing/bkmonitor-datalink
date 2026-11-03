// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
//go:build !linux

package noderunner

import (
	"errors"
	"io"
	"os/exec"
)

func enableSubreaper() error                 { return nil }
func reapAdopted()                           {}
func hostCommand(Request) (*exec.Cmd, error) { return nil, errors.New("host mode requires Linux") }
func HostChild(io.Reader) error              { return errors.New("host mode requires Linux") }

func reapGroup(int)           {}
func waitLeaderExit(int) bool { return false }
