package launchcontrol

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

const (
	// maxFrameBytes caps one NDJSON line in either direction. A FlowRecord
	// list is the largest thing that travels; 16 MiB is far above any real
	// one and far below anything that could exhaust the controller.
	maxFrameBytes = 16 << 20
	// dialTimeout bounds the liveness probe a Listen performs before it decides
	// a socket file is stale.
	dialTimeout = 500 * time.Millisecond
)

// ErrEndpointOwned reports that another live process already serves the
// socket path for this root. The caller runs without an endpoint.
var ErrEndpointOwned = errors.New("launch control endpoint is served by another process")

// ErrNoSocketPath reports that no candidate socket path fits the platform's
// sockaddr limit. Nothing is truncated; the caller runs without an endpoint.
var ErrNoSocketPath = errors.New("no launch control socket path fits the platform limit")

// socketDirCandidates lists where the per-user socket directory may live, in
// preference order. The state root itself is not a candidate: it is often
// under a long home-relative path that does not fit.
var socketDirCandidates = func() []string {
	uid := os.Getuid()
	dirs := []string{filepath.Join(os.TempDir(), fmt.Sprintf("approach-%d", uid))}
	fallback := filepath.Join("/tmp", fmt.Sprintf("approach-%d", uid))
	if dirs[0] != fallback {
		dirs = append(dirs, fallback)
	}
	return dirs
}

// maxSocketPathLen is measured, not assumed: sun_path differs by platform.
func maxSocketPathLen() int {
	var addr syscall.RawSockaddrUnix
	return len(addr.Path)
}

// SocketName is the file name for root's socket: eight hex characters of the
// SHA-256 of the canonical root, so two roots never collide and one root maps
// to one path across restarts.
func SocketName(root string) string {
	canonical := filepath.Clean(root)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:4]) + ".sock"
}

// SocketPath returns the socket path for root and whether one fits. The
// directory is created owner-only and verified owned by this user; a
// directory someone else owns is skipped, never used.
func SocketPath(root string) (string, bool) {
	name := SocketName(root)
	limit := maxSocketPathLen()
	for _, dir := range socketDirCandidates() {
		path := filepath.Join(dir, name)
		// The terminating NUL needs its byte too.
		if len(path)+1 > limit {
			continue
		}
		if err := ensureOwnedDir(dir); err != nil {
			continue
		}
		return path, true
	}
	return "", false
}

func ensureOwnedDir(dir string) error {
	if err := os.MkdirAll(dir, artifacts.DirPerm); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not owned by this user", dir)
	}
	if info.Mode().Perm() != artifacts.DirPerm {
		if err := os.Chmod(dir, artifacts.DirPerm); err != nil {
			return err
		}
	}
	return nil
}

// Listen binds path. The endpoint's ownership lock — a flock on the socket's
// sibling `.lock` file, held for the listener's whole lifetime — is what
// makes "is the socket stale" and "replace it" one step: a process that
// cannot take the lock is told another owns the endpoint (ErrEndpointOwned)
// and unlinks nothing, so a concurrent starter can never steal a path that
// was bound between its liveness probe and its own bind, and Close can never
// unlink a successor's socket because no successor binds until Close has
// released the lock. Under the lock, a live listener still means owned (an
// owner that predates the lock file), and a dead socket file is unlinked and
// replaced. The socket is chmod'd 0600 after bind.
func Listen(path string) (net.Listener, error) {
	release, held, err := lockEndpoint(path)
	if err != nil {
		return nil, err
	}
	if held {
		return nil, ErrEndpointOwned
	}
	listener, err := listenLocked(path)
	if err != nil {
		release()
		return nil, err
	}
	return &ownedListener{Listener: listener, path: path, release: release}, nil
}

func listenLocked(path string) (net.Listener, error) {
	if socketAlive(path) {
		return nil, ErrEndpointOwned
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale launch control socket: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on launch control socket: %w", err)
	}
	if err := os.Chmod(path, artifacts.FilePerm); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("secure launch control socket: %w", err)
	}
	return listener, nil
}

// ownedListener unlinks its socket and releases the endpoint lock on Close,
// in that order and under the lock, so the unlink can only ever hit the
// socket this listener bound.
type ownedListener struct {
	net.Listener
	path    string
	release func()
	once    sync.Once
}

func (l *ownedListener) Close() error {
	var err error
	l.once.Do(func() {
		err = l.Listener.Close()
		_ = os.Remove(l.path)
		l.release()
	})
	return err
}

// endpointLockPath is the ownership lock beside a socket: `<name>.lock` for
// `<name>.sock`, in the same owner-only directory.
func endpointLockPath(socketPath string) string {
	return strings.TrimSuffix(socketPath, ".sock") + ".lock"
}

// lockEndpoint tries the endpoint's ownership lock without waiting. held
// reports that another holder has it (nothing was acquired); otherwise the
// caller owns it until release.
func lockEndpoint(socketPath string) (release func(), held bool, err error) {
	path := endpointLockPath(socketPath)
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(artifacts.FilePerm))
	if err != nil {
		return nil, false, fmt.Errorf("open launch control endpoint lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("inspect launch control endpoint lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, false, errors.New("launch control endpoint lock must be a regular file")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("acquire launch control endpoint lock: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
		})
	}, false, nil
}

// socketAlive reports whether something accepts connections at path.
func socketAlive(path string) bool {
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ReapStale unlinks every *.sock in dir that nothing accepts on and whose
// endpoint lock is free: a held lock means an owner is alive (or between its
// probe and its bind), and its socket is not stale however it answers. Only
// files this user owns in a directory this user owns are touched.
func ReapStale(dir string) {
	if err := ensureOwnedDir(dir); err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sock") || entry.Type()&os.ModeSocket == 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		release, held, err := lockEndpoint(path)
		if err != nil || held {
			continue
		}
		if !socketAlive(path) {
			_ = os.Remove(path)
		}
		release()
	}
}

// writeFrame writes one NDJSON frame.
func writeFrame(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data)+1 > maxFrameBytes {
		return fmt.Errorf("launch control frame of %d bytes exceeds the %d byte cap", len(data), maxFrameBytes)
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// errFrameTooLarge is returned for a line over the cap; the frame is not read.
var errFrameTooLarge = errors.New("launch control frame exceeds the size cap")

// readFrame reads one NDJSON frame with the size cap enforced before decoding.
// The reader is capped one byte past the limit so an over-long line without a
// newline surfaces as too large instead of blocking for one that never comes.
func readFrame(conn io.Reader, into any) error {
	r := bufio.NewReader(io.LimitReader(conn, maxFrameBytes+1))
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxFrameBytes {
			return errFrameTooLarge
		}
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			// A final line without a newline is a truncated frame: the peer
			// closed mid-write. Report it as EOF so the caller classifies it as
			// unreachable rather than as an answer.
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return json.Unmarshal(line, into)
}
