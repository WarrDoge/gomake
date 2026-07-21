//go:build unix

package build

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
)

var jobserverSeq atomic.Uint64

// newServerJobserver creates a fifo-backed jobserver primed with jobs-1 tokens
// (the caller also holds one implicit slot, for jobs total).
func newServerJobserver(jobs int) (*jobserver, error) {
	if jobs < 2 {
		return nil, nil
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("gomake-js-%d-%d", os.Getpid(), jobserverSeq.Add(1)))
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return nil, fmt.Errorf("create jobserver fifo: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("open jobserver fifo: %w", err)
	}
	tokens := make([]byte, jobs-1)
	for i := range tokens {
		tokens[i] = '+'
	}
	if _, err := f.Write(tokens); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("prime jobserver fifo: %w", err)
	}
	return &jobserver{
		authFlag: "--jobserver-auth=fifo:" + path,
		rfd:      f,
		wfd:      f,
		fifoPath: path,
		implicit: true,
	}, nil
}

func fdValid(fd int) bool {
	var stat syscall.Stat_t
	return syscall.Fstat(fd, &stat) == nil
}
