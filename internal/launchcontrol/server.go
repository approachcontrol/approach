package launchcontrol

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/flowlease"
)

// LaunchLiveness is what the injected probe knows about a launch's session
// records. It is deliberately not "alive": the sessions store can attest that
// a record ended, but only positive evidence demotes.
type LaunchLiveness struct {
	RecordKnown bool
	Ended       bool
	EndedAt     time.Time
}

// LivenessProbe answers for one launch ID. cmd/approach wires it as a closure
// over the sessions store; this package never imports sessions.
type LivenessProbe func(launchID string) (LaunchLiveness, error)

// AppliedEvent tells the host that a launch's request was applied, so the TUI
// can refresh without waiting for its next tick.
type AppliedEvent struct {
	FlowID   string
	PhaseID  string
	LaunchID string
}

// Endpoint is what a launch is handed: the socket path and its per-launch
// token. A zero Endpoint means "no socket available"; the launch falls back.
type Endpoint struct {
	Path  string
	Token string
}

// Registration identifies one launch to the controller.
type Registration struct {
	FlowID   string
	PhaseID  string
	LaunchID string
	// Kind is recorded in launch.json for diagnostics: phase, autofix, repair,
	// generic, resume.
	Kind string
}

// Options configures a Controller.
type Options struct {
	Root  string
	Store *flowstore.Store
	// Liveness is optional; without it the sweep has only exit.json.
	Liveness LivenessProbe
	// InspectLease is optional and defaults to flowlease.Inspect. A held lease
	// vetoes every demotion.
	InspectLease func(root, flowID string) (flowlease.LeaseState, error)
	Now          func() time.Time
	// Log receives operator-facing diagnostics; nil discards them.
	Log io.Writer
}

type registration struct {
	flowID, phaseID, kind string
	tokenSHA256           string
}

// Controller owns the launch directories of one state root and, when it can
// bind the root's socket, serves proxied verbs against the process's store.
type Controller struct {
	root         string
	store        *flowstore.Store
	liveness     LivenessProbe
	inspectLease func(root, flowID string) (flowlease.LeaseState, error)
	now          func() time.Time
	log          io.Writer

	mu            sync.Mutex
	registrations map[string]registration
	launchLocks   map[string]*sync.Mutex
	notifier      func(AppliedEvent)
	listener      net.Listener
	socketPath    string
	closed        bool
	serving       sync.WaitGroup
	lastRetain    time.Time
}

// New builds a Controller. It touches nothing on disk; Recover and Listen do.
func New(opts Options) (*Controller, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("launch controller requires an absolute state root")
	}
	if opts.Store == nil {
		return nil, errors.New("launch controller requires a flow store")
	}
	c := &Controller{
		root:          filepath.Clean(root),
		store:         opts.Store,
		liveness:      opts.Liveness,
		inspectLease:  opts.InspectLease,
		now:           opts.Now,
		log:           opts.Log,
		registrations: make(map[string]registration),
		launchLocks:   make(map[string]*sync.Mutex),
	}
	if c.inspectLease == nil {
		c.inspectLease = flowlease.Inspect
	}
	if c.now == nil {
		c.now = func() time.Time { return time.Now().UTC() }
	}
	if c.log == nil {
		c.log = io.Discard
	}
	return c, nil
}

// Root is the state root this controller owns.
func (c *Controller) Root() string { return c.root }

func (c *Controller) logf(format string, args ...any) {
	fmt.Fprintf(c.log, "approach: launch control: "+format+"\n", args...)
}

// SetAppliedNotifier installs the callback run after every applied request
// and every reconciliation. Calls before it is set are dropped. It is invoked
// with no lock held.
func (c *Controller) SetAppliedNotifier(fn func(AppliedEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifier = fn
}

func (c *Controller) notify(event AppliedEvent) {
	c.mu.Lock()
	fn := c.notifier
	c.mu.Unlock()
	if fn != nil {
		fn(event)
	}
}

// SocketPath is the endpoint this controller serves, or "" when it has none.
func (c *Controller) SocketPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.socketPath
}

// launchLock returns the in-process mutex for a launch. The cross-process
// sequence lock is taken inside it, so two goroutines of one process never
// contend on the file lock's retry loop.
func (c *Controller) launchLock(launchID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, ok := c.launchLocks[launchID]
	if !ok {
		lock = &sync.Mutex{}
		c.launchLocks[launchID] = lock
	}
	return lock
}

