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
)

var _ session.HookInstaller = (*Backend)(nil)

const (
	hooksSubdir  = "claude-harness"
	hooksScript  = "claude-status.sh"
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

type hookPaths struct{ dir, script, settings string }

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
		dir:      dir,
		script:   ops.join(dir, hooksSubdir, hooksScript),
		settings: ops.join(dir, settingsName),
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
	ops, p, err := b.hookEnvUngated(ctx)
	if err != nil {
		return "", err
	}
	settings, _, err := readOptional(ctx, ops, p.settings)
	if err != nil {
		return "", b.mapError(err, "")
	}
	body, _, err := readOptional(ctx, ops, p.script)
	if err != nil {
		return "", b.mapError(err, "")
	}
	return claudehooks.Status(settings, body, p.script), nil
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
func (b *Backend) InstallHooks(ctx context.Context) error {
	defer b.invalidateHostCache()
	return b.installHooks(ctx)
}

func (b *Backend) installHooks(ctx context.Context) error {
	ops, p, err := b.hookEnv(ctx)
	if err != nil {
		return err
	}
	orig, existed, err := readOptional(ctx, ops, p.settings)
	if err != nil {
		return b.mapError(err, "")
	}
	merged, err := claudehooks.Merge(orig, p.script)
	if err != nil {
		return badSettings(p.settings, err) // nothing has been written
	}
	// Script first, so settings never point at a missing file.
	cur, _, err := readOptional(ctx, ops, p.script)
	if err != nil {
		return b.mapError(err, "")
	}
	if !bytes.Equal(cur, claudehooks.Script()) {
		if err := ops.writeFileAtomic(ctx, p.script, claudehooks.Script(), 0o755, false); err != nil {
			return b.mapError(err, "")
		}
	}
	if existed && bytes.Equal(orig, merged) {
		return nil
	}
	if existed {
		if err := backup(ctx, ops, p.settings, orig); err != nil {
			return b.mapError(err, "")
		}
	}
	return b.mapError(ops.writeFileAtomic(ctx, p.settings, merged, 0o600, true), "")
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
	if existed && claudehooks.Status(orig, nil, p.script) != session.HooksMissing {
		out, err := claudehooks.Unmerge(orig, p.script)
		if err != nil {
			return badSettings(p.settings, err)
		}
		if err := backup(ctx, ops, p.settings, orig); err != nil {
			return b.mapError(err, "")
		}
		if err := ops.writeFileAtomic(ctx, p.settings, out, 0o600, true); err != nil {
			return b.mapError(err, "")
		}
	}
	return b.mapError(ops.removeFile(ctx, p.script), "")
}
