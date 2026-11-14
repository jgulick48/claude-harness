package tmuxbackend

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

// Claude usage (docs/CORE_PROTOCOL.md §5.4). The status line wrapper
// (claudehooks.StatusLineScript) writes the JSON Claude passes to its status
// line command to <config dir>/claude-harness/usage/<tmux server pid>-<pane
// number>. List reads those files every usageEvery and attaches the reading
// of each session's pane to the session while Claude runs there.

// usageEvery is the minimum time between two reads of the usage files.
const usageEvery = 10 * time.Second

// usageNow is the clock for throttling and window expiry; tests replace it.
var usageNow = time.Now

// usageCache is the Backend's usage state; mu guards all of it.
type usageCache struct {
	mu       sync.Mutex
	readAt   time.Time                // when the files were last read (or tried)
	cfgDir   string                   // Claude config dir, cached after the first success
	readings map[string]session.Usage // session id -> chosen reading
}

// usagePane is one row of list-panes.
type usagePane struct {
	pid      int
	session  string
	pane     int
	selected bool // the active pane of the active window
}

const usagePanesFormat = "#{pid}" + fieldSep + "#{session_id}" + fieldSep + "#{pane_id}" +
	fieldSep + "#{pane_active}" + fieldSep + "#{window_active}"

func parseUsagePanes(out string) []usagePane {
	var panes []usagePane
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), fieldSep)
		if len(f) != 5 || !strings.HasPrefix(f[2], "%") {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		n, err2 := strconv.Atoi(f[2][1:])
		if err1 != nil || err2 != nil {
			continue
		}
		panes = append(panes, usagePane{pid: pid, session: f[1], pane: n, selected: f[3] == "1" && f[4] == "1"})
	}
	return panes
}

// usageReadScript prints "<name>\t<first line>" for each usage file, after
// deleting those not modified for a day. Portable to GNU and BSD find.
func usageReadScript(dir string) string {
	return `d=` + tmux.ShellQuote(dir) + `
[ -d "$d" ] || exit 0
find "$d" -maxdepth 1 -type f -mmin +1440 -exec rm -f {} + 2>/dev/null
for f in "$d"/*; do
	[ -f "$f" ] && [ ! -L "$f" ] || continue
	n=${f##*/}
	line=$(head -c 65536 "$f" 2>/dev/null | head -n 1)
	printf '%s\t%s\n' "$n" "$line"
done
exit 0
`
}

var usageFileRe = regexp.MustCompile(`^([0-9]+)-([0-9]+)$`)

