// Package flowlease provides Flow-scoped advisory ownership under Approach's
// shared artifact root. Lease files are stable: releasing a lease unlocks and
// closes its descriptor but never removes the file.
package flowlease

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

const collection = "flow-leases"

const (
	acquireRetryWindow   = 100 * time.Millisecond
	acquireRetryInterval = 2 * time.Millisecond
)

// LeaseState is the observable state of a Flow lease.
type LeaseState uint8

const (
	Free LeaseState = iota
	Held
)

func (s LeaseState) String() string {
	switch s {
	case Free:
		return "free"
	case Held:
		return "held"
	default:
		return fmt.Sprintf("LeaseState(%d)", s)
	}
}

// ErrHeld reports that another process currently owns the Flow lease.
var ErrHeld = errors.New("flow lease is held")

// Lease owns a locked Flow lease descriptor until Close. The descriptor is
// close-on-exec, so launched agents and their descendants cannot inherit it.
type Lease struct {
	file *os.File
	once sync.Once
	err  error
}

// ResolveRoot canonicalizes an existing absolute artifact root and verifies
// that it is a private directory owned by the current user.
func ResolveRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("flow lease artifact root must be absolute: %s", root)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve flow lease artifact root: %w", err)
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect flow lease artifact root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("flow lease artifact root must resolve to a directory")
	}
	if err := requireOwnedPrivate(info, artifacts.DirPerm, "flow lease artifact root"); err != nil {
		return "", err
	}
	return canonical, nil
}

// Inspect reports whether a Flow lease is free or held without waiting.
func Inspect(root, flowID string) (LeaseState, error) {
	file, err := openLeaseFile(root, flowID)
	if err != nil {
		return Free, err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return Held, nil
		}
		return Free, fmt.Errorf("inspect Flow lease lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		return Free, fmt.Errorf("release inspected Flow lease lock: %w", err)
	}
	return Free, nil
}

// Acquire takes the Flow lease without waiting.
func Acquire(root, flowID string) (*Lease, error) {
	file, err := openLeaseFile(root, flowID)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(acquireRetryWindow)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire Flow lease lock: %w", err)
		}
		if !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, ErrHeld
		}
		time.Sleep(acquireRetryInterval)
	}
	return &Lease{file: file}, nil
}

// Close releases the lease exactly once. It never unlinks the stable lock file.
func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.file == nil {
			return
		}
		unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		closeErr := l.file.Close()
		l.err = errors.Join(unlockErr, closeErr)
	})
	return l.err
}

func openLeaseFile(root, flowID string) (*os.File, error) {
	if !artifacts.IsSafeID(flowID) {
		return nil, fmt.Errorf("invalid Flow lease id %q", flowID)
	}
	canonical, err := ResolveRoot(root)
	if err != nil {
		return nil, err
	}
	dir, err := ensurePrivateCollection(canonical, collection)
	if err != nil {
		return nil, err
	}
	collectionLock, err := lockDirectory(dir, collection)
	if err != nil {
		return nil, err
	}
	defer collectionLock.Close()
	path := filepath.Join(dir, flowID+".lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_EXCL|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(artifacts.FilePerm))
	created := err == nil
	if errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EEXIST) {
		fd, err = syscall.Open(path, syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("open Flow lease file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if created {
		if err := file.Chmod(artifacts.FilePerm); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("set Flow lease file permissions: %w", err)
		}
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect Flow lease file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("Flow lease file must be regular")
	}
	if err := requireOwnedPrivate(info, artifacts.FilePerm, "Flow lease file"); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func ensurePrivateCollection(root, name string) (string, error) {
	rootLock, err := lockDirectory(root, "flow lease artifact root")
	if err != nil {
		return "", err
	}
	defer rootLock.Close()

	dir := filepath.Join(root, name)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		mkdirErr := os.Mkdir(dir, artifacts.DirPerm)
		created := mkdirErr == nil
		if mkdirErr != nil && !os.IsExist(mkdirErr) {
			return "", fmt.Errorf("create %s: %w", name, mkdirErr)
		}
		if created {
			if err := normalizeCreatedDirectory(dir, name); err != nil {
				return "", err
			}
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%s must be a directory, not a symlink or other node", name)
	}
	if err := requireOwnedPrivate(info, artifacts.DirPerm, name); err != nil {
		return "", err
	}
	return dir, nil
}

func normalizeCreatedDirectory(path, label string) error {
	if err := os.Chmod(path, artifacts.DirPerm); err != nil {
		return fmt.Errorf("set %s permissions: %w", label, err)
	}
	return nil
}

func lockDirectory(path, label string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock %s: %w", label, err)
	}
	return file, nil
}

func requireOwnedPrivate(info os.FileInfo, want os.FileMode, label string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect %s ownership: unsupported file metadata", label)
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("%s must be owned by uid %d", label, os.Geteuid())
	}
	if info.Mode().Perm() != want {
		return fmt.Errorf("%s permissions are %04o, want %04o", label, info.Mode().Perm(), want)
	}
	return nil
}
