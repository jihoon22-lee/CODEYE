package source

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

// WorkspaceGuard keeps an attempt workspace equal to its snapshot between
// tasks. The tasks of one attempt run one after another in the same copy; a
// tool or a build script may rewrite a file there or leave new files
// behind. Restore puts every included manifest file back to its manifest
// content (from the immutable snapshot, hash-checked) and removes what the
// manifest does not list, so the next task starts from the snapshot again.
//
// Unlisted entries whose name keep accepts (data directories such as
// node_modules or hidden directories) are left in place and not descended
// into: they are never analyzed as project source, and prepared
// dependencies live there.
type WorkspaceGuard struct {
	s         Snapshot
	workspace string
	keep      func(name string) bool
	files     map[string]Entry
	dirs      map[string]bool
	stamps    map[string]stamp
}

// RestoreResult lists what Restore changed: manifest files whose workspace
// copy was rewritten or missing, and unlisted entries it removed (a removed
// directory is one entry).
type RestoreResult struct {
	Restored []string
	Removed  []string
}

// Changed reports whether Restore changed anything.
func (r RestoreResult) Changed() bool { return len(r.Restored) > 0 || len(r.Removed) > 0 }

// stamp is what Lstat says about a workspace file the guard last wrote or
// verified; a file with the same stamp is not read again.
type stamp struct {
	size  int64
	mod   int64
	mode  fs.FileMode
	extra fileIdentity
}

func stampOf(fi fs.FileInfo) stamp {
	return stamp{size: fi.Size(), mod: fi.ModTime().UnixNano(), mode: fi.Mode(), extra: identityOf(fi)}
}

// NewWorkspaceGuard records the workspace as cloned: every included file of
// the manifest must be a regular file there. It reads no content (the clone
// wrote verified bytes).
func NewWorkspaceGuard(s Snapshot, workspace string, keep func(name string) bool) (*WorkspaceGuard, error) {
	if !filepath.IsAbs(workspace) || !filepath.IsAbs(s.SnapshotDir) {
		return nil, ErrInvalid
	}
	g := &WorkspaceGuard{s: s, workspace: workspace, keep: keep, files: map[string]Entry{}, dirs: map[string]bool{".": true}, stamps: map[string]stamp{}}
	for _, e := range s.Manifest.Entries {
		if e.ExcludedReason != "" || !portablePath(e.Path) {
			continue
		}
		switch e.Kind {
		case "directory":
			g.dirs[e.Path] = true
		case "file", "lfs_pointer":
			if e.Size == nil {
				continue
			}
			g.files[e.Path] = e
		default:
			continue
		}
		for d := path.Dir(e.Path); d != "."; d = path.Dir(d) {
			g.dirs[d] = true
		}
	}
	for p := range g.files {
		fi, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(p)))
		if err != nil || !fi.Mode().IsRegular() {
			return nil, ErrIntegrity
		}
		g.stamps[p] = stampOf(fi)
	}
	return g, nil
}