// parseUsageFiles parses usageReadScript's output into readings keyed by
// (server pid, pane number), skipping malformed lines.
func (b *Backend) parseUsageFiles(out string, now time.Time) map[[2]int]session.Usage {
	got := map[[2]int]session.Usage{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		name, rest, _ := strings.Cut(line, "\t")
		m := usageFileRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		pid, err1 := strconv.Atoi(m[1])
		pane, err2 := strconv.Atoi(m[2])
		at, body, _ := strings.Cut(rest, "\t")
		atN, err3 := strconv.ParseInt(at, 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || atN <= 0 {
			b.log.Debug("usage: malformed file line", "file", name)
			continue
		}
		u, err := parseUsage(atN, body, now)
		if err != nil {
			b.log.Debug("usage: unparseable reading", "file", name, "err", err)
			continue
		}
		got[[2]int{pid, pane}] = *u
	}
	return got
}

type rawWindow struct {
	UsedPct  *float64 `json:"used_percentage"`
	ResetsAt *float64 `json:"resets_at"`
}

type rawUsage struct {
	Model json.RawMessage `json:"model"`
	Cost  *struct {
		Total *float64 `json:"total_cost_usd"`
	} `json:"cost"`
	Context *struct {
		Used *float64 `json:"used_percentage"`
	} `json:"context_window"`
	Limits *struct {
		FiveHour *rawWindow `json:"five_hour"`
		SevenDay *rawWindow `json:"seven_day"`
		Spend    *struct {
			rawWindow
			UsedUSD  *float64 `json:"used_usd"`
			LimitUSD *float64 `json:"limit_usd"`
			Period   string   `json:"period"`
		} `json:"spend_limit"`
	} `json:"rate_limits"`
}

func clampPct(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

// window converts a raw window; it is nil when the percentage or reset time
// is missing or the window has already reset.
func (w *rawWindow) window(now time.Time) *session.UsageWindow {
	if w == nil || w.UsedPct == nil || w.ResetsAt == nil || int64(*w.ResetsAt) <= now.Unix() {
		return nil
	}
	return &session.UsageWindow{UsedPct: clampPct(*w.UsedPct), ResetsAt: int64(*w.ResetsAt)}
}

// parseUsage parses one status line JSON document read at unix time at.
func parseUsage(at int64, body string, now time.Time) (*session.Usage, error) {
	var r rawUsage
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return nil, err
	}
	u := &session.Usage{At: at}
	if len(r.Model) > 0 {
		var m struct {
			DisplayName string `json:"display_name"`
		}
		if json.Unmarshal(r.Model, &m) == nil {
			u.Model = m.DisplayName
		}
	}
	if r.Cost != nil {
		u.CostUSD = r.Cost.Total
	}
	if r.Context != nil && r.Context.Used != nil {
		v := clampPct(*r.Context.Used)
		u.ContextPct = &v
	}
	if r.Limits != nil {
		var l session.UsageLimits
		l.FiveHour = r.Limits.FiveHour.window(now)
		l.SevenDay = r.Limits.SevenDay.window(now)
		if s := r.Limits.Spend; s != nil {
			if w := s.window(now); w != nil {
				l.Spend = &session.SpendWindow{UsageWindow: *w, UsedUSD: s.UsedUSD, LimitUSD: s.LimitUSD, Period: s.Period}
			}
		}
		if l.FiveHour != nil || l.SevenDay != nil || l.Spend != nil {
			u.Limits = &l
		}
	}
	return u, nil
}

// pickUsage chooses one reading per session among the panes of the server
// pid: the selected pane's if it has one, else the newest (ties: the higher
// pane number).
func pickUsage(panes []usagePane, readings map[[2]int]session.Usage, pid int) map[string]session.Usage {
	type best struct {
		u        session.Usage
		pane     int
		selected bool
	}
	picked := map[string]best{}
	for _, p := range panes {
		if p.pid != pid {
			continue
		}
		u, ok := readings[[2]int{p.pid, p.pane}]
		if !ok {
			continue
		}
		cur, have := picked[p.session]
		switch {
		case !have,
			p.selected && !cur.selected,
			p.selected == cur.selected && !cur.selected && (u.At > cur.u.At || u.At == cur.u.At && p.pane > cur.pane):
			picked[p.session] = best{u, p.pane, p.selected}
		}
	}
	out := make(map[string]session.Usage, len(picked))
	for id, p := range picked {
		out[id] = p.u
	}
	return out
}

// readUsage reads the usage files and maps them to sessions. ok is false when
// the host can't be asked (the cache is then left alone).
func (b *Backend) readUsage(ctx context.Context) (map[string]session.Usage, bool) {
	sh, ok := b.ops.(shellOps)
	if !ok {
		return nil, false
	}
	ops, ok := b.ops.(hookOps)
	if !ok {
		return nil, false
	}
	if b.usage.cfgDir == "" {
		dir, err := ops.configDir(ctx)
		if err != nil {
			b.log.Debug("usage: config dir unavailable", "err", err)
			return nil, false
		}
		b.usage.cfgDir = dir
	}
	out, err := b.run.Output(ctx, "list-panes", "-a", "-F", usagePanesFormat)
	if err != nil {
		b.log.Debug("usage: listing panes failed", "err", err)
		return nil, false
	}
	panes := parseUsagePanes(string(out))
	if len(panes) == 0 {
		return map[string]session.Usage{}, true
	}
	files, err := sh.shell(ctx, usageReadScript(ops.join(b.usage.cfgDir, hooksSubdir, usageSubdir)))
	if err != nil {
		b.log.Debug("usage: reading files failed", "err", err)
		return nil, false
	}
	return pickUsage(panes, b.parseUsageFiles(string(files), usageNow()), panes[0].pid), true
}

// applyUsage sets Usage on the sessions where Claude runs (not run-as ones),
// refreshing the cached readings first when usageEvery has passed. The cache
// is applied on every call so a session keeps its usage between reads.
func (b *Backend) applyUsage(ctx context.Context, list []session.Session) {
	want := false
	for _, s := range list {
		want = want || s.ClaudeStatus != "" && s.RunAs == ""
	}
	if !want {
		return
	}
	b.usage.mu.Lock()
	defer b.usage.mu.Unlock()
	if now := usageNow(); b.usage.readAt.IsZero() || now.Sub(b.usage.readAt) >= usageEvery || now.Before(b.usage.readAt) {
		b.usage.readAt = now // failures are throttled too
		if r, ok := b.readUsage(ctx); ok {
			b.usage.readings = r
		}
	}
	for i := range list {
		if list[i].ClaudeStatus == "" || list[i].RunAs != "" {
			continue
		}
		if u, ok := b.usage.readings[list[i].ID]; ok {
			list[i].Usage = &u
		}
	}
}
