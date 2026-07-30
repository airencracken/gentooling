//go:build !linux

package gentooling

import (
	"context"
	"fmt"
)

type observedStateLock struct{}

func observeStateLocks(context.Context, SystemPaths) ([]observedStateLock, error) {
	return nil, fmt.Errorf("%w: snapshots require Linux fcntl locks", ErrLockObservationUnsupported)
}

func observeLockPaths(context.Context, []string) ([]observedStateLock, error) {
	return nil, fmt.Errorf("%w: selections require Linux fcntl locks", ErrLockObservationUnsupported)
}

func releaseStateLocks([]observedStateLock) {}
