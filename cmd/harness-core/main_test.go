package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/version"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want int
	}{
		{"non-loopback", []string{"--listen", "0.0.0.0:0"}, map[string]string{"HARNESS_TOKEN": "x"}, 2},
		{"missing token", nil, nil, 2},
		{"bind failure", []string{"--listen", "127.0.0.1:99999"}, map[string]string{"HARNESS_TOKEN": "x"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			got := run(context.Background(), tc.args, strings.NewReader(""), &out, &errb, env(tc.env))
			if got != tc.want {
				t.Fatalf("exit %d, want %d (stderr: %s)", got, tc.want, errb.String())
			}
			if out.Len() != 0 {
				t.Fatalf("unexpected stdout: %q", out.String())
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if got := run(context.Background(), []string{"--version"}, strings.NewReader(""), &out, &errb, env(nil)); got != 0 {
		t.Fatalf("exit %d", got)
	}
	if strings.TrimSpace(out.String()) != version.Version {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestRunHappyPath(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- run(context.Background(), []string{"--listen", "127.0.0.1:0"}, stdinR, stdoutW, io.Discard, env(map[string]string{"HARNESS_TOKEN": "abc"}))
		stdoutW.Close()
	}()

	line, err := bufio.NewReader(stdoutR).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Type     string `json:"type"`
		Addr     string `json:"addr"`
		Port     int    `json:"port"`
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal([]byte(line), &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Type != "ready" || ready.Port == 0 || ready.Protocol != 1 || ready.Version != version.Version {
		t.Fatalf("ready: %+v", ready)
	}
	go io.Copy(io.Discard, stdoutR)

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/healthz", ready.Port), nil)
	req.Header.Set("Authorization", "Bearer abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthz %d", resp.StatusCode)
	}

	stdinW.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after stdin EOF")
	}
}
