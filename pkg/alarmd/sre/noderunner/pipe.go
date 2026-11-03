// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package noderunner

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// outputPipe is one of the command's output streams, read by the runner.
//
// exec used to copy the streams and wait a fixed two seconds (WaitDelay)
// after the command exited for the copy to reach the end. Those two seconds
// covered the runner's own reading as well: a command that exits with its
// pipe full leaves up to a pipe's worth of lines, each redacted and spooled
// under the runner's lock, and on a loaded node (or under the race detector)
// that took longer. The copy was then cut, the execution reported failed,
// and the message blamed detached descendants that did not exist.
//
// The end is now read from the pipe. Once the command's process group has
// been reaped (finish), nothing in it can write again, so the pipe holds
// all the group wrote: it is read to the end with non-blocking reads. An
// empty pipe that reports end of file had no other writer; an empty pipe
// that would block is still open, and only a process outside the group --
// a detached descendant -- can be holding it.
//
// That rests on reapGroup having waited for every member of the group,
// which it can because Serve makes the runner the subreaper of the
// command's orphans (Linux). In a process that is not their subreaper, a
// member killed but not yet exited still holds the pipe for that moment
// and reads as detached.
type outputPipe struct {
	read, write *os.File
}

func openOutputPipes() (*outputPipe, *outputPipe, error) {
	out, err := newOutputPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := newOutputPipe()
	if err != nil {
		out.closeWrite()
		out.closeRead()
		return nil, nil, err
	}
	return out, stderr, nil
}

func newOutputPipe() (*outputPipe, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &outputPipe{read: read, write: write}, nil
}

func (p *outputPipe) closeWrite() { _ = p.write.Close() }
func (p *outputPipe) closeRead()  { _ = p.read.Close() }

// drain reads the stream into w while the command runs, and after finish
// reads what is left. The channel receives whether the pipe was still open
// once empty.
func (p *outputPipe) drain(w io.Writer) <-chan bool {
	done := make(chan bool, 1)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := p.read.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
			}
			switch {
			case err == nil:
			case errors.Is(err, os.ErrDeadlineExceeded):
				done <- p.rest(w, buf)
				return
			default:
				// End of file: every writer is gone.
				done <- false
				return
			}
		}
	}()
	return done
}

// finish wakes a read waiting on the pipe once the group is gone; drain
// then reads the rest without waiting.
func (p *outputPipe) finish() { _ = p.read.SetReadDeadline(time.Now()) }

// rest reads the pipe without blocking until it is empty, and reports
// whether it was still open then.
func (p *outputPipe) rest(w io.Writer, buf []byte) bool {
	// The deadline that woke the read would refuse these too; each read
	// below returns at once whatever it finds, so none waits without it.
	if err := p.read.SetReadDeadline(time.Time{}); err != nil {
		return false
	}
	raw, err := p.read.SyscallConn()
	if err != nil {
		return false
	}
	for {
		var n int
		var readErr error
		if err := raw.Read(func(fd uintptr) bool {
			n, readErr = syscall.Read(int(fd), buf)
			return true
		}); err != nil {
			return false
		}
		switch {
		case n > 0:
			_, _ = w.Write(buf[:n])
		case readErr == syscall.EINTR:
		case readErr == syscall.EAGAIN:
			return true
		default:
			return false
		}
	}
}
