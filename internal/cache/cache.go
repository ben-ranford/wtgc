// Package cache finds large dependency/build directories. It never deletes them.
package cache

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultThreshold int64 = 100 * 1024 * 1024

type Warning struct {
	Path   string
	Bytes  int64
	Kind   string
	Reason string
	Error  string
}

var catalogue = map[string]string{
	"node_modules": "node dependencies", "vendor": "vendored dependencies", ".gradle": "Gradle cache", "target": "Java/Rust build output", "__pycache__": "Python bytecode", ".venv": "Python environment", "venv": "Python environment", "dist": "build output", "build": "build output", ".cargo": "Rust cache",
}

// Scan walks below root without following symlinked directories. Failures are returned as warnings.
func Scan(ctx context.Context, root string, threshold int64) []Warning {
	if threshold < 0 {
		return []Warning{{Path: root, Reason: "cache scan disabled: invalid threshold"}}
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return []Warning{{Path: root, Reason: "cache scan error", Error: err.Error()}}
	}
	rootFS, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return []Warning{{Path: root, Reason: "cache scan error", Error: err.Error()}}
	}
	defer rootFS.Close()
	var out []Warning
	err = fs.WalkDir(rootFS.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			out = append(out, Warning{Path: filepath.Join(canonicalRoot, path), Reason: "cache scan error", Error: walkErr.Error()})
			return nil
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		kind, ok := catalogue[entry.Name()]
		if !ok {
			return nil
		}
		size, err := size(ctx, rootFS.FS(), path)
		if err != nil {
			out = append(out, Warning{Path: filepath.Join(canonicalRoot, path), Kind: kind, Reason: "cache size unavailable", Error: err.Error()})
			return filepath.SkipDir
		}
		if size >= threshold {
			out = append(out, Warning{Path: filepath.Join(canonicalRoot, path), Bytes: size, Kind: kind, Reason: "cache directory exceeds configured threshold"})
		}
		return filepath.SkipDir
	})
	if err != nil && ctx.Err() != nil {
		out = append(out, Warning{Path: root, Reason: "cache scan cancelled", Error: ctx.Err().Error()})
	}
	sort.Slice(out, func(i, j int) bool { return strings.Compare(out[i].Path, out[j].Path) < 0 })
	return out
}
func size(ctx context.Context, filesystem fs.FS, root string) (int64, error) {
	var total int64
	err := fs.WalkDir(filesystem, root, func(_ string, e fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if e.Type()&fs.ModeSymlink != 0 {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
