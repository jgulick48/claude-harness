package hosts

import (
	"context"
	"errors"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

type transcriptInner struct {
	stubInner
	got map[string]int64
}

func (s *transcriptInner) ReadTranscripts(_ context.Context, known map[string]int64) ([]session.TranscriptChunk, error) {
	s.got = known
	return []session.TranscriptChunk{{Path: "/p.jsonl", Size: 3, End: 3}}, nil
}

// The wrapper must expose the inner backend's TranscriptReader, or usage
// history silently shows nothing for SSH hosts.
func TestSSHHostDelegatesTranscripts(t *testing.T) {
	var b session.Backend = newSSHHost(nil, "devbox", "devbox", "devbox")
	if _, ok := b.(session.TranscriptReader); !ok {
		t.Fatal("sshHost is not a session.TranscriptReader")
	}
	h := newSSHHost(nil, "devbox", "devbox", "devbox")
	ctx := context.Background()

	if _, err := h.ReadTranscripts(ctx, nil); !errors.Is(err, session.ErrUnavailable) {
		t.Fatalf("disconnected: %v", err)
	}
	h.state = session.HostConnected
	h.inner = stubInner{}
	if _, err := h.ReadTranscripts(ctx, nil); !errors.Is(err, session.ErrUnavailable) {
		t.Fatalf("inner without capability: %v", err)
	}
	in := &transcriptInner{}
	h.inner = in
	cs, err := h.ReadTranscripts(ctx, map[string]int64{"/p.jsonl": 1})
	if err != nil || len(cs) != 1 || in.got["/p.jsonl"] != 1 {
		t.Fatalf("delegation: %v %v %v", cs, err, in.got)
	}
}
