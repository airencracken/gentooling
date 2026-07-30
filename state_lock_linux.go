//go:build linux

package gentooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"syscall"
	"time"
)

type observedStateLock struct {
	file *os.File
	path string
}

func observeStateLocks(ctx context.Context, paths SystemPaths) ([]observedStateLock, error) {
	lockPaths := []string{PortageStateLockPath(paths.VDB), PortageStateLockPath(paths.World)}
	sort.Strings(lockPaths)
	var locks []observedStateLock
	for _, path := range lockPaths {
		if len(locks) > 0 && locks[len(locks)-1].path == path {
			continue
		}
		lock, err := observeStateLock(ctx, path)
		if err != nil {
			releaseStateLocks(locks)
			return nil, err
		}
		if lock.file != nil {
			locks = append(locks, lock)
		}
	}
	return locks, nil
}

func observeStateLock(ctx context.Context, path string) (observedStateLock, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return observedStateLock{}, nil
	}
	if err != nil {
		return observedStateLock{}, fmt.Errorf("observe Portage state lock %q: %w", path, err)
	}
	lock := syscall.Flock_t{Type: syscall.F_RDLCK, Whence: 0}
	for {
		err = syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &lock)
		if err == nil {
			return observedStateLock{file: file, path: path}, nil
		}
		if !errors.Is(err, syscall.EACCES) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return observedStateLock{}, fmt.Errorf("observe Portage state lock %q: %w", path, err)
		}
		select {
		case <-ctx.Done():
			file.Close()
			return observedStateLock{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func releaseStateLocks(locks []observedStateLock) {
	for index := len(locks) - 1; index >= 0; index-- {
		lock := syscall.Flock_t{Type: syscall.F_UNLCK, Whence: 0}
		_ = syscall.FcntlFlock(locks[index].file.Fd(), syscall.F_SETLK, &lock)
		_ = locks[index].file.Close()
	}
}
