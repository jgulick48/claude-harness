package tmuxbackend

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

var _ hookOps = localOps{}

func (o localOps) configDir(context.Context) (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	return filepath.Join(o.home, ".claude"), nil
}

func (o localOps) join(elem ...string) string { return filepath.Join(elem...) }

func (o localOps) readFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (o localOps) exists(_ context.Context, path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (o localOps) removeFile(_ context.Context, path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (o localOps) writeFileAtomic(_ context.Context, path string, data []byte, perm fs.FileMode, keepMode bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mode := perm
	if keepMode {
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
