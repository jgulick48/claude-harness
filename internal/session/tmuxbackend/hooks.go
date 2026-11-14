package tmuxbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"github.com/jgulick48/claude-harness/internal/claudehooks"
	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

var _ session.HookInstaller = (*Backend)(nil)

const (
	hooksSubdir  = "claude-harness"
	hooksScript  = "claude-status.sh"
	slScript     = "statusline.sh"   // status line wrapper (usage)
	slOrigName   = "statusline-orig" // the user's own status line command, 0600
	usageSubdir  = "usage"           // per-pane usage readings written by the wrapper
	settingsName = "settings.json"
	backupInfix  = ".claude-harness-backup-"
)

// hookOps are the file operations the hook installer needs on the host. Both
// localOps and sshOps implement it; Backend finds it by type assertion on
// b.ops, so the hostOps interface stays untouched.
type hookOps interface {
	// configDir is Claude Code's config dir: $CLAUDE_CONFIG_DIR, else ~/.claude.
	configDir(ctx context.Context) (string, error)
	// join joins host path elements.
	join(elem ...string) string
	// readFile returns the file's bytes; a missing file yields an error
	// wrapping fs.ErrNotExist.
	readFile(ctx context.Context, path string) ([]byte, error)
	// writeFileAtomic writes data to a temp file and renames it over path,
	// creating missing directories with mode 0700. An existing file keeps its
	// mode when keepMode is set; otherwise (and for new files) perm is used.
	writeFileAtomic(ctx context.Context, path string, data []byte, perm fs.FileMode, keepMode bool) error
	// removeFile deletes path; a missing file is not an error.
	removeFile(ctx context.Context, path string) error
	exists(ctx context.Context, path string) (bool, error)
}

type hookPaths struct{ dir, script, settings, statusline, slOrig, usageDir string }

func (b *Backend) hookEnv(ctx context.Context) (hookOps, hookPaths, error) {
	if err := b.gate(); err != nil {
		return nil, hookPaths{}, err
	}
	return b.hookEnvUngated(ctx)
}

// hookEnvUngated is hookEnv without the gate (the setup check must still be
// able to look at a host whose tmux is missing).
func (b *Backend) hookEnvUngated(ctx context.Context) (hookOps, hookPaths, error) {
	ops, ok := b.ops.(hookOps)
	if !ok {
		return nil, hookPaths{}, fmt.Errorf("%w: host %s cannot manage Claude hooks", session.ErrUnavailable, b.id)
	}
	dir, err := ops.configDir(ctx)
	if err != nil {
		return nil, hookPaths{}, b.mapError(err, "")
	}
	return ops, hookPaths{
		dir:        dir,
		script:     ops.join(dir, hooksSubdir, hooksScript),
		settings:   ops.join(dir, settingsName),
		statusline: ops.join(dir, hooksSubdir, slScript),
		slOrig:     ops.join(dir, hooksSubdir, slOrigName),
		usageDir:   ops.join(dir, hooksSubdir, usageSubdir),
	}, nil
}

