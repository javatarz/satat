package health

import (
	"context"
	"errors"
	"sync"
)

// LaneSetup is readiness check 4: every lane loaded and passed the floor. The worker sets lanes up
// once at start, after the health server is serving, so a failed setup shows in /readyz instead of
// as a container that only restarts.
type LaneSetup struct {
	mu   sync.Mutex
	done bool
	err  error
}

// Done records the result of lane setup; nil means every lane is ready.
func (s *LaneSetup) Done(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done, s.err = true, err
}

// Check is the "lanes" check: failing until Done, then failing with the setup error, if any.
func (s *LaneSetup) Check() Check {
	return Check{Name: "lanes", Run: func(context.Context) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.done {
			return errors.New("lane setup has not finished")
		}
		return s.err
	}}
}
