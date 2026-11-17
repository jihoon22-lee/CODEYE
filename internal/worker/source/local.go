package source

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

type localFile struct {
	Entry    Entry
	Modified time.Time
	Identity fs.FileInfo
}

func scanLocal(ctx context.Context, root *os.Root, limits Limits) ([]localFile, error) {
	out := []localFile{}
	names := map[string]bool{}
	var bytes int64
	err := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return ErrUnstable
		}
		if path == "." {
			return nil
		}
		if len(out) >= limits.MaxFiles {
			return ErrLimit
		}
		if !portablePath(path) {
			return ErrUnsupported
		}
		folded := strings.ToLower(path)
		if names[folded] {
			return ErrUnsupported
		}
		names[folded] = true
		info, err := root.Lstat(filepath.FromSlash(path))
		if err != nil {
			return ErrUnstable
		}
		size := info.Size()
		entry := Entry{Path: path, Kind: "file", Size: &size, Mode: uint32(info.Mode().Perm())}
		reason := exclusion(path, d.IsDir(), func(dir string) bool {
			_, err := root.Lstat(filepath.FromSlash(dir + "/pyvenv.cfg"))
			return err == nil
		})
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			entry.Kind = "symlink"
			reason = "link_not_followed"
		case info.IsDir():
			entry.Kind = "directory"
			*entry.Size = 0
		case !info.Mode().IsRegular():
			entry.Kind = "special"
			reason = "special_file_not_copied"
		}
		entry.ExcludedReason = reason
		if reason == "" && entry.Kind == "file" {
			if info.Size() > limits.MaxFileBytes {
				return ErrLimit
			}
			bytes += info.Size()
			if bytes > limits.MaxTotalBytes {
				return ErrLimit
			}
		}
		out = append(out, localFile{entry, info.ModTime(), info})
		if reason != "" && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	return out, err
}
func sameLocal(a, b []localFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i].Entry, b[i].Entry) || !a[i].Modified.Equal(b[i].Modified) || !os.SameFile(a[i].Identity, b[i].Identity) {
			return false
		}
	}
	return true
}
func AcquireLocal(ctx context.Context, approvedRoot, relative, dataRoot string, limits Limits) (Snapshot, error) {
	return acquireLocalHooks(ctx, approvedRoot, relative, dataRoot, limits, localHooks{})
}
func acquireLocal(ctx context.Context, approvedRoot, relative, dataRoot string, limits Limits, afterCopy func(int)) (Snapshot, error) {
	return acquireLocalHooks(ctx, approvedRoot, relative, dataRoot, limits, localHooks{afterCopy: afterCopy})
}

// localHooks let tests change the filesystem at exact points of an acquisition.
type localHooks struct {
	afterResolve func()                      // between path validation and opening the root
	beforeOpen   func(attempt int, p string) // after the scan, before a file is opened
	afterCopy    func(attempt int)
}

