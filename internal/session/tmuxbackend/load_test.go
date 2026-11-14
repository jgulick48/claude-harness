package tmuxbackend

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

const linuxLoadOut = `os=Linux
loadavg=0.52 0.61 0.70 2/512 12345
stat=cpu  4705 356 584 3699 23 23 0 0 0 0
ncpu=8
memtotal=MemTotal:       16314564 kB
memavail=MemAvailable:    9876543 kB
`

const darwinLoadOut = `os=Darwin
ncpu=10
memsize=17179869184
loadavg={ 2.10 1.95 1.80 }
vm=Mach Virtual Memory Statistics: (page size of 16384 bytes)
vm=Pages free:                               11111.
vm=Pages active:                            200000.
vm=Pages inactive:                          190000.
vm=Pages wired down:                        100000.
vm=Pages occupied by compressor:             50000.
vm=Pages purgeable:                           9000.
pscpu=250.00
`

func TestParseLoadLinux(t *testing.T) {
	r, err := parseLoad(linuxLoadOut)
	if err != nil {
		t.Fatal(err)
	}
	if r.load != [3]float64{0.52, 0.61, 0.70} || r.cores != 8 || r.memTotal != 16314564*1024 ||
		r.memUsed != (16314564-9876543)*1024 || r.idle != 3699+23 || r.total != 4705+356+584+3699+23+23 || !r.haveStat {
		t.Fatalf("%+v", r)
	}
}

func TestParseLoadDarwin(t *testing.T) {
	r, err := parseLoad(darwinLoadOut)
	if err != nil {
		t.Fatal(err)
	}
	if r.load != [3]float64{2.10, 1.95, 1.80} || r.cores != 10 || r.memTotal != 17179869184 ||
		r.memUsed != 350000*16384 || !r.havePS || r.psCPU != 250 {
		t.Fatalf("%+v", r)
	}
}

func TestParseLoadErrors(t *testing.T) {
	if _, err := parseLoad("os=FreeBSD\n"); !errors.Is(err, session.ErrUnsupported) {
		t.Fatalf("freebsd: %v", err)
	}
	for _, bad := range []string{
		"",
		"os=Linux\n",
		strings.Replace(linuxLoadOut, "0.52 0.61 0.70", "x", 1),
		strings.Replace(linuxLoadOut, "cpu  4705", "cpu  abc", 1),
		strings.Replace(linuxLoadOut, "ncpu=8", "ncpu=", 1),
		strings.Replace(linuxLoadOut, "MemAvailable:    9876543 kB", "", 1),
		strings.Replace(darwinLoadOut, "(page size of 16384 bytes)", "", 1),
		strings.Replace(darwinLoadOut, "memsize=17179869184", "memsize=", 1),
	} {
		_, err := parseLoad(bad)
		if err == nil || errors.Is(err, session.ErrUnsupported) {
			t.Errorf("want plain error for %q, got %v", bad, err)
		}
	}
}

func TestCPUDelta(t *testing.T) {
	var c loadCache
	t0 := time.Unix(1000, 0)
	if c.cpuDelta(t0, 100, 200) != nil {
		t.Fatal("first sample must be nil")
	}
	// Too close: nil, baseline kept.
	if c.cpuDelta(t0.Add(500*time.Millisecond), 150, 300) != nil {
		t.Fatal("close sample must be nil")
	}
	// Delta against the t0 baseline: idle +50 of total +100 -> 50%.
	v := c.cpuDelta(t0.Add(2*time.Second), 150, 300)
	if v == nil || *v != 50 {
		t.Fatalf("got %v", v)
	}
	// Counter reset: nil, new baseline.
	if c.cpuDelta(t0.Add(4*time.Second), 10, 20) != nil {
		t.Fatal("reset must be nil")
	}
	v = c.cpuDelta(t0.Add(6*time.Second), 10, 120)
	if v == nil || *v != 100 {
		t.Fatalf("got %v", v)
	}
}

type loadOps struct {
	hostOps
	out string
	err error
}

func (o loadOps) shell(context.Context, string) ([]byte, error) { return []byte(o.out), o.err }

func TestReadLoadWithFixtures(t *testing.T) {
	now := time.Unix(5000, 0)
	old := loadNow
	loadNow = func() time.Time { return now }
	t.Cleanup(func() { loadNow = old })

	b := &Backend{ops: loadOps{out: linuxLoadOut}}
	l, err := b.ReadLoad(context.Background())
	if err != nil || l.CPUPct != nil || l.At != 5000000 || l.Cores != 8 {
		t.Fatalf("%+v %v", l, err)
	}
	now = now.Add(2 * time.Second)
	b.ops = loadOps{out: strings.Replace(linuxLoadOut, "3699 23", "3799 23", 1)}
	if l, err = b.ReadLoad(context.Background()); err != nil || l.CPUPct == nil || *l.CPUPct != 0 {
		t.Fatalf("%+v %v", l, err)
	}

	d := &Backend{ops: loadOps{out: darwinLoadOut}}
	if l, err = d.ReadLoad(context.Background()); err != nil || l.CPUPct == nil || *l.CPUPct != 25 {
		t.Fatalf("%+v %v", l, err)
	}
	d.ops = loadOps{out: strings.Replace(darwinLoadOut, "250.00", "5000.00", 1)}
	if l, _ = d.ReadLoad(context.Background()); *l.CPUPct != 100 {
		t.Fatalf("cap: %v", *l.CPUPct)
	}

	u := &Backend{ops: loadOps{out: "os=SunOS\n"}}
	if _, err = u.ReadLoad(context.Background()); !errors.Is(err, session.ErrUnsupported) {
		t.Fatalf("%v", err)
	}
}

// The script must parse in a POSIX sh, and avoid here-docs and bashisms.
func TestLoadScriptSyntax(t *testing.T) {
	s := loadScript()
	if strings.Contains(s, "<<") || strings.Contains(s, "[[") {
		t.Fatal("script uses a here-doc or [[")
	}
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	for _, sh := range []string{"sh", "dash", "bash"} {
		p, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		cmd := exec.Command(p, "-n")
		cmd.Stdin = strings.NewReader(s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s -n: %v: %s", sh, err, out)
		}
	}
}

// Real local host: Linux in CI and locally, Darwin on macOS CI.
func TestReadLoadLocalIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	b := NewLocal(Options{Socket: "harness-load-unused", Config: "/dev/null", Home: t.TempDir()})
	ctx := context.Background()
	first, err := b.ReadLoad(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cores < 1 || first.MemTotal == 0 || first.MemUsed > first.MemTotal {
		t.Fatalf("%+v", first)
	}
	time.Sleep(1100 * time.Millisecond)
	l, err := b.ReadLoad(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if l.CPUPct == nil || *l.CPUPct < 0 || *l.CPUPct > 100 {
		t.Fatalf("cpu: %+v", l)
	}
	if l.Cores < 1 || l.MemTotal == 0 || l.MemUsed > l.MemTotal || l.At <= first.At {
		t.Fatalf("%+v", l)
	}
}
