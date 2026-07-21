package build

import (
	"strings"
	"testing"
	"time"

	"gomake/internal/config"
)

func TestParseJobserverAuth(t *testing.T) {
	cases := map[string]string{
		"-j --jobserver-auth=fifo:/tmp/x": "fifo:/tmp/x",
		"--jobserver-fds=3,4 -k":          "3,4",
		"--jobserver-auth=5,6":            "5,6",
		"-j2 -k":                          "",
		"":                                "",
	}
	for in, want := range cases {
		if got := parseJobserverAuth(in); got != want {
			t.Errorf("parseJobserverAuth(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJobserverServerTokenLimit(t *testing.T) {
	js, err := newServerJobserver(3) // implicit + 2 pipe tokens = 3 concurrent
	if err != nil {
		t.Fatalf("newServerJobserver() error = %v", err)
	}
	if js == nil {
		t.Skip("jobserver unsupported on this platform")
	}
	defer js.close()

	tokens := make([]jobToken, 0, 3)
	for i := range 3 {
		tk, err := js.acquire()
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		tokens = append(tokens, tk)
	}

	// A fourth slot is not available until one is released.
	got := make(chan jobToken, 1)
	go func() {
		tk, err := js.acquire()
		if err == nil {
			got <- tk
		}
	}()
	select {
	case <-got:
		t.Fatal("acquire returned a 4th token before any release")
	case <-time.After(200 * time.Millisecond):
	}

	// Release a pipe token (tokens[0] is the implicit slot, which cannot wake a
	// blocked reader); tokens[1] is drawn from the pipe.
	js.release(tokens[1])
	select {
	case tk := <-got:
		js.release(tk)
	case <-time.After(time.Second):
		t.Fatal("acquire did not return after a pipe token was released")
	}
	js.release(tokens[0])
	js.release(tokens[2])
}

func TestSetupJobserverExportsAuth(t *testing.T) {
	project := &config.Project{Variables: map[string]string{"MAKEFLAGS": "-j2"}}
	js := setupJobserver(project, &Options{Jobs: 2})
	if js == nil {
		t.Skip("jobserver unsupported on this platform")
	}
	defer js.close()
	if !strings.Contains(project.Variables["MAKEFLAGS"], "--jobserver-auth=") {
		t.Fatalf("MAKEFLAGS = %q, want jobserver auth appended", project.Variables["MAKEFLAGS"])
	}
}
