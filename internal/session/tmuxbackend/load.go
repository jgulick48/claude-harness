package tmuxbackend

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

// Host CPU and memory load (docs/CORE_PROTOCOL.md §5.5). One shell command
// per reading prints labelled lines for the host's OS; parseLoad turns them
// into a session.HostLoad.

var _ session.LoadReader = (*Backend)(nil)

// loadMinGap is the smallest distance between two /proc/stat samples used
// for a CPU delta.
const loadMinGap = time.Second

// loadNow is the clock for readings; tests replace it.
var loadNow = time.Now

// loadCache is the Backend's previous /proc/stat sample; mu guards it.
type loadCache struct {
	mu          sync.Mutex
	have        bool
	at          time.Time
	idle, total uint64
}

// loadScript prints "os=<uname -s>" and then the labelled lines for that OS.
// POSIX sh only (macOS /bin/sh is bash 3.2, Linux's is often dash).
func loadScript() string {
	return `os=$(uname -s 2>/dev/null)
printf 'os=%s\n' "$os"
case "$os" in
Linux)
	printf 'loadavg=%s\n' "$(cat /proc/loadavg 2>/dev/null)"
	printf 'stat=%s\n' "$(grep -m1 '^cpu ' /proc/stat 2>/dev/null)"
	n=$(nproc 2>/dev/null)
	[ -n "$n" ] || n=$(grep -c '^processor' /proc/cpuinfo 2>/dev/null)
	printf 'ncpu=%s\n' "$n"
	printf 'memtotal=%s\n' "$(grep -m1 '^MemTotal:' /proc/meminfo 2>/dev/null)"
	printf 'memavail=%s\n' "$(grep -m1 '^MemAvailable:' /proc/meminfo 2>/dev/null)"
	;;
Darwin)
	printf 'ncpu=%s\n' "$(sysctl -n hw.ncpu 2>/dev/null)"
	printf 'memsize=%s\n' "$(sysctl -n hw.memsize 2>/dev/null)"
	printf 'loadavg=%s\n' "$(sysctl -n vm.loadavg 2>/dev/null)"
	vm_stat 2>/dev/null | sed 's/^/vm=/'
	printf 'pscpu=%s\n' "$(ps -A -o %cpu= 2>/dev/null | awk '{s+=$1} END {printf "%.2f", s}')"
	;;
esac
exit 0
`
}

// rawLoad is loadScript's output, parsed but not yet reduced.
type rawLoad struct {
	os       string
	load     [3]float64
	cores    int
	memTotal uint64
	memUsed  uint64
	// Linux: aggregate cpu jiffies.
	idle, total uint64
	// Darwin: summed %cpu of all processes (not yet divided by cores).
	psCPU    float64
	havePS   bool
	haveStat bool
}

func parseLoadavg(s string) ([3]float64, error) {
	var l [3]float64
	s = strings.Trim(strings.TrimSpace(s), "{}")
	f := strings.Fields(s)
	if len(f) < 3 {
		return l, fmt.Errorf("bad loadavg %q", s)
	}
	for i := range l {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return l, fmt.Errorf("bad loadavg %q", s)
		}
		l[i] = v
	}
	return l, nil
}

// parseStatCPU reads the "cpu  user nice system idle iowait irq softirq
// steal ..." line: idle is idle+iowait, total the sum of the first eight
// fields (guest times are already inside user/nice).
func parseStatCPU(line string) (idle, total uint64, err error) {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, fmt.Errorf("bad cpu line %q", line)
	}
	vals := make([]uint64, 0, 8)
	for _, x := range f[1:] {
		if len(vals) == 8 {
			break
		}
		v, e := strconv.ParseUint(x, 10, 64)
		if e != nil {
			return 0, 0, fmt.Errorf("bad cpu line %q", line)
		}
		vals = append(vals, v)
	}
	for _, v := range vals {
		total += v
	}
	idle = vals[3]
	if len(vals) > 4 {
		idle += vals[4]
	}
	return idle, total, nil
}

// meminfoKB parses "MemTotal:  16314564 kB" into bytes.
func meminfoKB(line string) (uint64, error) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return 0, fmt.Errorf("bad meminfo line %q", line)
	}
	v, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad meminfo line %q", line)
	}
	if len(f) > 2 && strings.EqualFold(f[2], "kB") {
		v *= 1024
	}
	return v, nil
}

