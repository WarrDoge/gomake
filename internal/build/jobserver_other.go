//go:build !unix

package build

// Platforms without fifo/pipe jobserver support fall back to local job counting.

func newServerJobserver(jobs int) (*jobserver, error) { return nil, nil }

func fdValid(fd int) bool { return false }
