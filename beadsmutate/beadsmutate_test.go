package beadsmutate_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/beadsmutate"
)

type fakeRunner struct {
	err   error
	dir   string
	args  []string
	calls int
}

func (r *fakeRunner) Run(dir string, args ...string) (string, error) {
	r.calls++
	r.dir = dir
	r.args = append([]string(nil), args...)
	return "", r.err
}

func TestMutatorClaimCanonicalizesInputsAndUsesClaimCommand(t *testing.T) {
	runner := &fakeRunner{}
	err := beadsmutate.NewMutator(runner).Claim("/selected/repo/../repo", "  child-42 \t")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if runner.calls != 1 || runner.dir != "/selected/repo" {
		t.Fatalf("runner call = (%d, %q), want (1, %q)", runner.calls, runner.dir, "/selected/repo")
	}
	if want := []string{"update", "--claim", "--", "child-42"}; !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, want)
	}
}

func TestClaimUsesDefaultRunner(t *testing.T) {
	runner := &fakeRunner{}
	previous := beadsmutate.DefaultRunner
	beadsmutate.DefaultRunner = runner
	t.Cleanup(func() { beadsmutate.DefaultRunner = previous })

	if err := beadsmutate.Claim("/repo", "bead-default"); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if runner.calls != 1 || runner.dir != "/repo" {
		t.Fatalf("runner call = (%d, %q), want (1, %q)", runner.calls, runner.dir, "/repo")
	}
	if want := []string{"update", "--claim", "--", "bead-default"}; !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, want)
	}
}

func TestMutatorClaimTreatsFlagShapedIDsAsPositional(t *testing.T) {
	for _, beadID := range []string{"--help", "--db=/other/db", "--actor=other"} {
		t.Run(beadID, func(t *testing.T) {
			runner := &fakeRunner{}
			if err := beadsmutate.NewMutator(runner).Claim("/repo", beadID); err != nil {
				t.Fatalf("Claim() error = %v", err)
			}
			if want := []string{"update", "--claim", "--", beadID}; !reflect.DeepEqual(runner.args, want) {
				t.Fatalf("runner args = %#v, want %#v", runner.args, want)
			}
		})
	}
}

func TestMutatorClaimRejectsInvalidInputsWithoutRunning(t *testing.T) {
	tests := []struct {
		name string
		repo string
		id   string
	}{
		{name: "blank repository", repo: " \t", id: "bead-1"},
		{name: "relative repository", repo: "relative/repo", id: "bead-1"},
		{name: "blank bead id", repo: "/repo", id: " \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{}
			err := beadsmutate.NewMutator(runner).Claim(tt.repo, tt.id)
			if err == nil {
				t.Fatal("Claim() error = nil, want validation error")
			}
			if runner.calls != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.calls)
			}
		})
	}
}

func TestMutatorClaimWrapsRunnerFailureWithChildIdentity(t *testing.T) {
	cause := errors.New("already claimed")
	err := beadsmutate.NewMutator(&fakeRunner{err: cause}).Claim("/repo", " child-42 ")
	if !errors.Is(err, cause) {
		t.Fatalf("Claim() error = %v, want wrapped cause %v", err, cause)
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "child-42") {
		t.Fatalf("Claim() error = %q, want child identity", got)
	}
}

func TestMutatorCloseCanonicalizesInputsAndUsesCloseCommand(t *testing.T) {
	runner := &fakeRunner{}
	err := beadsmutate.NewMutator(runner).Close("/selected/repo/../repo", "  child-42 \t", " merged as PR #7 ")
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if runner.calls != 1 || runner.dir != "/selected/repo" {
		t.Fatalf("runner call = (%d, %q), want (1, %q)", runner.calls, runner.dir, "/selected/repo")
	}
	want := []string{"close", "--reason=merged as PR #7", "--", "child-42"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, want)
	}
}

// The reason precedes the -- separator: bd treats everything after -- as
// positional, so a trailing --reason is parsed as a second issue ID.
func TestMutatorCloseOmitsBlankReason(t *testing.T) {
	runner := &fakeRunner{}
	if err := beadsmutate.NewMutator(runner).Close("/repo", "bead-1", "   "); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if want := []string{"close", "--", "bead-1"}; !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, want)
	}
}

func TestMutatorCloseTreatsFlagShapedIDsAsPositional(t *testing.T) {
	for _, beadID := range []string{"--help", "--db=/other/db"} {
		t.Run(beadID, func(t *testing.T) {
			runner := &fakeRunner{}
			if err := beadsmutate.NewMutator(runner).Close("/repo", beadID, "why"); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			if got := runner.args[len(runner.args)-2]; got != "--" {
				t.Fatalf("args[%d] = %q, want %q", len(runner.args)-2, got, "--")
			}
		})
	}
}

func TestMutatorCloseRejectsBlankInputs(t *testing.T) {
	runner := &fakeRunner{}
	for name, tc := range map[string]struct{ repo, bead string }{
		"blank repo":    {"", "bead-1"},
		"relative repo": {"repo", "bead-1"},
		"blank bead":    {"/repo", "  "},
	} {
		t.Run(name, func(t *testing.T) {
			if err := beadsmutate.NewMutator(runner).Close(tc.repo, tc.bead, "why"); err == nil {
				t.Fatal("Close() error = nil, want error")
			}
		})
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
}

func TestMutatorCloseWrapsRunnerError(t *testing.T) {
	sentinel := errors.New("boom")
	runner := &fakeRunner{err: sentinel}
	err := beadsmutate.NewMutator(runner).Close("/repo", "bead-1", "why")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Close() error = %v, want wrapped %v", err, sentinel)
	}
	if !strings.Contains(err.Error(), "bead-1") {
		t.Fatalf("Close() error = %v, want it to name the bead", err)
	}
}

func TestCloseUsesDefaultRunner(t *testing.T) {
	runner := &fakeRunner{}
	previous := beadsmutate.DefaultRunner
	beadsmutate.DefaultRunner = runner
	t.Cleanup(func() { beadsmutate.DefaultRunner = previous })

	if err := beadsmutate.Close("/repo", "bead-default", "why"); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if want := []string{"close", "--reason=why", "--", "bead-default"}; !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, want)
	}
}
