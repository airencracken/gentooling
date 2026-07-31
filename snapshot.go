package gentooling

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var (
	ErrLockObservationUnsupported = errors.New("gentooling: Portage lock observation unsupported")
	ErrStateLockUnavailable       = errors.New("gentooling: Portage state lock unavailable")
	ErrCandidateNotFound          = errors.New("gentooling: repository candidate not found")
)

// SnapshotOptions configures one combined system-state observation.
type SnapshotOptions struct {
	Installed  InstalledOptions
	Config     ConfigOptions
	Candidates CandidateOptions
	// IncludeCandidates binds repository candidates to each stabilized
	// observation so prospective policy evaluation cannot mix snapshots.
	IncludeCandidates bool
	Consistency       SnapshotConsistency
	// Attempts is the maximum number of complete observations. Zero uses 3.
	Attempts int

	betweenObservations func(int)
}

// SystemSnapshot is a mutually consistent view of installed packages,
// effective Portage policy, and world/system selections.
type SystemSnapshot struct {
	Installed    InstalledInventory
	Config       EffectiveConfig
	Repositories []Repository
	Candidates   CandidateInventory
	Selections   Selections
	Consistency  SnapshotConsistency
}

// SnapshotConsistency selects an explicit system-snapshot guarantee.
type SnapshotConsistency uint8

const (
	// LockedAndStabilized observes Portage-compatible VDB/world locks and then
	// requires two agreeing complete observations.
	LockedAndStabilized SnapshotConsistency = iota
	// StabilizedLockless skips lock files and requires agreeing observations.
	// It is intended for explicit unprivileged inspection, never fallback.
	StabilizedLockless
)

// ReadSystemSnapshot returns only after two consecutive complete observations
// agree. LockedAndStabilized additionally holds shared VDB and world locks.
// Persistent change is reported as ErrConcurrentMutation instead of returning
// mixed state.
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
	if options.Consistency != LockedAndStabilized && options.Consistency != StabilizedLockless {
		return SystemSnapshot{}, fmt.Errorf("%w: unknown snapshot consistency mode %d", ErrInvalidData, options.Consistency)
	}
	var locks []observedStateLock
	var err error
	if options.Consistency == LockedAndStabilized {
		locks, err = observeStateLocks(ctx, paths)
		if err != nil {
			return SystemSnapshot{}, err
		}
		defer releaseStateLocks(locks)
	}
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
			current.Consistency = options.Consistency
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
		if errs[1] == nil && options.IncludeCandidates {
			result.Candidates, errs[1] = ReadRepositoryCandidates(observationContext, config.Repositories, options.Candidates)
		}
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
	result.Repositories = cloneRepositories(config.Repositories)
	return result, nil
}

type ProspectiveCandidateEvaluation struct {
	Candidate  RepositoryCandidate
	Visibility VisibilityResult
	Use        UseEvaluation
}

// EvaluateCandidate evaluates one exact candidate using configuration and
// repository evidence captured by this stabilized snapshot.
func (snapshot SystemSnapshot) EvaluateCandidate(ctx context.Context, id PackageID) (ProspectiveCandidateEvaluation, error) {
	if err := ctx.Err(); err != nil {
		return ProspectiveCandidateEvaluation{}, err
	}
	var found *RepositoryCandidate
	for index := range snapshot.Candidates.Candidates {
		candidate := &snapshot.Candidates.Candidates[index]
		if candidate.ID.Category != id.Category || candidate.ID.Name != id.Name ||
			candidate.ID.Version != id.Version || id.Repository != "" && candidate.ID.Repository != id.Repository {
			continue
		}
		if id.Slot != "" && candidate.ID.Slot != id.Slot {
			continue
		}
		if found != nil {
			return ProspectiveCandidateEvaluation{}, fmt.Errorf("%w: %s is ambiguous without a repository", ErrInvalidData, id.CPV())
		}
		found = candidate
	}
	if found == nil {
		return ProspectiveCandidateEvaluation{}, fmt.Errorf("%w: %s", ErrCandidateNotFound, id.CPV())
	}
	visibility, err := snapshot.Config.EvaluateVisibility(ctx, PackageVisibilityContext{
		ID: found.ID, Keywords: append([]string(nil), found.Keywords...),
	})
	if err != nil {
		return ProspectiveCandidateEvaluation{}, err
	}
	use, err := snapshot.Config.EvaluateUse(ctx, PackageContext{
		ID: found.ID, DeclaredUse: append([]UseDeclaration(nil), found.DeclaredUse...), Stable: visibility.Stable,
	})
	if err != nil {
		return ProspectiveCandidateEvaluation{}, err
	}
	candidate := *found
	candidate.Keywords = append([]string(nil), found.Keywords...)
	candidate.DeclaredUse = append([]UseDeclaration(nil), found.DeclaredUse...)
	candidate.Inherited = append([]string(nil), found.Inherited...)
	return ProspectiveCandidateEvaluation{Candidate: candidate, Visibility: visibility, Use: use}, nil
}