// readOptional returns the file's bytes and whether it exists.
func readOptional(ctx context.Context, ops hookOps, path string) ([]byte, bool, error) {
	data, err := ops.readFile(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func badSettings(path string, err error) error {
	return fmt.Errorf("%w: %s is not usable: %v", session.ErrBadRequest, path, err)
}

// HooksStatus implements session.HookInstaller.
func (b *Backend) HooksStatus(ctx context.Context) (string, error) {
	if err := b.gate(); err != nil {
		return "", err
	}
	return b.hooksStatus(ctx)
}

func (b *Backend) hooksStatus(ctx context.Context) (string, error) {
	hooks, _, err := b.hookStatuses(ctx)
	return hooks, err
}

// hookStatuses returns the hooks and the usage (status line wrapper) install
// states from one read of the settings file.
func (b *Backend) hookStatuses(ctx context.Context) (hooks, usage string, err error) {
	ops, p, err := b.hookEnvUngated(ctx)
	if err != nil {
		return "", "", err
	}
	settings, _, err := readOptional(ctx, ops, p.settings)
	if err != nil {
		return "", "", b.mapError(err, "")
	}
	body, _, err := readOptional(ctx, ops, p.script)
	if err != nil {
		return "", "", b.mapError(err, "")
	}
	hooks = claudehooks.Status(settings, body, p.script)
	// The wrapper script is only read when the settings point at it.
	var slBody []byte
	if claudehooks.StatusLineStatus(settings, nil, p.statusline) != session.HooksMissing {
		if slBody, _, err = readOptional(ctx, ops, p.statusline); err != nil {
			return "", "", b.mapError(err, "")
		}
	}
	return hooks, claudehooks.StatusLineStatus(settings, slBody, p.statusline), nil
}

// backup saves orig next to path as settings.json.claude-harness-backup-<unix>
// (with a numeric suffix if that name is taken).
func backup(ctx context.Context, ops hookOps, path string, orig []byte) error {
	base := path + backupInfix + strconv.FormatInt(time.Now().Unix(), 10)
	name := base
	for i := 1; ; i++ {
		ok, err := ops.exists(ctx, name)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		name = base + "-" + strconv.Itoa(i)
	}
	return ops.writeFileAtomic(ctx, name, orig, 0o600, false)
}

// InstallHooks implements session.HookInstaller.
func (b *Backend) InstallHooks(ctx context.Context, usage bool) error {
	defer b.invalidateHostCache()
	return b.installHooks(ctx, usage)
}

// writeIfDiffers writes data (mode perm) to path unless it already holds it.
func writeIfDiffers(ctx context.Context, ops hookOps, path string, data []byte, perm fs.FileMode) error {
	cur, _, err := readOptional(ctx, ops, path)
	if err != nil {
		return err
	}
	if bytes.Equal(cur, data) {
		return nil
	}
	return ops.writeFileAtomic(ctx, path, data, perm, false)
}

// readStatusLineOrig returns the saved original status line command; a
// missing or empty file means the user had none.
func readStatusLineOrig(ctx context.Context, ops hookOps, p hookPaths) (string, bool, error) {
	data, ok, err := readOptional(ctx, ops, p.slOrig)
	if err != nil {
		return "", false, err
	}
	return string(data), ok && len(data) > 0, nil
}

func (b *Backend) installHooks(ctx context.Context, usage bool) error {
	ops, p, err := b.hookEnv(ctx)
	if err != nil {
		return err
	}
	orig, existed, err := readOptional(ctx, ops, p.settings)
	if err != nil {
		return b.mapError(err, "")
	}
	// Both merges are computed from the same bytes, so settings is backed up
	// and written once.
	merged, err := claudehooks.Merge(orig, p.script)
	if err != nil {
		return badSettings(p.settings, err) // nothing has been written
	}
	var slOrig string
	var hadOrig, already bool
	if usage {
		merged, slOrig, hadOrig, already, err = claudehooks.MergeStatusLine(merged, p.statusline)
	} else {
		var o string
		var had bool
		if o, had, err = readStatusLineOrig(ctx, ops, p); err != nil {
			return b.mapError(err, "")
		}
		merged, err = claudehooks.UnmergeStatusLine(merged, p.statusline, o, had)
	}
	if err != nil {
		return badSettings(p.settings, err)
	}
	// Scripts first, so settings never point at a missing file.
	if err := writeIfDiffers(ctx, ops, p.script, claudehooks.Script(), 0o755); err != nil {
		return b.mapError(err, "")
	}
	if usage {
		if !already {
			// Save the user's own command before our wrapper replaces it; an
			// already-installed wrapper must not overwrite it with itself.
			var err error
			if hadOrig {
				err = ops.writeFileAtomic(ctx, p.slOrig, []byte(slOrig), 0o600, false)
			} else {
				err = ops.removeFile(ctx, p.slOrig)
			}
			if err != nil {
				return b.mapError(err, "")
			}
		}
		if err := writeIfDiffers(ctx, ops, p.statusline, claudehooks.StatusLineScript(), 0o755); err != nil {
			return b.mapError(err, "")
		}
	}
	if !existed || !bytes.Equal(orig, merged) {
		if existed {
			if err := backup(ctx, ops, p.settings, orig); err != nil {
				return b.mapError(err, "")
			}
		}
		if err := ops.writeFileAtomic(ctx, p.settings, merged, 0o600, true); err != nil {
			return b.mapError(err, "")
		}
	}
	if !usage {
		return b.mapError(b.removeUsageFiles(ctx, ops, p), "")
	}
	return nil
}

// removeUsageFiles deletes the status line wrapper, its saved original and
// the recorded readings. The usage directory's files are removed with one
// shell command where the host has one (best effort; the directory itself is
// removed too when empty), otherwise they are left to expire (the reader
// ignores files older than a day).
func (b *Backend) removeUsageFiles(ctx context.Context, ops hookOps, p hookPaths) error {
	for _, f := range []string{p.statusline, p.slOrig} {
		if err := ops.removeFile(ctx, f); err != nil {
			return err
		}
	}
	if sh, ok := ops.(shellOps); ok {
		u := tmux.ShellQuote(p.usageDir)
		sh.shell(ctx, "rm -f "+u+"/* 2>/dev/null; rmdir "+u+" 2>/dev/null; rmdir "+tmux.ShellQuote(ops.join(p.dir, hooksSubdir))+" 2>/dev/null; true")
	}
	return nil
}

// RemoveHooks implements session.HookInstaller.
func (b *Backend) RemoveHooks(ctx context.Context) error {
	defer b.invalidateHostCache()
	return b.removeHooks(ctx)
}

func (b *Backend) removeHooks(ctx context.Context) error {
	ops, p, err := b.hookEnv(ctx)
	if err != nil {
		return err
	}
	orig, existed, err := readOptional(ctx, ops, p.settings)
	if err != nil {
		return b.mapError(err, "")
	}
	if existed {
		out := orig
		if claudehooks.Status(out, nil, p.script) != session.HooksMissing {
			if out, err = claudehooks.Unmerge(out, p.script); err != nil {
				return badSettings(p.settings, err)
			}
		}
		if claudehooks.StatusLineStatus(out, nil, p.statusline) != session.HooksMissing {
			o, had, err := readStatusLineOrig(ctx, ops, p)
			if err != nil {
				return b.mapError(err, "")
			}
			if out, err = claudehooks.UnmergeStatusLine(out, p.statusline, o, had); err != nil {
				return badSettings(p.settings, err)
			}
		}
		if !bytes.Equal(out, orig) {
			if err := backup(ctx, ops, p.settings, orig); err != nil {
				return b.mapError(err, "")
			}
			if err := ops.writeFileAtomic(ctx, p.settings, out, 0o600, true); err != nil {
				return b.mapError(err, "")
			}
		}
	}
	if err := ops.removeFile(ctx, p.script); err != nil {
		return b.mapError(err, "")
	}
	return b.mapError(b.removeUsageFiles(ctx, ops, p), "")
}