// vmStatBytes computes used memory from vm_stat output lines (without the
// "vm=" label): (active + wired + occupied by compressor) pages x page size.
func vmStatBytes(lines []string) (uint64, error) {
	var pageSize uint64
	pages := map[string]uint64{}
	for _, l := range lines {
		if strings.HasPrefix(l, "Mach Virtual Memory Statistics") {
			if i := strings.Index(l, "page size of "); i >= 0 {
				f := strings.Fields(l[i+len("page size of "):])
				if len(f) > 0 {
					pageSize, _ = strconv.ParseUint(f[0], 10, 64)
				}
			}
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
		if err != nil {
			continue
		}
		pages[strings.Trim(k, `" `)] = n
	}
	if pageSize == 0 {
		return 0, fmt.Errorf("vm_stat: no page size")
	}
	active, ok1 := pages["Pages active"]
	wired, ok2 := pages["Pages wired down"]
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("vm_stat: missing page counts")
	}
	return (active + wired + pages["Pages occupied by compressor"]) * pageSize, nil
}

// parseLoad parses loadScript's output. An unknown OS wraps ErrUnsupported;
// anything malformed is a plain error.
func parseLoad(out string) (rawLoad, error) {
	var r rawLoad
	vals := map[string]string{}
	var vm []string
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if !ok {
			continue
		}
		if k == "vm" {
			vm = append(vm, v)
			continue
		}
		vals[k] = v
	}
	r.os = strings.TrimSpace(vals["os"])
	var err error
	switch r.os {
	case "Linux":
		if r.load, err = parseLoadavg(vals["loadavg"]); err != nil {
			return r, err
		}
		if r.idle, r.total, err = parseStatCPU(vals["stat"]); err != nil {
			return r, err
		}
		r.haveStat = true
		if r.cores, err = strconv.Atoi(strings.TrimSpace(vals["ncpu"])); err != nil || r.cores < 1 {
			return r, fmt.Errorf("bad cpu count %q", vals["ncpu"])
		}
		var avail uint64
		if r.memTotal, err = meminfoKB(vals["memtotal"]); err != nil {
			return r, err
		}
		if avail, err = meminfoKB(vals["memavail"]); err != nil {
			return r, err
		}
		if avail > r.memTotal {
			avail = r.memTotal
		}
		r.memUsed = r.memTotal - avail
	case "Darwin":
		if r.load, err = parseLoadavg(vals["loadavg"]); err != nil {
			return r, err
		}
		if r.cores, err = strconv.Atoi(strings.TrimSpace(vals["ncpu"])); err != nil || r.cores < 1 {
			return r, fmt.Errorf("bad cpu count %q", vals["ncpu"])
		}
		if r.memTotal, err = strconv.ParseUint(strings.TrimSpace(vals["memsize"]), 10, 64); err != nil || r.memTotal == 0 {
			return r, fmt.Errorf("bad memory size %q", vals["memsize"])
		}
		if r.memUsed, err = vmStatBytes(vm); err != nil {
			return r, err
		}
		if r.memUsed > r.memTotal {
			r.memUsed = r.memTotal
		}
		if v, e := strconv.ParseFloat(strings.TrimSpace(vals["pscpu"]), 64); e == nil {
			r.psCPU, r.havePS = v, true
		}
	case "":
		return r, fmt.Errorf("load: no output from host")
	default:
		return r, fmt.Errorf("%w: cannot read load on %s", session.ErrUnsupported, r.os)
	}
	return r, nil
}

// ReadLoad implements session.LoadReader.
func (b *Backend) ReadLoad(ctx context.Context) (session.HostLoad, error) {
	sh, ok := b.ops.(shellOps)
	if !ok {
		return session.HostLoad{}, fmt.Errorf("%w: host cannot report load", session.ErrUnsupported)
	}
	out, err := sh.shell(ctx, loadScript())
	if err != nil {
		return session.HostLoad{}, fmt.Errorf("load: %w", err)
	}
	now := loadNow()
	r, err := parseLoad(string(out))
	if err != nil {
		return session.HostLoad{}, err
	}
	l := session.HostLoad{At: now.UnixMilli(), Cores: r.cores,
		Load1: r.load[0], Load5: r.load[1], Load15: r.load[2], MemTotal: r.memTotal, MemUsed: r.memUsed}
	switch {
	case r.haveStat:
		l.CPUPct = b.load.cpuDelta(now, r.idle, r.total)
	case r.havePS:
		v := clampPct(r.psCPU / float64(r.cores))
		l.CPUPct = &v
	}
	return l, nil
}

// cpuDelta returns the CPU percentage since the previous sample, or nil when
// there is none, the counters went backwards, or the sample is less than
// loadMinGap newer than the baseline (which is then kept).
func (c *loadCache) cpuDelta(now time.Time, idle, total uint64) *float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.have || now.Before(c.at) || idle < c.idle || total < c.total {
		c.have, c.at, c.idle, c.total = true, now, idle, total
		return nil
	}
	if now.Sub(c.at) < loadMinGap {
		return nil
	}
	dIdle, dTotal := idle-c.idle, total-c.total
	c.at, c.idle, c.total = now, idle, total
	if dTotal == 0 {
		return nil
	}
	v := clampPct(100 * (1 - float64(dIdle)/float64(dTotal)))
	return &v
}
