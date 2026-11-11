package recent

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

var _ session.RecentDirs = (*Store)(nil)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "state.json")
	s, err := New(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestMRUDedupeNormalize(t *testing.T) {
	s, _ := open(t)
	if got := s.List("h"); got == nil || len(got) != 0 {
		t.Fatalf("empty list = %#v", got)
	}
	s.Add("h", "~/a")
	s.Add("h", "~/b/")
	s.Add("h", "")
	s.Add("h", "~/a")
	s.Add("h", "/")
	s.Add("other", "~/z")
	want := []string{"/", "~/a", "~", "~/b"}
	if got := s.List("h"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	s.Forget("h")
	if len(s.List("h")) != 0 || len(s.List("other")) != 1 {
		t.Fatal("forget")
	}
}

func TestCap(t *testing.T) {
	s, _ := open(t)
	for i := 0; i < 15; i++ {
		s.Add("h", fmt.Sprintf("~/d%d", i))
	}
	l := s.List("h")
	if len(l) != MaxPerHost || l[0] != "~/d14" || l[9] != "~/d5" {
		t.Fatalf("got %v", l)
	}
}

func TestPersistence(t *testing.T) {
	s, p := open(t)
	s.Add("h", "~/a")
	s.Add("h", "/srv/x")
	s.Add("g", "~")
	s.Forget("g")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// Unix permission bits don't exist on Windows (os.Stat reports 0666).
	if runtime.GOOS != "windows" {
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
		if di, _ := os.Stat(filepath.Dir(p)); di.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v", di.Mode())
		}
	}
	s2, err := New(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.List("h"); !reflect.DeepEqual(got, []string{"/srv/x", "~/a"}) {
		t.Fatalf("got %v", got)
	}
	if len(s2.List("g")) != 0 {
		t.Fatal("g not forgotten")
	}
	raw, _ := os.ReadFile(p)
	if want := `"version": 1`; !strings.Contains(string(raw), want) {
		t.Fatalf("file %s", raw)
	}
}

func TestCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p, []byte("{garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List("h")) != 0 {
		t.Fatal("not empty")
	}
	s.Forget("h") // no-op, must not touch the file
	if b, _ := os.ReadFile(p); string(b) != "{garbage" {
		t.Fatalf("bad file overwritten before Add: %q", b)
	}
	s.Add("h", "~/a")
	if b, _ := os.ReadFile(p + ".corrupt"); string(b) != "{garbage" {
		t.Fatalf("corrupt copy: %q", b)
	}
	s2, _ := New(p, nil)
	if got := s2.List("h"); !reflect.DeepEqual(got, []string{"~/a"}) {
		t.Fatalf("got %v", got)
	}
}

func TestConcurrent(t *testing.T) {
	s, p := open(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s.Add("h", fmt.Sprintf("~/d%d", (i*7+j)%13))
				s.List("h")
				if j%9 == 0 {
					s.Forget("x")
				}
			}
		}()
	}
	wg.Wait()
	if n := len(s.List("h")); n != MaxPerHost {
		t.Fatalf("len %d", n)
	}
	s2, _ := New(p, nil)
	if !reflect.DeepEqual(s2.List("h"), s.List("h")) {
		t.Fatal("persisted state differs")
	}
}
