//go:build linux

package gentooling

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestObserveStateLockWaitsForEcosystemWriter(t *testing.T) {
	path := t.TempDir() + "/.pkg.portage_lockfile"
	command := exec.Command(os.Args[0], "-test.run=TestStateLockWriterHelper")
	command.Env = append(os.Environ(), "GENTOOLING_LOCK_HELPER="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = command.Wait()
	})
	if line, readErr := bufio.NewReader(stdout).ReadString('\n'); readErr != nil || line != "locked\n" {
		t.Fatalf("writer readiness = %q, %v", line, readErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = observeStateLock(ctx, path)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("observing writer-held lock = %v", err)
	}
}

func TestStateLockWriterHelper(t *testing.T) {
	path := os.Getenv("GENTOOLING_LOCK_HELPER")
	if path == "" {
		t.Skip("helper subprocess only")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
	if err := syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &lock); err != nil {
		t.Fatal(err)
	}
	fmt.Println("locked")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
}
