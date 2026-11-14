package tmuxbackend

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

var usageT0 = time.Unix(1_800_000_000, 0)

func fp(v float64) *float64 { return &v }

func TestParseUsage(t *testing.T) {
	now := usageT0
	future := func(d int64) string { return strconv.FormatInt(now.Unix()+d, 10) }
	past := strconv.FormatInt(now.Unix()-5, 10)
	full := `{"model":{"id":"x","display_name":"Opus"},"cost":{"total_cost_usd":0.0123},
	 "context_window":{"used_percentage":42.5,"context_window_size":200000},
	 "rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":` + future(100) + `},
	  "seven_day":{"used_percentage":141.2,"resets_at":` + future(200) + `},
	  "spend_limit":{"used_percentage":62.8,"resets_at":` + future(300) + `,"used_usd":314.12,"limit_usd":500,"period":"monthly"}}}`
	for _, c := range []struct {
		name, in string
		want     *session.Usage
		err      bool
	}{
		{"full", full, &session.Usage{At: 7, Model: "Opus", CostUSD: fp(0.0123), ContextPct: fp(42.5), Limits: &session.UsageLimits{
			FiveHour: &session.UsageWindow{UsedPct: 23.5, ResetsAt: now.Unix() + 100},
			SevenDay: &session.UsageWindow{UsedPct: 100, ResetsAt: now.Unix() + 200}, // clamped
			Spend: &session.SpendWindow{UsageWindow: session.UsageWindow{UsedPct: 62.8, ResetsAt: now.Unix() + 300},
				UsedUSD: fp(314.12), LimitUSD: fp(500), Period: "monthly"},
		}}, false},
		{"empty object", `{}`, &session.Usage{At: 7}, false},
		{"model only", `{"model":{"display_name":"Sonnet"}}`, &session.Usage{At: 7, Model: "Sonnet"}, false},
		{"model as string is tolerated", `{"model":"opus","cost":{"total_cost_usd":1}}`, &session.Usage{At: 7, CostUSD: fp(1)}, false},
		{"negative context clamped", `{"context_window":{"used_percentage":-3}}`, &session.Usage{At: 7, ContextPct: fp(0)}, false},
		{"expired windows dropped", `{"rate_limits":{"five_hour":{"used_percentage":50,"resets_at":` + past + `},
		  "seven_day":{"used_percentage":10,"resets_at":` + future(10) + `}}}`,
			&session.Usage{At: 7, Limits: &session.UsageLimits{SevenDay: &session.UsageWindow{UsedPct: 10, ResetsAt: now.Unix() + 10}}}, false},
		{"all expired: no limits", `{"rate_limits":{"five_hour":{"used_percentage":50,"resets_at":` + past + `}}}`, &session.Usage{At: 7}, false},
		{"window missing fields dropped", `{"rate_limits":{"five_hour":{"used_percentage":50},"seven_day":{"resets_at":` + future(5) + `}}}`, &session.Usage{At: 7}, false},
		{"rate_limits empty", `{"rate_limits":{}}`, &session.Usage{At: 7}, false},
		{"null fields", `{"model":null,"cost":null,"context_window":null,"rate_limits":null}`, &session.Usage{At: 7}, false},
		{"garbage", `not json`, nil, true},
		{"wrong type", `{"cost":"cheap"}`, nil, true},
		{"array", `[1,2]`, nil, true},
		{"empty", ``, nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseUsage(7, c.in, now)
			if (err != nil) != c.err {
				t.Fatalf("err = %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseUsagePanes(t *testing.T) {
	sep := fieldSep
	got := parseUsagePanes("10" + sep + "$1" + sep + "%3" + sep + "1" + sep + "1\n" +
		"10" + sep + "$1" + sep + "%4" + sep + "0" + sep + "1\n" +
		"10" + sep + "$2" + sep + "%5" + sep + "1" + sep + "0\n" +
		"junk\n\n10" + sep + "$2" + sep + "5" + sep + "1" + sep + "1\n" +
		"x" + sep + "$2" + sep + "%6" + sep + "1" + sep + "1\n")
	want := []usagePane{{10, "$1", 3, true}, {10, "$1", 4, false}, {10, "$2", 5, false}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestParseUsageFiles(t *testing.T) {
	b := NewLocal(Options{Home: t.TempDir()})
	body := `{"model":{"display_name":"Opus"}}`
	out := strings.Join([]string{
		"10-3\t100\t" + body,
		"10-4\t101\t{bad",         // garbage
		"10-5.1234\t100\t" + body, // temp file
		"notes\t100\t" + body,
		"10-6\tabc\t" + body, // bad time
		"10-7\t100",          // no body
		"",
		"11-3\t102\t" + body,
	}, "\n")
	got := b.parseUsageFiles(out, usageT0)
	if len(got) != 2 || got[[2]int{10, 3}].At != 100 || got[[2]int{11, 3}].At != 102 || got[[2]int{10, 3}].Model != "Opus" {
		t.Errorf("got %+v", got)
	}
}

func TestPickUsage(t *testing.T) {
	r := func(at int64) session.Usage { return session.Usage{At: at} }
	panes := []usagePane{
		{1, "$1", 1, false}, {1, "$1", 2, true}, {1, "$1", 3, false}, // selected pane 2
		{1, "$2", 4, false}, {1, "$2", 5, false}, {1, "$2", 6, true}, // selected pane 6 has no reading
		{1, "$3", 7, true},                        // no readings at all
		{2, "$4", 8, true},                        // other server
		{1, "$5", 9, false}, {1, "$5", 10, false}, // tie: higher pane wins
	}
	readings := map[[2]int]session.Usage{
		{1, 1}: r(50), {1, 2}: r(10), {1, 3}: r(99),
		{1, 4}: r(20), {1, 5}: r(30),
		{2, 8}: r(1), {1, 8}: r(1),
		{1, 9}: r(5), {1, 10}: r(5),
		{3, 6}: r(500), // another server's file never matches
	}
	got := pickUsage(panes, readings, 1)
	want := map[string]session.Usage{"$1": r(10), "$2": r(30), "$5": r(5)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// fakeUsageOps counts reads through a real localOps with a fixed config dir.
func TestUsageScriptLocal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := filepath.Join(t.TempDir(), "us age")
	run := func() string {
		out, err := localOps{}.shell(context.Background(), usageReadScript(dir))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if out := run(); out != "" { // missing dir: silent
		t.Fatalf("missing dir printed %q", out)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	w := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	w("10-1", "100\t{\"a\":1}\nsecond line\n")
	w("10-2", "101\t{}") // no trailing newline
	old := w("10-3", "1\t{}\n")
	w("10-4.99", "1\t{}\n") // temp file: listed, but ignored by the parser
	os.Symlink(filepath.Join(dir, "10-1"), filepath.Join(dir, "10-9"))
	os.Mkdir(filepath.Join(dir, "10-8"), 0o700)
	w("10-10", "5\t"+strings.Repeat("x", 100000)+"\n")
	oldT := time.Now().Add(-25 * time.Hour)
	os.Chtimes(old, oldT, oldT)
	out := run()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	got := map[string]string{}
	for _, l := range lines {
		n, rest, _ := strings.Cut(l, "\t")
		got[n] = rest
	}
	if got["10-1"] != "100\t{\"a\":1}" || got["10-2"] != "101\t{}" {
		t.Errorf("content: %q", out[:min(len(out), 300)])
	}
	if _, ok := got["10-3"]; ok {
		t.Error("old file listed")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old file not deleted")
	}
	if _, ok := got["10-9"]; ok {
		t.Error("symlink read")
	}
	if _, ok := got["10-8"]; ok {
		t.Error("directory read")
	}
	if n := len(got["10-10"]); n == 0 || n > 65536 {
		t.Errorf("long file len %d (cap 65536)", n)
	}
}

// A backend with no tmux server involved: readUsage is driven through
// applyUsage with a fake runner via the real tmux, so throttling is tested in
// the integration test; here the clock and cache logic are checked directly.
func TestApplyUsageThrottleAndCache(t *testing.T) {
	b := NewLocal(Options{Home: t.TempDir(), Socket: "harness-usage-unused", Config: "/dev/null"})
	now := usageT0
	old := usageNow
	usageNow = func() time.Time { return now }
	t.Cleanup(func() { usageNow = old })

	list := func() []session.Session {
		return []session.Session{
			{ID: "$1", ClaudeStatus: "idle"},
			{ID: "$2", ClaudeStatus: ""},
			{ID: "$3", ClaudeStatus: "working", RunAs: "bob"},
		}
	}
	// No candidates: nothing is read and the clock stays unset.
	b.applyUsage(context.Background(), []session.Session{{ID: "$1"}})
	if !b.usage.readAt.IsZero() {
		t.Fatal("read with no Claude sessions")
	}
	b.usage.readAt = now // pretend a read just happened
	b.usage.readings = map[string]session.Usage{"$1": {At: 5}, "$2": {At: 6}, "$3": {At: 7}}
	l := list()
	b.applyUsage(context.Background(), l)
	if l[0].Usage == nil || l[0].Usage.At != 5 || l[1].Usage != nil || l[2].Usage != nil {
		t.Errorf("cache apply: %+v", l)
	}
	// Within the interval the cache stays; no read attempted (readAt unchanged).
	now = now.Add(usageEvery - time.Second)
	b.applyUsage(context.Background(), list())
	if !b.usage.readAt.Equal(usageT0) {
		t.Error("re-read within the interval")
	}
	// After the interval a read is attempted (it fails here: no tmux server),
	// readAt moves, and the cache survives the failure.
	now = now.Add(2 * time.Second)
	l = list()
	b.applyUsage(context.Background(), l)
	if !b.usage.readAt.Equal(now) {
		t.Error("no read after the interval")
	}
	if l[0].Usage == nil || l[0].Usage.At != 5 {
		t.Error("cache lost after a failed read")
	}
}