func acquireLocalHooks(ctx context.Context, approvedRoot, relative, dataRoot string, limits Limits, hooks localHooks) (Snapshot, error) {
	if !limits.valid() || !filepath.IsAbs(approvedRoot) || !filepath.IsAbs(dataRoot) {
		return Snapshot{}, ErrInvalid
	}
	// Friendly validation first; the confinement below does not trust its result.
	if _, err := ResolveLocal(approvedRoot, relative); err != nil {
		return Snapshot{}, err
	}
	resolvedApproved, err := filepath.EvalSymlinks(approvedRoot)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	// Platform storage must be outside the entire approved source root, not merely
	// outside the selected project, and is resolved through existing symlinked ancestors.
	dataRoot, err = outsideSource(dataRoot, resolvedApproved)
	if err != nil {
		return Snapshot{}, err
	}
	if err = os.MkdirAll(dataRoot, 0700); err != nil {
		return Snapshot{}, ErrUnavailable
	}
	if dataRoot, err = filepath.EvalSymlinks(dataRoot); err != nil {
		return Snapshot{}, ErrUnavailable
	}
	if within(resolvedApproved, dataRoot) || within(dataRoot, resolvedApproved) {
		return Snapshot{}, ErrInvalid
	}
	if hooks.afterResolve != nil {
		hooks.afterResolve()
	}
	// The selected directory is opened relative to the approved root, so a path component
	// swapped for a symlink after validation cannot lead outside it.
	approved, err := os.OpenRoot(resolvedApproved)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer approved.Close()
	rel := filepath.FromSlash(relative)
	root, err := approved.OpenRoot(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Snapshot{}, ErrUnavailable
		}
		return Snapshot{}, ErrInvalid
	}
	defer root.Close()
	original, err := root.Stat(".")
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	for attempt := 0; attempt <= limits.Retries; attempt++ {
		s, err := newSnapshot(dataRoot, "local", "stable_copy_best_effort")
		if err != nil {
			return Snapshot{}, err
		}
		s.Manifest.Limitations = append(s.Manifest.Limitations, "local_copy_is_not_atomic", "excluded_links_and_special_files_are_not_analyzed")
		err = copyLocal(ctx, root, &s, limits, attempt, hooks)
		current, statErr := approved.Stat(rel)
		if err == nil && (statErr != nil || !os.SameFile(original, current)) {
			err = ErrUnstable
		}
		if err == nil {
			err = finalize(ctx, &s)
		}
		if err == nil {
			return s, nil
		}
		_ = Remove(s.Root)
		if !errors.Is(err, ErrUnstable) {
			return Snapshot{}, err
		}
	}
	return Snapshot{}, ErrUnstable
}
func copyLocal(ctx context.Context, root *os.Root, s *Snapshot, limits Limits, attempt int, hooks localHooks) error {
	before, err := scanLocal(ctx, root, limits)
	if err != nil {
		return err
	}
	for _, item := range before {
		e := item.Entry
		if e.Kind == "directory" && e.ExcludedReason == "" {
			if err := os.MkdirAll(filepath.Join(s.SnapshotDir, filepath.FromSlash(e.Path)), 0700); err != nil {
				return ErrUnavailable
			}
		}
		if e.ExcludedReason != "" || e.Kind != "file" {
			s.Manifest.Entries = append(s.Manifest.Entries, e)
			continue
		}
		if hooks.beforeOpen != nil {
			hooks.beforeOpen(attempt, e.Path)
		}
		// O_NONBLOCK: a file replaced by a FIFO after the scan must not hang the open;
		// the type check below then reports the change.
		file, err := root.OpenFile(filepath.FromSlash(e.Path), os.O_RDONLY|openNonblock, 0)
		if err != nil {
			return ErrUnstable
		}
		pre, err := file.Stat()
		if err != nil || !pre.Mode().IsRegular() || !os.SameFile(pre, item.Identity) {
			_ = file.Close()
			return ErrUnstable
		}
		data, readErr := readLimited(ctx, file, limits.MaxFileBytes)
		post, postErr := file.Stat()
		_ = file.Close()
		if readErr != nil {
			return readErr
		}
		if postErr != nil || !os.SameFile(pre, post) || pre.Size() != post.Size() || !pre.ModTime().Equal(post.ModTime()) || int64(len(data)) != pre.Size() {
			return ErrUnstable
		}
		if err = writeFile(s.SnapshotDir, e.Path, data, pre.Mode()); err != nil {
			return err
		}
		s.Manifest.Entries = append(s.Manifest.Entries, entryFor(e.Path, data, pre.Mode()))
	}
	if hooks.afterCopy != nil {
		hooks.afterCopy(attempt)
	}
	after, err := scanLocal(ctx, root, limits)
	if err != nil {
		return err
	}
	if !sameLocal(before, after) {
		return ErrUnstable
	}
	return nil
}