// Restore makes the workspace equal to the snapshot again. Files whose stamp
// is unchanged are trusted; any other included file is read and compared
// with its manifest SHA-256 and rewritten from the snapshot when it differs.
// It stops at the first error; the workspace is then left as it is and every
// later task's own drift check still sees the difference.
func (g *WorkspaceGuard) Restore(ctx context.Context) (RestoreResult, error) {
	var out RestoreResult
	root, err := os.OpenRoot(g.workspace)
	if err != nil {
		return out, ErrUnavailable
	}
	defer root.Close()
	seen := map[string]bool{".": true}
	if err := g.walk(ctx, root, ".", seen, &out); err != nil {
		return out, err
	}
	// A listed directory that is gone with no file below it to rewrite.
	for d := range g.dirs {
		if !seen[d] {
			if err := root.MkdirAll(filepath.FromSlash(d), 0o700); err != nil {
				return out, ErrUnavailable
			}
		}
	}
	var missing []string
	for p := range g.files {
		if !seen[p] {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	for _, p := range missing {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := g.rewrite(root, p); err != nil {
			return out, err
		}
		out.Restored = append(out.Restored, p)
	}
	sort.Strings(out.Restored)
	sort.Strings(out.Removed)
	return out, nil
}

func (g *WorkspaceGuard) walk(ctx context.Context, root *os.Root, dir string, seen map[string]bool, out *RestoreResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := root.Open(filepath.FromSlash(dir))
	if err != nil {
		return ErrUnavailable
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return ErrUnavailable
	}
	for _, d := range entries {
		rel := d.Name()
		if dir != "." {
			rel = dir + "/" + d.Name()
		}
		native := filepath.FromSlash(rel)
		switch _, file := g.files[rel]; {
		case file:
			seen[rel] = true
			ok, err := g.intact(root, rel, d)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
			if d.IsDir() || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
				if err := root.RemoveAll(native); err != nil {
					return ErrUnavailable
				}
			}
			if err := g.rewrite(root, rel); err != nil {
				return err
			}
			out.Restored = append(out.Restored, rel)
		case g.dirs[rel]:
			seen[rel] = true
			if !d.IsDir() {
				// A listed directory replaced by a file or a link: remove it;
				// the files below it are rewritten as missing.
				if err := root.RemoveAll(native); err != nil {
					return ErrUnavailable
				}
				out.Removed = append(out.Removed, rel)
				if err := root.MkdirAll(native, 0o700); err != nil {
					return ErrUnavailable
				}
			}
			if err := g.walk(ctx, root, rel, seen, out); err != nil {
				return err
			}
		case g.keep != nil && g.keep(d.Name()):
			// An unlisted data directory or file: left in place, not read.
		default:
			if err := root.RemoveAll(native); err != nil {
				return ErrUnavailable
			}
			out.Removed = append(out.Removed, rel)
		}
	}
	return nil
}

// intact reports whether the workspace file rel still holds its manifest
// content: the same stamp, or (after any change of it) the same bytes.
func (g *WorkspaceGuard) intact(root *os.Root, rel string, d fs.DirEntry) (bool, error) {
	if !d.Type().IsRegular() {
		return false, nil
	}
	fi, err := d.Info()
	if err != nil {
		return false, nil
	}
	// The stamp is trusted only where it carries the file's change time and
	// inode: size and modification time alone can be put back by a rewrite,
	// so platforms without that identity compare the bytes every time.
	if st, ok := g.stamps[rel]; ok && st == stampOf(fi) && st.extra != (fileIdentity{}) {
		return true, nil
	}
	e := g.files[rel]
	if fi.Size() != *e.Size {
		return false, nil
	}
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return false, nil
	}
	data, err := readLimited(context.Background(), f, *e.Size)
	_ = f.Close()
	if err != nil || hashBytes(data) != e.SHA256 {
		return false, nil
	}
	g.stamps[rel] = stampOf(fi)
	return true, nil
}

// rewrite writes the snapshot bytes of rel into the workspace with the
// clone's mode, creating missing parent directories.
func (g *WorkspaceGuard) rewrite(root *os.Root, rel string) error {
	e := g.files[rel]
	data, err := g.snapshotBytes(e)
	if err != nil {
		return err
	}
	native := filepath.FromSlash(rel)
	if dir := path.Dir(rel); dir != "." {
		if err := g.clearParents(root, dir); err != nil {
			return err
		}
		if err := root.MkdirAll(filepath.FromSlash(dir), 0o700); err != nil {
			return ErrUnavailable
		}
	}
	if err := root.Remove(native); err != nil && !errors.Is(err, fs.ErrNotExist) {
		if err := root.RemoveAll(native); err != nil {
			return ErrUnavailable
		}
	}
	f, err := root.OpenFile(native, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrUnavailable
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return ErrUnavailable
	}
	if err := root.Chmod(native, fs.FileMode(e.Mode).Perm()|0o600); err != nil {
		return ErrUnavailable
	}
	fi, err := root.Lstat(native)
	if err != nil {
		return ErrUnavailable
	}
	g.stamps[rel] = stampOf(fi)
	return nil
}

// clearParents removes a file or link that stands where a parent directory
// of a manifest file belongs.
func (g *WorkspaceGuard) clearParents(root *os.Root, dir string) error {
	var chain []string
	for d := dir; d != "."; d = path.Dir(d) {
		chain = append(chain, d)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		fi, err := root.Lstat(filepath.FromSlash(chain[i]))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return ErrUnavailable
		}
		if !fi.IsDir() {
			if err := root.RemoveAll(filepath.FromSlash(chain[i])); err != nil {
				return ErrUnavailable
			}
			return nil
		}
	}
	return nil
}

// snapshotBytes reads a manifest file from the read-only snapshot and checks
// its size and SHA-256.
func (g *WorkspaceGuard) snapshotBytes(e Entry) ([]byte, error) {
	sroot, err := os.OpenRoot(g.s.SnapshotDir)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer sroot.Close()
	f, err := sroot.Open(filepath.FromSlash(e.Path))
	if err != nil {
		return nil, ErrIntegrity
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, *e.Size+1))
	if err != nil || int64(len(data)) != *e.Size || hashBytes(data) != e.SHA256 {
		return nil, ErrIntegrity
	}
	return data, nil
}