// Register records a launch and returns its endpoint. The token is minted
// here, kept in memory, and persisted only as its SHA-256 in launch.json, so a
// controller restarted on the same root re-accepts launches the previous
// process handed out. When no socket is available Register still writes
// launch.json (the sweep needs the identity) and returns a zero Endpoint.
func (c *Controller) Register(reg Registration) (Endpoint, error) {
	if !artifacts.IsSafeID(reg.LaunchID) {
		return Endpoint{}, fmt.Errorf("launch control refuses unsafe launch id %q", reg.LaunchID)
	}
	if strings.TrimSpace(reg.FlowID) == "" {
		return Endpoint{}, errors.New("launch control registration requires a flow id")
	}
	log, err := OpenLog(c.root, reg.LaunchID)
	if err != nil {
		return Endpoint{}, err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Endpoint{}, fmt.Errorf("mint launch control token: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	entry := registration{
		flowID:      strings.TrimSpace(reg.FlowID),
		phaseID:     artifacts.NormalizePhaseID(reg.PhaseID),
		kind:        reg.Kind,
		tokenSHA256: tokenDigest(token),
	}
	if err := log.WriteLaunch(LaunchInfo{
		FlowID:       entry.flowID,
		PhaseID:      entry.phaseID,
		Kind:         entry.kind,
		TokenSHA256:  entry.tokenSHA256,
		RegisteredAt: c.now(),
	}); err != nil {
		return Endpoint{}, err
	}
	c.mu.Lock()
	c.registrations[reg.LaunchID] = entry
	path := c.socketPath
	c.mu.Unlock()
	if path == "" {
		return Endpoint{}, nil
	}
	return Endpoint{Path: path, Token: token}, nil
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// loadRegistrations reloads launch.json for every launch directory, so a
// restarted controller accepts the tokens its predecessor handed out.
func (c *Controller) loadRegistrations() error {
	ids, err := ListLaunchIDs(c.root)
	if err != nil {
		return err
	}
	for _, id := range ids {
		log, err := OpenLog(c.root, id)
		if err != nil {
			continue
		}
		info, ok, err := log.Launch()
		if err != nil {
			c.logf("launch %s: unreadable launch.json: %v", id, err)
			continue
		}
		if !ok || info.TokenSHA256 == "" {
			continue
		}
		c.mu.Lock()
		if _, exists := c.registrations[id]; !exists {
			c.registrations[id] = registration{
				flowID: info.FlowID, phaseID: artifacts.NormalizePhaseID(info.PhaseID),
				kind: info.Kind, tokenSHA256: info.TokenSHA256,
			}
		}
		c.mu.Unlock()
	}
	return nil
}

// Listen binds the root's socket and starts serving. ErrEndpointOwned means
// another process serves this root; the caller runs without an endpoint.
func (c *Controller) Listen() error {
	path, ok := SocketPath(c.root)
	if !ok {
		return ErrNoSocketPath
	}
	listener, err := Listen(path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = listener.Close()
		return errors.New("launch controller is closed")
	}
	c.listener = listener
	c.socketPath = path
	c.mu.Unlock()
	c.serving.Add(1)
	go c.serve(listener)
	return nil
}

func (c *Controller) serve(listener net.Listener) {
	defer c.serving.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		c.serving.Add(1)
		go func() {
			defer c.serving.Done()
			c.handleConn(conn)
		}()
	}
}

// Close stops serving. Closing the listener unlinks the socket file under the
// endpoint lock and then releases it — never the other way round, so a
// successor's socket is never the one unlinked. Launch directories are
// untouched; the next controller on this root recovers them.
func (c *Controller) Close() error {
	c.mu.Lock()
	c.closed = true
	listener := c.listener
	c.listener = nil
	c.socketPath = ""
	c.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	c.serving.Wait()
	return nil
}

func (c *Controller) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ClientTimeout))
	var req Request
	if err := readFrame(conn, &req); err != nil {
		if errors.Is(err, errFrameTooLarge) {
			_ = writeFrame(conn, refuse(errFrameTooLarge))
		}
		return
	}
	resp := c.handleRequest(req)
	_ = writeFrame(conn, resp)
}

// errIdentityMismatch is the one refusal for a request whose token, Flow, or
// phase does not match its launch's registration. Nothing is logged for it.
var errIdentityMismatch = errors.New("launch identity mismatch")

