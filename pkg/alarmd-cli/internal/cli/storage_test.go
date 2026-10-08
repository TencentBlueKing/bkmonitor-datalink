package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageProcessHelper(t *testing.T) {
	mode := os.Getenv("ALARMD_STORAGE_HELPER")
	if mode == "" {
		return
	}
	s := Store{Dir: os.Getenv("ALARMD_STORAGE_DIR")}
	if mode == "hold" {
		err := s.locked(func(*config) (bool, error) {
			fmt.Println("locked")
			_, err := bufio.NewReader(os.Stdin).ReadString('\n')
			return false, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	fmt.Println("writing")
	if err := s.save(Profile{EnvironmentID: mode, PublicBaseURL: "https://example.test"}, false); err != nil {
		t.Fatal(err)
	}
}

func storageChild(t *testing.T, dir, mode string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStorageProcessHelper$")
	cmd.Env = append(os.Environ(), "ALARMD_STORAGE_HELPER="+mode, "ALARMD_STORAGE_DIR="+dir)
	return cmd
}

func TestStoreAcrossProcessesAndKilledLockHolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config space-中文")
	holder := storageChild(t, dir, "hold")
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer holder.Process.Kill()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		ready <- scanner.Scan() && scanner.Text() == "locked"
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("holder did not acquire lock")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lock handshake timed out")
	}
	writer := storageChild(t, dir, "one")
	writerOutput, err := writer.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	defer writer.Process.Kill()
	writerReady := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(writerOutput)
		writerReady <- scanner.Scan() && scanner.Text() == "writing"
		for scanner.Scan() {
		}
	}()
	select {
	case ok := <-writerReady:
		if !ok {
			t.Fatal("writer did not start")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("writer handshake timed out")
	}
	done := make(chan error, 1)
	go func() { done <- writer.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("writer bypassed held lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("killed holder did not release lock")
	}
	children := []*exec.Cmd{storageChild(t, dir, "two"), storageChild(t, dir, "three")}
	for _, child := range children {
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range children {
		if err := child.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	s := Store{Dir: dir}
	if err := s.locked(func(c *config) (bool, error) {
		if len(c.Profiles) != 3 {
			t.Fatalf("lost concurrent updates: %v", c.Profiles)
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	assertPrivatePath(t, filepath.Join(dir, "profiles.json"), false)
}

func TestAtomicWriteReplacesAndProtectsFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(dir, "profiles.json")
	for _, content := range []string{"first", "second"} {
		if err := atomicWrite(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != content {
			t.Fatalf("incorrect replacement: %q %v", data, err)
		}
		assertPrivatePath(t, path, false)
	}
	assertPrivatePath(t, dir, true)
}
