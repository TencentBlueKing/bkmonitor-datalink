package cli

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func connectionRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, syscall.ECONNREFUSED)
}
