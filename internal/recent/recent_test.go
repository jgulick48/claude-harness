package recent

import (
	"errors"
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
	if want := `"version": 2`; !strings.Contains(string(raw), want) {
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

func TestPinUnpin(t *testing.T) {
	s, _ := open(t)
	if got := s.Pinned("h"); got == nil || len(got) != 0 {
		t.Fatalf("empty pinned = %#v", got)
	}
	s.Add("h", "~/a")
	s.Add("h", "~/b")
	s.Add("h", "~/c")
	// c, b, a
	if err := s.Pin("h", "~/b/"); err != nil { // normalized
		t.Fatal(err)
	}
	if err := s.Pin("h", "~/a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Pin("h", "~/b"); err != nil { // no-op keeps position
		t.Fatal(err)
	}
	if got := s.Pinned("h"); !reflect.DeepEqual(got, []string{"~/b", "~/a"}) {
		t.Fatalf("pinned %v", got)
	}
	if got := s.List("h"); !reflect.DeepEqual(got, []string{"~/c"}) {
		t.Fatalf("recent %v", got)
	}
	// Add of a pinned dir leaves recent alone.
	s.Add("h", "~/a")
	if got := s.List("h"); !reflect.DeepEqual(got, []string{"~/c"}) {
		t.Fatalf("recent after add %v", got)
	}
	// Unpin returns it to the front of recent.
	s.Unpin("h", "~/b/")
	s.Unpin("h", "~/nope") // no-op
	if got := s.Pinned("h"); !reflect.DeepEqual(got, []string{"~/a"}) {
		t.Fatalf("pinned %v", got)
	}
	if got := s.List("h"); !reflect.DeepEqual(got, []string{"~/b", "~/c"}) {
		t.Fatalf("recent %v", got)
	}
	// Other hosts are independent; Forget drops both lists.
	if len(s.Pinned("other")) != 0 {
		t.Fatal("other host")
	}
	s.Forget("h")
	if len(s.Pinned("h")) != 0 || len(s.List("h")) != 0 {
		t.Fatal("forget")
	}
}

func TestPinCap(t *testing.T) {
	s, _ := open(t)
	for i := 0; i < MaxPinnedPerHost; i++ {
		if err := s.Pin("h", fmt.Sprintf("~/d%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Pin("h", "~/extra"); !errors.Is(err, ErrTooManyPinned) {
		t.Fatalf("err %v", err)
	}
	if err := s.Pin("h", "~/d3"); err != nil { // already pinned: fine at the cap
		t.Fatal(err)
	}
	if len(s.Pinned("h")) != MaxPinnedPerHost {
		t.Fatal("cap")
	}
	// Pinned dirs don't count against the recent limit.
	for i := 0; i < 15; i++ {
		s.Add("h", fmt.Sprintf("~/r%d", i))
	}
	if len(s.List("h")) != MaxPerHost {
		t.Fatalf("recent %v", s.List("h"))
	}
}

func TestUnpinTruncatesRecent(t *testing.T) {
	s, _ := open(t)
	if err := s.Pin("h", "~/p"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxPerHost; i++ {
		s.Add("h", fmt.Sprintf("~/r%d", i))
	}
	s.Unpin("h", "~/p")
	l := s.List("h")
	if len(l) != MaxPerHost || l[0] != "~/p" || l[MaxPerHost-1] != "~/r1" {
		t.Fatalf("got %v", l)
	}
}

func TestPinPersistenceAndV1(t *testing.T) {
	s, p := open(t)
	s.Add("h", "~/a")
	s.Add("h", "~/b")
	if err := s.Pin("h", "~/a"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), `"version": 2`) || !strings.Contains(string(raw), `"pinnedDirs"`) {
		t.Fatalf("file %s", raw)
	}
	s2, err := New(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s2.Pinned("h"), []string{"~/a"}) || !reflect.DeepEqual(s2.List("h"), []string{"~/b"}) {
		t.Fatalf("reload %v %v", s2.Pinned("h"), s2.List("h"))
	}

	// v1 files (no pinned dirs) still load.
	p1 := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p1, []byte(`{"version":1,"recentDirs":{"h":["~/x","~/y"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s3, err := New(p1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s3.List("h"), []string{"~/x", "~/y"}) || len(s3.Pinned("h")) != 0 {
		t.Fatalf("v1 %v", s3.List("h"))
	}
	// Overlap in a hand-edited file: pinned wins.
	p2 := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p2, []byte(`{"version":2,"recentDirs":{"h":["~/x","~/y"]},"pinnedDirs":{"h":["~/x"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s4, _ := New(p2, nil)
	if !reflect.DeepEqual(s4.List("h"), []string{"~/y"}) || !reflect.DeepEqual(s4.Pinned("h"), []string{"~/x"}) {
		t.Fatalf("v2 overlap %v %v", s4.List("h"), s4.Pinned("h"))
	}
	// Unknown versions are still corrupt.
	p3 := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(p3, []byte(`{"version":3}`), 0o600)
	s5, _ := New(p3, nil)
	if !s5.corrupt {
		t.Fatal("v3 accepted")
	}
}
