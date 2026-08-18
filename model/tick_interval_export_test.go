package model

import (
	"time"

	"github.com/approachcontrol/approach/scanner"
)

// testTickInterval collapses the two 1 Hz poll loops for tests. The test
// helpers that drain a tea.Cmd tree run every command in it, including the
// loops' own reschedule ticks, so at the production cadence each tick child in
// a batch cost a real second of wall time.
const testTickInterval = time.Millisecond

// fastTickOptions injects the collapsed cadence unless the test set its own.
func fastTickOptions(opts Options) Options {
	if opts.AutoAdvanceTickInterval == 0 {
		opts.AutoAdvanceTickInterval = testTickInterval
	}
	if opts.FlowRefreshTickInterval == 0 {
		opts.FlowRefreshTickInterval = testTickInterval
	}
	if opts.StatusTimings == (StatusTimings{}) {
		opts.StatusTimings = StatusTimings{
			FadeStep1: testTickInterval,
			FadeStep2: 2 * testTickInterval,
			Lifetime:  3 * testTickInterval,
		}
	}
	return opts
}

// newModelForTest is NewWithOptions with the poll loops collapsed. Tests build
// Models through it rather than calling NewWithOptions directly so no test
// waits on the production tick cadence.
func newModelForTest(repos []scanner.Repo, opts Options) Model {
	return NewWithOptions(repos, fastTickOptions(opts))
}