// authorize checks the request against its registration and returns the
// registered phase (empty for unowned launches).
func (c *Controller) authorize(req Request) (registration, error) {
	c.mu.Lock()
	entry, ok := c.registrations[req.LaunchID]
	c.mu.Unlock()
	if !ok || req.LaunchID == "" {
		return registration{}, errIdentityMismatch
	}
	presented := []byte(tokenDigest(req.Token))
	if subtle.ConstantTimeCompare(presented, []byte(entry.tokenSHA256)) != 1 {
		return registration{}, errIdentityMismatch
	}
	if req.Verb != VerbFlowList && strings.TrimSpace(req.FlowID) != entry.flowID {
		return registration{}, errIdentityMismatch
	}
	if entry.phaseID != "" && !IsRead(req.Verb) && !FlowLevel(req.Verb) &&
		artifacts.NormalizePhaseID(req.PhaseID) != entry.phaseID {
		return registration{}, errIdentityMismatch
	}
	if entry.phaseID != "" && !IsRead(req.Verb) {
		record, err := c.store.Read(entry.flowID)
		if err != nil {
			return registration{}, err
		}
		phase, ok := PhaseByID(record, entry.phaseID)
		if !ok {
			return registration{}, errIdentityMismatch
		}
		for _, launchID := range phase.RecoveredLaunchIDs {
			if launchID == req.LaunchID {
				return registration{}, fmt.Errorf("launch %q was recovered and can no longer write", req.LaunchID)
			}
		}
	}
	return entry, nil
}

// FlowLevel reports verbs whose target is the Flow, not a phase. They are
// authorized on the Flow alone; in the log their phase is the launch's own,
// which is what replay gates on.
func FlowLevel(verb Verb) bool {
	switch verb {
	case VerbPlanSet, VerbIssueSet, VerbPRSet, VerbMergeSet:
		return true
	}
	return false
}

func (c *Controller) handleRequest(req Request) Response {
	if err := Validate(req); err != nil {
		return refuse(err)
	}
	class, _ := Classify(req.Verb)
	if class == ClassDirect {
		return refuse(fmt.Errorf("%s is not proxied; run it directly", req.Verb))
	}
	entry, err := c.authorize(req)
	if err != nil {
		return refuse(err)
	}
	req.OwnerPhaseID = entry.phaseID
	if IsRead(req.Verb) {
		resp, err := Execute(c.store, req)
		if err != nil {
			return Response{SchemaVersion: ProtocolSchemaVersion, Error: err.Error()}
		}
		return resp
	}
	// Flow-level verbs carry the launch's own phase in the log so replay has a
	// phase to gate on.
	phaseID := artifacts.NormalizePhaseID(req.PhaseID)
	if FlowLevel(req.Verb) || phaseID == "" {
		if entry.phaseID != "" {
			phaseID = entry.phaseID
		}
	}
	env := RequestEnvelope{
		RequestID:  req.RequestID,
		FlowID:     req.FlowID,
		PhaseID:    phaseID,
		Verb:       req.Verb,
		Replayable: Replayable(req.Verb),
		Unowned:    entry.phaseID == "",
		Payload:    req.Payload,
		WrittenBy:  WrittenByController,
	}
	resp, event, err := c.applyLogged(req, env)
	if err != nil {
		return Response{SchemaVersion: ProtocolSchemaVersion, Error: err.Error()}
	}
	if event != nil {
		c.notify(*event)
	}
	return resp
}

// applyLogged is the write pipeline shared by the controller and the CLI's
// direct path: launch lock → durable append (ack ordering) → Execute → applied
// marker. The marker records the phase status after the apply, which is what
// a later replay compares against.
func (c *Controller) applyLogged(req Request, env RequestEnvelope) (Response, *AppliedEvent, error) {
	lock := c.launchLock(req.LaunchID)
	lock.Lock()
	defer lock.Unlock()
	log, err := OpenLog(c.root, req.LaunchID)
	if err != nil {
		return Response{}, nil, err
	}
	unlock, err := log.Lock(LaunchLockTimeout)
	if err != nil {
		return Response{}, nil, err
	}
	defer unlock()
	resp, err := ApplyLogged(c.store, log, req, env, c.now())
	if err != nil && resp.SchemaVersion == 0 {
		return Response{}, nil, err
	}
	if err != nil {
		// Execute already produced a wire result (success or refusal). A
		// missing applied marker is recovered by replay; do not tell the
		// agent the mutation failed, or it may retry a non-replayable verb.
		c.logf("launch %s: applied marker not written after %s: %v", req.LaunchID, req.Verb, err)
	}
	return resp, &AppliedEvent{FlowID: req.FlowID, PhaseID: env.PhaseID, LaunchID: req.LaunchID}, nil
}

