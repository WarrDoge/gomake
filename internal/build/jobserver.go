package build

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"gomake/internal/config"
)

// jobserver implements the GNU make jobserver protocol so that job slots are
// shared across recursive $(MAKE) invocations instead of each level running its
// own -jN. Each participating make holds one implicit token (it may always run a
// single job) and draws additional slots from a shared named pipe.
//
// gomake only ever creates fifo-style jobservers (GNU make 4.4+), which need no
// file-descriptor inheritance: the fifo path travels in MAKEFLAGS and any child
// opens it by name. When inheriting from a parent make, both the fifo form and
// the legacy "R,W" descriptor form are accepted.
type jobserver struct {
	authFlag string // e.g. "--jobserver-auth=fifo:/tmp/x", appended to MAKEFLAGS
	rfd      *os.File
	wfd      *os.File
	fifoPath string // set only when this process created the fifo and must remove it

	mu        sync.Mutex
	implicit  bool
	closeOnce sync.Once
}

type jobToken struct {
	implicit bool
	value    byte
}

// acquire blocks until a job slot is available. The implicit slot is handed out
// first; further slots come as a byte read from the pipe.
func (j *jobserver) acquire() (jobToken, error) {
	j.mu.Lock()
	if j.implicit {
		j.implicit = false
		j.mu.Unlock()
		return jobToken{implicit: true}, nil
	}
	j.mu.Unlock()

	buf := make([]byte, 1)
	for {
		n, err := j.rfd.Read(buf)
		if err != nil {
			return jobToken{}, err
		}
		if n > 0 {
			return jobToken{value: buf[0]}, nil
		}
	}
}

// release returns a slot. A blocked reader can only be woken by a pipe token, so
// a freed implicit slot is reused by the next acquire call rather than a waiter;
// with the scheduler capped at one goroutine per slot this at worst idles the
// single implicit slot briefly and never deadlocks.
func (j *jobserver) release(t jobToken) {
	if t.implicit {
		j.mu.Lock()
		j.implicit = true
		j.mu.Unlock()
		return
	}
	// Best effort: a lost write only forfeits one slot for the rest of the run.
	_, _ = j.wfd.Write([]byte{t.value})
}

func (j *jobserver) close() {
	j.closeOnce.Do(func() {
		if j.rfd != nil {
			j.rfd.Close()
		}
		if j.wfd != nil && j.wfd != j.rfd {
			j.wfd.Close()
		}
		if j.fifoPath != "" {
			os.Remove(j.fifoPath)
		}
	})
}

func parseJobserverAuth(flags string) string {
	var fds string
	for tok := range strings.FieldsSeq(flags) {
		if v, ok := strings.CutPrefix(tok, "--jobserver-auth="); ok {
			return v
		}
		if v, ok := strings.CutPrefix(tok, "--jobserver-fds="); ok {
			fds = v // legacy alias; prefer -auth if both appear
		}
	}
	return fds
}

// adoptJobserver joins a jobserver described by an inherited auth string. It
// returns nil when the string is empty or the jobserver is unusable, in which
// case the caller falls back to local job counting.
func adoptJobserver(auth string) *jobserver {
	auth = strings.TrimSpace(auth)
	if auth == "" {
		return nil
	}
	if path, ok := strings.CutPrefix(auth, "fifo:"); ok {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return nil
		}
		return &jobserver{authFlag: "--jobserver-auth=" + auth, rfd: f, wfd: f, implicit: true}
	}
	r, w, ok := parseFDPair(auth)
	if !ok || !fdValid(r) || !fdValid(w) {
		return nil
	}
	rf := os.NewFile(uintptr(r), "jobserver-r")
	wf := os.NewFile(uintptr(w), "jobserver-w")
	if rf == nil || wf == nil {
		return nil
	}
	return &jobserver{authFlag: "--jobserver-auth=" + auth, rfd: rf, wfd: wf, implicit: true}
}

func parseFDPair(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, ",")
	if !ok {
		return 0, 0, false
	}
	r, err1 := strconv.Atoi(strings.TrimSpace(a))
	w, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || r < 0 || w < 0 {
		return 0, 0, false
	}
	return r, w, true
}

// setupJobserver adopts an inherited jobserver or, for a parallel top-level run,
// creates one. Either way the auth flag is recorded in MAKEFLAGS so recursive
// $(MAKE) invocations share the same slots. Returns nil when this run does not
// participate (sequential, or unsupported platform).
func setupJobserver(project *config.Project, options *Options) *jobserver {
	inherited := parseJobserverAuth(os.Getenv("MAKEFLAGS") + " " + os.Getenv("GNUMAKEFLAGS"))
	if js := adoptJobserver(inherited); js != nil {
		exportJobserverFlag(project, js.authFlag)
		return js
	}
	if options.Jobs > 1 && !project.NotParallel {
		js, err := newServerJobserver(options.Jobs)
		if err != nil || js == nil {
			return nil
		}
		exportJobserverFlag(project, js.authFlag)
		return js
	}
	return nil
}

func exportJobserverFlag(project *config.Project, flag string) {
	if flag == "" {
		return
	}
	if project.Variables == nil {
		project.Variables = map[string]string{}
	}
	cur := strings.TrimSpace(project.Variables["MAKEFLAGS"])
	if strings.Contains(cur, flag) {
		return
	}
	if cur == "" {
		project.Variables["MAKEFLAGS"] = flag
		return
	}
	project.Variables["MAKEFLAGS"] = cur + " " + flag
}
