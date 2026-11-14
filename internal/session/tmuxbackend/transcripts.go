package tmuxbackend

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

// Claude transcripts (docs/CORE_PROTOCOL.md §5.4, usage history). Claude Code
// appends one JSON object per line to <config dir>/projects/**/*.jsonl. A
// shell script on the host reads only what was appended since the offsets the
// caller already knows and prints one record per assistant message.

var _ session.TranscriptReader = (*Backend)(nil)

// projectsSubdir is the transcript directory inside Claude's config dir.
const projectsSubdir = "projects"

// transcriptAwk extracts the usage of assistant entries. It runs with
// -v total=<bytes in the chunk>. Only lines ending in a newline count as
// consumed; a trailing partial line is dropped so it is read again later.
const transcriptAwk = `function str(key,   i, s, j) {
	i = index($0, key)
	if (!i) return ""
	s = substr($0, i + length(key))
	j = index(s, "\"")
	return j ? substr(s, 1, j - 1) : ""
}
function num(key,   i, s) {
	i = index($0, key)
	if (!i) return 0
	s = substr($0, i + length(key))
	return match(s, /^[0-9]+/) ? substr(s, 1, RLENGTH) : 0
}
{
	if (rec != "") print rec
	rec = ""
	last = length($0) + 1
	consumed += last
	if (index($0, "\"type\":\"assistant\"") && index($0, "\"usage\":{")) {
		model = str("\"model\":\"")
		ts = str("\"timestamp\":\"")
		id = str("\"id\":\"msg_")
		if (id != "") id = "msg_" id
		if (model != "" && ts != "" && substr(model, 1, 1) != "<")
			rec = "U\t" ts "\t" model "\t" id "\t" num("\"input_tokens\":") "\t" num("\"output_tokens\":") "\t" num("\"cache_creation_input_tokens\":") "\t" num("\"cache_read_input_tokens\":") "\t" str("\"cwd\":\"")
	}
}
END {
	if (consumed > total) { consumed -= last; rec = "" }
	if (rec != "") print rec
	print "E\t" consumed
}`

// transcriptScript builds the shell script that reads the transcripts under
// dir from the known offsets (path -> offset). Its output:
//
//	F<TAB>path<TAB>size<TAB>offset<TAB>reset   once per file, then
//	U<TAB>ts<TAB>model<TAB>msgid<TAB>in<TAB>out<TAB>cacheCreate<TAB>cacheRead<TAB>cwd   per message
//	E<TAB>bytes consumed                       when anything was read
//
// Paths with a tab or newline are never reported (they can't be framed).
func transcriptScript(dir string, known map[string]int64) string {
	paths := make([]string, 0, len(known))
	for p := range known {
		if !strings.ContainsAny(p, "\t\n") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var kb strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&kb, "%d\t%s\n", known[p], p)
	}
	return `LC_ALL=C
export LC_ALL
d=` + tmux.ShellQuote(dir) + `
[ -d "$d" ] || exit 0
known=$(cat <<'HARNESS_KNOWN_EOF'
` + kb.String() + `HARNESS_KNOWN_EOF
)
find "$d" -type f -name '*.jsonl' 2>/dev/null | while IFS= read -r f; do
	case $f in *'	'*) continue ;; esac
	size=$(wc -c < "$f" 2>/dev/null) || continue
	size=$((size + 0))
	off=
	if [ -n "$known" ]; then
		off=$(printf '%s\n' "$known" | P="$f" awk -F '\t' '$2 == ENVIRON["P"] { print $1; exit }')
	fi
	off=$((${off:-0} + 0))
	reset=0
	if [ "$size" -lt "$off" ]; then off=0; reset=1; fi
	printf 'F\t%s\t%s\t%s\t%s\n' "$f" "$size" "$off" "$reset"
	if [ "$size" -gt "$off" ]; then
		tail -c +$((off + 1)) "$f" 2>/dev/null | head -c $((size - off)) | awk -v total=$((size - off)) '` + transcriptAwk + `'
	fi
done
exit 0
`
}

// parseTranscripts parses transcriptScript's output, ignoring malformed lines.
func parseTranscripts(out string) []session.TranscriptChunk {
	var chunks []session.TranscriptChunk
	var cur *session.TranscriptChunk
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		switch {
		case f[0] == "F" && len(f) == 5:
			size, e1 := strconv.ParseInt(f[2], 10, 64)
			off, e2 := strconv.ParseInt(f[3], 10, 64)
			if e1 != nil || e2 != nil || size < 0 || off < 0 || f[1] == "" {
				cur = nil
				continue
			}
			chunks = append(chunks, session.TranscriptChunk{Path: f[1], Size: size, Start: off, End: off, Reset: f[4] == "1"})
			cur = &chunks[len(chunks)-1]
		case cur == nil:
		case f[0] == "E" && len(f) == 2:
			if n, err := strconv.ParseInt(f[1], 10, 64); err == nil && n >= 0 && cur.Start+n <= cur.Size {
				cur.End = cur.Start + n
			}
		case f[0] == "U" && len(f) == 9:
			ts, err := time.Parse(time.RFC3339Nano, f[1])
			var n [4]int64
			for i := range n {
				v, e := strconv.ParseInt(f[4+i], 10, 64)
				if e != nil {
					err = e
				}
				n[i] = v
			}
			if err != nil || f[2] == "" {
				continue
			}
			cwd := f[8]
			if strings.Contains(cwd, `\`) {
				if u, err := strconv.Unquote(`"` + cwd + `"`); err == nil {
					cwd = u
				}
			}
			cur.Records = append(cur.Records, session.TranscriptRecord{
				Time: ts.UTC(), Model: f[2], MsgID: f[3],
				Input: n[0], Output: n[1], CacheCreate: n[2], CacheRead: n[3], Cwd: cwd,
			})
		}
	}
	return chunks
}

// ReadTranscripts implements session.TranscriptReader for the login user.
// Run-as accounts (other users' homes) are not read.
func (b *Backend) ReadTranscripts(ctx context.Context, known map[string]int64) ([]session.TranscriptChunk, error) {
	sh, ok := b.ops.(shellOps)
	if !ok {
		return nil, fmt.Errorf("%w: host cannot run commands", session.ErrUnavailable)
	}
	ops, ok := b.ops.(hookOps)
	if !ok {
		return nil, fmt.Errorf("%w: host cannot locate Claude's config", session.ErrUnavailable)
	}
	cfg, err := ops.configDir(ctx)
	if err != nil {
		return nil, err
	}
	out, err := sh.shell(ctx, transcriptScript(ops.join(cfg, projectsSubdir), known))
	if err != nil {
		return nil, err
	}
	return parseTranscripts(string(out)), nil
}
