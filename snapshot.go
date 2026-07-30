package gentooling

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var ErrLockObservationUnsupported = errors.New("gentooling: Portage lock observation unsupported")

// SnapshotOptions configures one combined system-state observation.
type SnapshotOptions struct {
	Installed InstalledOptions
	Config    ConfigOptions
	// Attempts is the maximum number of complete observations. Zero uses 3.
	Attempts int

	betweenObservations func(int)
}

// SystemSnapshot is a mutually consistent view of installed packages,
// effective Portage policy, and world/system selections.
type SystemSnapshot struct {
	Installed  InstalledInventory
	Config     EffectiveConfig
	Selections Selections
}

// ReadSystemSnapshot returns only after two consecutive complete observations
// agree while shared VDB and world locks exclude cooperating package-state
// writers. Persistent change is reported as ErrConcurrentMutation instead of
// returning mixed state.
func ReadSystemSnapshot(ctx context.Context, paths SystemPaths, options SnapshotOptions) (SystemSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SystemSnapshot{}, err
	}
	attempts := options.Attempts
	if attempts == 0 {
		attempts = 3
	}
	if attempts < 2 {
		return SystemSnapshot{}, fmt.Errorf("%w: snapshot attempts must be at least 2", ErrInvalidData)
	}
	locks, err := observeStateLocks(ctx, paths)
	if err != nil {
		return SystemSnapshot{}, err
	}
	defer releaseStateLocks(locks)
	previous, err := readSystemObservation(ctx, paths, options)
	if err != nil {
		return SystemSnapshot{}, err
	}
	for attempt := 1; attempt < attempts; attempt++ {
		if options.betweenObservations != nil {
			options.betweenObservations(attempt)
		}
		current, readErr := readSystemObservation(ctx, paths, options)
		if readErr != nil {
			return SystemSnapshot{}, readErr
		}
		if reflect.DeepEqual(previous, current) {
			return current, nil
		}
		previous = current
	}
	return SystemSnapshot{}, fmt.Errorf("%w: system state did not stabilize after %d observations", ErrConcurrentMutation, attempts)
}

func readSystemObservation(ctx context.Context, paths SystemPaths, options SnapshotOptions) (SystemSnapshot, error) {
	observationContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		result    SystemSnapshot
		inventory InstalledInventory
		config    EffectiveConfig
		world     []Selection
		errs      [3]error
		wait      sync.WaitGroup
	)
	wait.Add(3)
	go func() {
		defer wait.Done()
		inventory, errs[0] = ReadInstalled(observationContext, paths, options.Installed)
		if errs[0] != nil {
			cancel()
		}
	}()
	go func() {
		defer wait.Done()
		config, errs[1] = ReadEffectiveConfig(observationContext, paths, options.Config)
		if errs[1] != nil {
			cancel()
		}
	}()
	go func() {
		defer wait.Done()
		world, errs[2] = readWorldSelections(observationContext, paths.World)
		if errs[2] != nil {
			cancel()
		}
	}()
	wait.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return SystemSnapshot{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return SystemSnapshot{}, err
	}
	for _, err := range errs {
		if err != nil {
			return SystemSnapshot{}, err
		}
	}
	system, err := selectionsFromProfile(config.Profile)
	if err != nil {
		return SystemSnapshot{}, err
	}
	selections := Selections{World: world, System: system}
	result.Installed, result.Config, result.Selections = inventory, config, selections
	return result, nil
}
