package tmuxbackend

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestMain doubles as a fake Claude: realClaudeStub copies this test binary
// to a file named "claude", so the process's comm really is "claude". Run
// under that name with a number of seconds, it just sleeps.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "claude" {
		secs := 60
		if len(os.Args) > 1 {
			if n, err := strconv.Atoi(os.Args[1]); err == nil {
				secs = n
			}
		}
		time.Sleep(time.Duration(secs) * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
