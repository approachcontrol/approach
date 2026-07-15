package artifacts

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

const fileLockRetryInterval = 5 * time.Millisecond

// AcquireFileLock acquires an exclusive advisory lock with a bounded retry
// window. The lock parent directory remains the caller's responsibility.
func AcquireFileLock(path, label string, timeout time.Duration) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, FilePerm)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	closeFile := func() { _ = file.Close() }
	if err := file.Chmod(FilePerm); err != nil {
		closeFile()
		return nil, fmt.Errorf("secure %s: %w", label, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			if err := writeLockOwner(file, label); err != nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				closeFile()
				return nil, err
			}
			var once sync.Once
			return func() {
				once.Do(func() {
					_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
					closeFile()
				})
			}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			closeFile()
			return nil, fmt.Errorf("acquire %s: %w", label, err)
		}
		if !time.Now().Before(deadline) {
			closeFile()
			return nil, fmt.Errorf("timed out waiting for %s", label)
		}
		time.Sleep(fileLockRetryInterval)
	}
}

func writeLockOwner(file *os.File, label string) error {
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate %s: %w", label, err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek %s: %w", label, err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		return fmt.Errorf("write %s: %w", label, err)
	}
	return nil
}
