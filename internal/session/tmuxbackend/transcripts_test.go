package tmuxbackend

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

func asstLine(id, model, ts, cwd string, in, out, cc, cr int) string {
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":false,"cwd":%q,"message":{"model":%q,"id":%q,"type":"message","role":"assistant","content":[{"type":"text","text":"say \"model\":\"x\" and \"input_tokens\":999"}],"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,"iterations":[{"input_tokens":77}]}},"requestId":"req_1","type":"assistant","timestamp":%q}`,
		cwd, model, id, in, cc, cr, out, ts) + "\n"
}

func TestTranscriptScriptSyntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	script := transcriptScript("/tmp/it's/projects", map[string]int64{"/a b/c.jsonl": 5, "/bad\tpath": 1})
	if strings.Contains(script, "bad\tpath") {
		t.Error("unframeable path embedded")
	}
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
}

func TestParseTranscripts(t *testing.T) {
	out := "junk\n" +
		"U\t2026-11-14T19:37:01.614Z\tm\tmsg_x\t1\t2\t3\t4\t/orphan\n" + // before any F
		"F\t/p/a.jsonl\t100\t10\t0\n" +
		"U\t2026-11-14T19:37:01.614Z\tclaude-opus-5-5\tmsg_1\t2\t264\t14394\t24768\t/home/u/proj\n" +
		"U\tnot-a-time\tm\tmsg_2\t1\t1\t1\t1\t/x\n" +
		"U\t2026-11-14T19:37:01Z\tm\tmsg_3\tx\t1\t1\t1\t/x\n" +
		"U\t2026-11-14T19:37:01Z\tm\tmsg_4\t1\t1\t1\n" +
		"U\t2026-11-14T19:37:01Z\tm\tmsg_5\t1\t1\t1\t1\tC:\\\\w\n" +
		"E\t50\n" +
		"F\t/p/b.jsonl\tbad\t0\t0\n" +
		"U\t2026-11-14T19:37:01Z\tm\tmsg_6\t1\t1\t1\t1\t/lost\n" +
		"F\t/p/c.jsonl\t7\t7\t1\n" +
		"E\t999\n"
	got := parseTranscripts(out)
	if len(got) != 2 {
		t.Fatalf("chunks: %+v", got)
	}
	a := got[0]
	if a.Path != "/p/a.jsonl" || a.Size != 100 || a.Start != 10 || a.End != 60 || a.Reset || len(a.Records) != 2 {
		t.Fatalf("a: %+v", a)
	}
	r := a.Records[0]
	if r.Model != "claude-opus-5-5" || r.MsgID != "msg_1" || r.Input != 2 || r.Output != 264 || r.CacheCreate != 14394 || r.CacheRead != 24768 ||
		r.Cwd != "/home/u/proj" || !r.Time.Equal(time.Date(2026, 11, 14, 19, 37, 1, 614e6, time.UTC)) {
		t.Fatalf("record: %+v", r)
	}
	if a.Records[1].Cwd != `C:\w` {
		t.Errorf("cwd unescape: %q", a.Records[1].Cwd)
	}
	if c := got[1]; !c.Reset || c.End != 7 || len(c.Records) != 0 {
		t.Errorf("c: %+v", c)
	}
}

func TestReadTranscriptsLocal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	cfg := filepath.Join(t.TempDir(), "claude")
	guardConfigDir(t, cfg)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	b := NewLocal(Options{Socket: "unused", Config: "/dev/null", Home: t.TempDir()})
	ctx := context.Background()

	// No projects dir: empty, no error.
	if got, err := b.ReadTranscripts(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("missing dir: %v %v", got, err)
	}

	proj := filepath.Join(cfg, "projects", "-home-u-my proj's")
	sub := filepath.Join(proj, "sess1", "subagents")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	ts := "2026-11-14T19:37:01.614Z"
	main := filepath.Join(proj, "sess1.jsonl")
	agent := filepath.Join(sub, "agent-a1.jsonl")
	l1 := asstLine("msg_A", "claude-opus-5-5", ts, "/home/u/proj", 2, 264, 14394, 24768)
	write := func(path, s string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		f.WriteString(s)
	}
	write(main, l1+l1+`{"type":"user","message":{"content":"hi"}}`+"\n"+
		asstLine("msg_S", "<synthetic>", ts, "/x", 0, 0, 0, 0)+
		asstLine("msg_B", "claude-sonnet-5", ts, "/home/u/proj", 5, 6, 7, 8))
	write(agent, asstLine("msg_C", "claude-haiku-5", ts, "", 1, 2, 3, 4))
	write(filepath.Join(proj, "notes.txt"), "ignored")

	byPath := func(cs []session.TranscriptChunk) map[string]session.TranscriptChunk {
		m := map[string]session.TranscriptChunk{}
		for _, c := range cs {
			m[c.Path] = c
		}
		return m
	}
	cs, err := b.ReadTranscripts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(cs)
	if len(m) != 2 {
		t.Fatalf("files: %+v", cs)
	}
	mc := m[main]
	st, _ := os.Stat(main)
	// Both duplicates are reported (deduping is the store's job); synthetic and non-assistant are not.
	if len(mc.Records) != 3 || mc.Records[0].MsgID != "msg_A" || mc.Records[1].MsgID != "msg_A" || mc.Records[2].MsgID != "msg_B" ||
		mc.Start != 0 || mc.End != st.Size() || mc.Size != st.Size() || mc.Reset {
		t.Fatalf("main chunk: %+v (size %d)", mc, st.Size())
	}
	if ac := m[agent]; len(ac.Records) != 1 || ac.Records[0].Cwd != "" || ac.Records[0].Input != 1 {
		t.Fatalf("agent chunk: %+v", ac)
	}

	// Append a complete line and a partial one; only the complete line counts.
	known := map[string]int64{main: mc.End, agent: m[agent].End}
	full := asstLine("msg_D", "claude-opus-5-5", ts, "/home/u/proj", 9, 9, 9, 9)
	partial := asstLine("msg_E", "claude-opus-5-5", ts, "/home/u/proj", 1, 1, 1, 1)
	write(main, full+strings.TrimSuffix(partial, "\n"))
	cs, err = b.ReadTranscripts(ctx, known)
	if err != nil {
		t.Fatal(err)
	}
	m = byPath(cs)
	mc2 := m[main]
	if len(mc2.Records) != 1 || mc2.Records[0].MsgID != "msg_D" || mc2.Start != mc.End || mc2.End != mc.End+int64(len(full)) {
		t.Fatalf("second read: %+v", mc2)
	}
	if ac := m[agent]; len(ac.Records) != 0 || ac.End != known[agent] || ac.Start != known[agent] {
		t.Fatalf("unchanged file: %+v", ac)
	}

	// The partial line is completed and read on the next scan.
	write(main, "\n")
	known[main] = mc2.End
	cs, _ = b.ReadTranscripts(ctx, known)
	if mc3 := byPath(cs)[main]; len(mc3.Records) != 1 || mc3.Records[0].MsgID != "msg_E" || mc3.End != mc3.Size {
		t.Fatalf("third read: %+v", mc3)
	}

	// A file shorter than its known offset is read again from the start.
	cs, _ = b.ReadTranscripts(ctx, map[string]int64{agent: 1 << 20})
	if ac := byPath(cs)[agent]; !ac.Reset || ac.Start != 0 || len(ac.Records) != 1 {
		t.Fatalf("reset: %+v", ac)
	}
}
