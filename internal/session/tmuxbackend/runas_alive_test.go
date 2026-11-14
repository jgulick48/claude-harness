package tmuxbackend

import "testing"

func TestParsePS(t *testing.T) {
	in := "    1     0 systemd         /sbin/init splash\n" +
		"  200   100 claude          claude --settings /tmp/x s\n" +
		"garbage\n\n  abc def ghi jkl\n  300 1 sh\n  301 1 sh sh\n"
	got := parsePS(in)
	want := []proc{
		{1, 0, "systemd", "/sbin/init splash"},
		{200, 100, "claude", "claude --settings /tmp/x s"},
		{300, 1, "sh", ""},
		{301, 1, "sh", "sh"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestClaudeAlive(t *testing.T) {
	base := []proc{{1, 0, "init", "/sbin/init"}, {10, 1, "tmux", "tmux"}}
	with := func(extra ...proc) []proc { return append(append([]proc{}, base...), extra...) }
	cases := []struct {
		name  string
		procs []proc
		roots []int
		want  map[int]bool
	}{
		{"grandchild via sudo", with(proc{20, 10, "sudo", "sudo -iu bob"}, proc{21, 20, "sh", "sh"}, proc{22, 21, "claude", "claude"}),
			[]int{20}, map[int]bool{20: true}},
		{"second pane", with(proc{20, 10, "sh", "sh"}, proc{30, 10, "sh", "sh"}, proc{31, 30, "claude", "claude"}),
			[]int{20, 30}, map[int]bool{30: true}},
		{"absent", with(proc{20, 10, "sudo", "sudo -iu bob -- sh -c claude"}, proc{21, 20, "sh", "sh"}),
			[]int{20}, map[int]bool{}},
		{"macos comm path", with(proc{20, 10, "sudo", "sudo"}, proc{21, 20, "/Users/x/.local/share/claude/versions/2.1.323", "x"}),
			[]int{20}, map[int]bool{20: true}},
		{"args only", with(proc{20, 10, "sudo", "sudo"}, proc{21, 20, "node", "/usr/bin/claude --foo"}),
			[]int{20}, map[int]bool{20: true}},
		{"pane itself", with(proc{20, 10, "claude", "claude"}), []int{20}, map[int]bool{20: true}},
	}
	for _, c := range cases {
		got, ok := claudeAlive(c.procs, c.roots)
		if !ok {
			t.Errorf("%s: ok = false", c.name)
			continue
		}
		for _, r := range c.roots {
			if got[r] != c.want[r] {
				t.Errorf("%s: alive[%d] = %v", c.name, r, got[r])
			}
		}
	}
	if _, ok := claudeAlive([]proc{{10, 5, "claude", "claude"}}, []int{10}); ok {
		t.Error("no pid 1: ok = true")
	}
	if _, ok := claudeAlive(nil, []int{10}); ok {
		t.Error("empty: ok = true")
	}
}