// ApplyLogged runs one write under an already-held launch lock: append the
// envelope durably, execute, then write the applied marker. A store refusal
// still advances applied_seq (result: refused) so replay never retries it.
func ApplyLogged(store *flowstore.Store, log *Log, req Request, env RequestEnvelope, now time.Time) (Response, error) {
	if env.Unowned {
		req.OwnerPhaseID = ""
	} else {
		req.OwnerPhaseID = artifacts.NormalizePhaseID(env.PhaseID)
		if info, ok, err := log.Launch(); err != nil {
			return Response{}, fmt.Errorf("read launch ownership: %w", err)
		} else if ok && strings.TrimSpace(info.PhaseID) != "" {
			req.OwnerPhaseID = artifacts.NormalizePhaseID(info.PhaseID)
		}
	}
	if env.Observed.Status == "" {
		env.Observed = observePhase(store, req.FlowID, env.PhaseID)
	}
	env.WrittenAt = now
	seq, err := log.Append(env)
	if err != nil {
		return Response{}, fmt.Errorf("record launch request: %w", err)
	}
	resp, err := Execute(store, req)
	if err != nil {
		return Response{}, err
	}
	if err := applyMarkerHook(); err != nil {
		return resp, err
	}
	state := AppliedState{AppliedSeq: seq, Result: ResultApplied, AppliedAt: now}
	if !resp.OK {
		state.Result = ResultRefused
	}
	observed := observePhase(store, req.FlowID, env.PhaseID)
	state.Status = observed.Status
	state.ObservedUpdatedAt = observed.UpdatedAt
	if err := log.WriteApplied(state); err != nil {
		return resp, fmt.Errorf("record applied launch request: %w", err)
	}
	return resp, nil
}

// observePhase reads the phase's current status for the diagnostic
// `observed` fields. Best effort: an unreadable Flow leaves them empty.
func observePhase(store *flowstore.Store, flowID, phaseID string) ObservedPhase {
	if store == nil || flowID == "" || phaseID == "" {
		return ObservedPhase{}
	}
	record, err := store.Read(flowID)
	if err != nil {
		return ObservedPhase{}
	}
	phase, ok := PhaseByID(record, phaseID)
	if !ok {
		return ObservedPhase{}
	}
	return ObservedPhase{Status: string(phase.Status), UpdatedAt: phase.UpdatedAt}
}

// RecoverReport summarizes a Recover pass.
type RecoverReport struct {
	Launches   int
	Replayed   int
	Reconciled int
	Retired    int
	Notices    []string
}

// Recover reaps stale sockets in the socket directory, reloads registrations
// from launch.json, replays and reconciles every launch, and runs retention.
// Errors on one launch are logged and do not stop the pass; a broken launch
// directory must not take the TUI down.
func (c *Controller) Recover() (RecoverReport, error) {
	if path, ok := SocketPath(c.root); ok {
		ReapStale(filepath.Dir(path))
	}
	var report RecoverReport
	if err := c.loadRegistrations(); err != nil {
		return report, err
	}
	sweep := c.Sweep()
	report.Launches = sweep.Launches
	report.Replayed = sweep.Replayed
	report.Reconciled = sweep.Reconciled
	report.Notices = sweep.Notices
	retired, err := c.Retain()
	report.Retired = retired
	return report, err
}

// RecordLaunchExit writes exit.json for a launch. The lease runner calls it
// (through cmd/approach) after the agent's process group is gone; the sweep
// treats the file as authoritative exit evidence.
func RecordLaunchExit(root, flowID, phaseID, launchID string, code int, signaled bool, endedAt time.Time) error {
	log, err := OpenLog(root, launchID)
	if err != nil {
		return err
	}
	if endedAt.IsZero() {
		endedAt = time.Now().UTC()
	}
	return log.WriteExit(ExitRecord{
		FlowID:   flowID,
		PhaseID:  artifacts.NormalizePhaseID(phaseID),
		ExitCode: code,
		Signaled: signaled,
		EndedAt:  endedAt.UTC(),
		Source:   "lease_runner",
	})
}

// RecordBaseline decorates the AddPhaseLaunchID seam: after a successful
// launch-ID publication it writes baseline.json for exactly that launch with
// the status the store returned. A blank root makes it a pass-through.
func RecordBaseline(root string, next func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)) func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
	if next == nil || strings.TrimSpace(root) == "" {
		return next
	}
	return func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
		record, err := next(update)
		if err != nil {
			return record, err
		}
		phase, ok := PhaseByID(record, update.PhaseID)
		if !ok {
			return record, nil
		}
		log, openErr := OpenLog(root, strings.TrimSpace(update.LaunchID))
		if openErr != nil {
			return record, nil
		}
		_ = log.WriteBaseline(Baseline{BaselineStatus: string(phase.Status), ObservedUpdatedAt: phase.UpdatedAt})
		return record, nil
	}
}
