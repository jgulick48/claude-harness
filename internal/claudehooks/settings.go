package claudehooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jgulick48/claude-harness/internal/session"
)

// hookTimeout is the per-hook timeout in seconds.
const hookTimeout = 5

// Merge and Unmerge parse and re-marshal the whole file: every key, event and
// group is preserved as a value (numbers keep their exact text), but key order
// inside objects becomes alphabetical and formatting is normalized.

// Command returns the shell command registered for the script at scriptPath
// (the path single-quoted, so spaces are safe).
func Command(scriptPath string) string {
	return "'" + strings.ReplaceAll(scriptPath, "'", `'\''`) + "'"
}

type obj = map[string]any

func parse(settings []byte) (obj, error) {
	if len(bytes.TrimSpace(settings)) == 0 {
		return obj{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(settings))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("invalid JSON: trailing data after top-level value")
	}
	m, ok := v.(obj)
	if !ok {
		return nil, fmt.Errorf("invalid settings: top-level value is not an object")
	}
	return m, nil
}

func marshal(m obj) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil { // Encode appends the newline
		return nil, err
	}
	return buf.Bytes(), nil
}

// hooksOf returns settings["hooks"] as an object (nil when absent).
func hooksOf(m obj) (obj, error) {
	v, ok := m["hooks"]
	if !ok || v == nil {
		return nil, nil
	}
	h, ok := v.(obj)
	if !ok {
		return nil, fmt.Errorf("invalid settings: \"hooks\" is not an object")
	}
	return h, nil
}

func groupsOf(h obj, event string) ([]any, error) {
	v, ok := h[event]
	if !ok || v == nil {
		return nil, nil
	}
	gs, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid settings: hooks.%s is not an array", event)
	}
	return gs, nil
}

// references reports whether a handler is ours (its command names the script).
func references(handler any, scriptPath string) (ours, current bool) {
	h, ok := handler.(obj)
	if !ok {
		return false, false
	}
	cmd, _ := h["command"].(string)
	if cmd == "" || !(strings.Contains(cmd, scriptPath) || strings.Contains(cmd, Command(scriptPath))) {
		return false, false
	}
	t, _ := h["type"].(string)
	n, _ := h["timeout"].(json.Number)
	return true, t == "command" && cmd == Command(scriptPath) && n.String() == fmt.Sprint(hookTimeout)
}

// strip removes our handlers from groups. keepCurrent keeps up-to-date ones.
// Groups left without handlers are dropped (only if they had ours in them).
// It returns the new groups, how many current handlers were kept, and whether
// anything was removed.
func strip(groups []any, scriptPath string, keepCurrent bool) (out []any, kept int, removed bool) {
	for _, g := range groups {
		gm, ok := g.(obj)
		hs, hok := gm["hooks"].([]any)
		if !ok || !hok {
			out = append(out, g)
			continue
		}
		var nh []any
		touched := false
		for _, h := range hs {
			ours, cur := references(h, scriptPath)
			switch {
			case !ours:
				nh = append(nh, h)
			case cur && keepCurrent:
				nh = append(nh, h)
				kept++
			default:
				touched = true
			}
		}
		if !touched {
			out = append(out, g)
			continue
		}
		removed = true
		if len(nh) > 0 {
			ng := obj{}
			for k, v := range gm {
				ng[k] = v
			}
			ng["hooks"] = nh
			out = append(out, ng)
		}
	}
	return out, kept, removed
}

// Merge registers the script at scriptPath for every event in Events. Existing
// current entries are kept (so Merge is idempotent), outdated entries of ours
// (same script path, different command or timeout) are replaced, and
// everything else is preserved. An empty settings file means {}. Invalid JSON
// returns an error; the caller must not write the file then.
func Merge(settings []byte, scriptPath string) ([]byte, error) {
	m, err := parse(settings)
	if err != nil {
		return nil, err
	}
	h, err := hooksOf(m)
	if err != nil {
		return nil, err
	}
	if h == nil {
		h = obj{}
	}
	for _, ev := range Events {
		groups, err := groupsOf(h, ev)
		if err != nil {
			return nil, err
		}
		groups, kept, _ := strip(groups, scriptPath, true)
		if kept == 0 {
			groups = append(groups, obj{"hooks": []any{obj{
				"type": "command", "command": Command(scriptPath), "timeout": json.Number(fmt.Sprint(hookTimeout)),
			}}})
		}
		h[ev] = groups
	}
	m["hooks"] = h
	return marshal(m)
}

// Unmerge removes only our handlers, then drops groups and events left empty,
// then "hooks" itself if nothing remains.
func Unmerge(settings []byte, scriptPath string) ([]byte, error) {
	m, err := parse(settings)
	if err != nil {
		return nil, err
	}
	h, err := hooksOf(m)
	if err != nil {
		return nil, err
	}
	if h != nil {
		for ev := range h {
			groups, err := groupsOf(h, ev)
			if err != nil {
				continue // not ours to judge; leave it alone
			}
			ng, _, removed := strip(groups, scriptPath, false)
			if !removed {
				continue
			}
			if len(ng) == 0 {
				delete(h, ev)
			} else {
				h[ev] = ng
			}
		}
		if len(h) == 0 {
			delete(m, "hooks")
		}
	}
	return marshal(m)
}

// Status classifies the install: session.HooksInstalled when every event has
// our current entry and scriptBody equals Script(); HooksOutdated when our
// entries are only partly present or out of date, or the script differs;
// HooksMissing when settings has no entry of ours; HooksUnknown when settings
// can't be parsed.
func Status(settings []byte, scriptBody []byte, scriptPath string) string {
	m, err := parse(settings)
	if err != nil {
		return session.HooksUnknown
	}
	h, err := hooksOf(m)
	if err != nil {
		return session.HooksUnknown
	}
	current, anyOurs := 0, false
	for _, ev := range Events {
		groups, _ := groupsOf(h, ev)
		has := false
		for _, g := range groups {
			gm, _ := g.(obj)
			hs, _ := gm["hooks"].([]any)
			for _, hd := range hs {
				ours, cur := references(hd, scriptPath)
				anyOurs = anyOurs || ours
				has = has || cur
			}
		}
		if has {
			current++
		}
	}
	switch {
	case !anyOurs:
		return session.HooksMissing
	case current == len(Events) && bytes.Equal(scriptBody, Script()):
		return session.HooksInstalled
	default:
		return session.HooksOutdated
	}
}
