package source

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type GitOptions struct {
	Executable string
	// AllowedHost is an operator-approved HTTPS host[:port]. Redirects are disabled.
	AllowedHost string
	// Optional explicitly approved local Git root. Intended for offline/native use;
	// user-supplied URLs cannot enable local transport by themselves.
	AllowedLocalRoot string
	// Timeout is the deadline for the whole acquisition (every git command and the
	// local extraction work share it), at most 10 minutes.
	Timeout time.Duration
	// CABundle is the PEM file git trusts for the host's TLS
	// (GIT_SSL_CAINFO and SSL_CERT_FILE); "" keeps git's own store.
	CABundle string
	// Token authenticates the HTTPS requests to AllowedHost of this one
	// acquisition (personal mode's GitHub token). It reaches git only as an
	// Authorization header in the process environment
	// (GIT_CONFIG_COUNT/KEY/VALUE scoped to https://AllowedHost/), never in
	// argv, the remote URL or a file of the workspace.
	Token string
	// PullRequest, when set, fetches refs/pull/NUMBER/head instead of a
	// branch; ExpectSHA is the pinned head commit. When the ref has moved
	// since the pin, the pinned commit itself is fetched; if the host does
	// not serve it the acquisition fails with ErrUnstable.
	PullRequest int
	ExpectSHA   string
}
type capWriter struct {
	data     []byte
	limit    int64
	overflow bool
}

func (w *capWriter) Write(b []byte) (int, error) {
	n := len(b)
	left := w.limit - int64(len(w.data))
	if int64(n) > left {
		b = b[:left]
		w.overflow = true
	}
	w.data = append(w.data, b...)
	return n, nil
}

type gitRunner struct {
	executable, repo, home string
	caBundle               string
	fileTransport          bool
	// authHeader is the http.extraHeader for authHost ("" for none).
	authHeader, authHost string
	// fetchLimit is a soft ceiling on the bytes git may write under repo. It is polled,
	// not a filesystem quota: a fast transfer can overshoot it before the process is stopped.
	fetchLimit int64
}

// softFetchLimit leaves room for pack/index/tree overhead above the content limit.
func softFetchLimit(l Limits) int64 { return l.MaxTotalBytes + l.MaxTotalBytes/4 + 1<<20 }

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, e := d.Info(); e == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// run executes git. A deadline, cancellation or fetch-size stop is reported as
// ErrRecoveryRequired (child-process cleanup is not confirmed); a size stop also wraps ErrLimit.
func (g gitRunner) run(ctx context.Context, input io.Reader, max int64, args ...string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	policy := []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "credential.helper=", "-c", "http.followRedirects=false", "-c", "core.hooksPath=" + filepath.Join(g.home, "hooks"), "-c", "fetch.recurseSubmodules=false", "-c", "transfer.fsckObjects=true", "-c", "fetch.fsckObjects=true"}
	if g.fileTransport {
		policy = append(policy, "-c", "protocol.file.allow=always")
	}
	cmd := exec.CommandContext(ctx, g.executable, append(policy, args...)...)
	cmd.Dir = g.repo
	cmd.Stdin = input
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Env = []string{"HOME=" + g.home, "USERPROFILE=" + g.home, "TMP=" + g.home, "TEMP=" + g.home, "TMPDIR=" + g.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1", "LC_ALL=C"}
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	if g.caBundle != "" {
		// GIT_SSL_CAINFO for every TLS backend git's curl may use.
		cmd.Env = append(cmd.Env, "GIT_SSL_CAINFO="+g.caBundle, "SSL_CERT_FILE="+g.caBundle)
	}
	if g.authHeader != "" {
		// Command-scope configuration through the environment: the header
		// applies to https://authHost/ only and is not in argv.
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://"+g.authHost+"/.extraHeader", "GIT_CONFIG_VALUE_0="+g.authHeader)
	}
	configureGitProcess(cmd)
	out := &capWriter{limit: max}
	stderr := &capWriter{limit: 8192}
	cmd.Stdout = out
	cmd.Stderr = stderr
	var over atomic.Bool
	done := make(chan struct{})
	if g.fetchLimit > 0 {
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if dirSize(g.repo) > g.fetchLimit {
						over.Store(true)
						cancel()
						return
					}
				}
			}
		}()
	}
	err := cmd.Run()
	close(done)
	if over.Load() {
		return nil, errors.Join(ErrLimit, ErrRecoveryRequired)
	}
	if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) {
		return nil, ErrRecoveryRequired
	}
	if out.overflow {
		return nil, ErrLimit
	}
	if err != nil {
		return nil, gitFailure(stderr.data)
	}
	return out.data, nil
}

// gitFailure classifies a failed git command by its diagnostics (LC_ALL=C,
// so the messages are git's English ones). The diagnostics are not kept.
func gitFailure(stderr []byte) error {
	msg := strings.ToLower(string(stderr))
	for _, s := range []string{"could not read username", "authentication failed", "terminal prompts disabled", "repository not found", "returned error: 401", "returned error: 403"} {
		if strings.Contains(msg, s) {
			return errors.Join(ErrAuthRequired, ErrUnavailable)
		}
	}
	for _, s := range []string{"could not resolve host", "failed to connect", "connection refused", "connection timed out", "network is unreachable", "ssl certificate problem", "gnutls", "unable to access"} {
		if strings.Contains(msg, s) {
			return errors.Join(ErrUnreachable, ErrUnavailable)
		}
	}
	return ErrUnavailable
}

// remoteDefaultBranch asks the remote which branch its HEAD names
// (`git ls-remote --symref`): the branch an empty project branch means.
func remoteDefaultBranch(run func(io.Reader, int64, ...string) ([]byte, error), location string) (string, error) {
	out, err := run(nil, 64<<10, "ls-remote", "--symref", "--", location, "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		ref, target, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && target == "HEAD" && strings.HasPrefix(ref, "ref: refs/heads/") {
			if b := strings.TrimPrefix(ref, "ref: refs/heads/"); b != "" {
				return b, nil
			}
		}
	}
	return "", ErrUnsupported
}

var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func gitLocation(repository string, options GitOptions) (string, bool, error) {
	u, err := url.Parse(repository)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false, ErrInvalid
	}
	if u.Scheme == "https" && u.Host != "" && strings.EqualFold(u.Host, options.AllowedHost) && u.Path != "" && u.Path != "/" {
		return repository, false, nil
	}
	if u.Scheme == "file" && u.Host == "" && options.AllowedLocalRoot != "" {
		root, err := filepath.EvalSymlinks(options.AllowedLocalRoot)
		if err != nil || !filepath.IsAbs(root) {
			return "", false, ErrInvalid
		}
		path := filepath.FromSlash(u.Path)
		// file:///C:/... is the canonical URL form on Windows.
		if len(path) > 3 && path[0] == filepath.Separator && path[2] == ':' {
			path = path[1:]
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil || !within(root, path) {
			return "", false, ErrInvalid
		}
		return path, true, nil
	}
	return "", false, ErrUnsupported
}
func AcquireGit(ctx context.Context, repository, branch, dataRoot string, limits Limits, options GitOptions) (result Snapshot, returnErr error) {
	if !limits.valid() || options.Timeout <= 0 || options.Timeout > 10*time.Minute || !filepath.IsAbs(options.Executable) || strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, "\x00\r\n") {
		return Snapshot{}, ErrInvalid
	}
	if options.AllowedLocalRoot != "" && !filepath.IsAbs(options.AllowedLocalRoot) {
		return Snapshot{}, ErrInvalid
	}
	if options.PullRequest < 0 || (options.PullRequest > 0 && (branch != "" || !objectID.MatchString(options.ExpectSHA))) || strings.ContainsAny(options.Token, "\x00\r\n") {
		return Snapshot{}, ErrInvalid
	}
	requestedAt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	location, local, err := gitLocation(repository, options)
	if err != nil {
		return Snapshot{}, err
	}
	if options.AllowedLocalRoot != "" {
		// Platform storage stays outside the whole approved local Git root.
		allowed, err := filepath.EvalSymlinks(options.AllowedLocalRoot)
		if err != nil {
			return Snapshot{}, ErrInvalid
		}
		if dataRoot, err = outsideSource(dataRoot, allowed); err != nil {
			return Snapshot{}, err
		}
	} else if !filepath.IsAbs(dataRoot) {
		return Snapshot{}, ErrInvalid
	}
	s, err := newSnapshot(dataRoot, "git", "git_commit")
	if err != nil {
		return Snapshot{}, err
	}
	s.Manifest.RequestedAt = requestedAt
	success := false
	recovering := false
	defer func() {
		if recovering {
			result = s
			_ = os.WriteFile(filepath.Join(s.Root, "recovery-required"), []byte("cleanup_unconfirmed\n"), 0600)
		}
		if !success && !recovering {
			_ = Remove(s.Root)
		}
	}()
	gitDir := filepath.Join(s.Root, "git")
	home := filepath.Join(s.Root, "home")
	if os.Mkdir(gitDir, 0700) != nil || os.Mkdir(home, 0700) != nil {
		return Snapshot{}, ErrUnavailable
	}
	runner := gitRunner{executable: options.Executable, repo: gitDir, home: home, caBundle: options.CABundle, fileTransport: local, fetchLimit: softFetchLimit(limits)}
	if options.Token != "" && !local {
		runner.authHeader = "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+options.Token))
		runner.authHost = strings.ToLower(options.AllowedHost)
	}
	run := func(input io.Reader, max int64, args ...string) ([]byte, error) {
		out, err := runner.run(ctx, input, max, args...)
		if errors.Is(err, ErrRecoveryRequired) {
			recovering = true
		}
		return out, err
	}
	ref := "refs/pull/" + strconv.Itoa(options.PullRequest) + "/head"
	if options.PullRequest == 0 {
		if branch == "" {
			// No branch: the remote's default branch, whose name the snapshot records.
			if branch, err = remoteDefaultBranch(run, location); err != nil {
				return Snapshot{}, err
			}
			if strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, "\x00\r\n") || len(branch) > 255 {
				return Snapshot{}, ErrUnsupported
			}
		}
		s.Manifest.SourceBranch = branch
		ref = "refs/heads/" + branch
		if _, err = run(nil, 8192, "check-ref-format", ref); err != nil {
			if errors.Is(err, ErrRecoveryRequired) {
				return Snapshot{}, err
			}
			return Snapshot{}, ErrInvalid
		}
	} else {
		s.Manifest.SourceRef = ref
	}
	if _, err = run(nil, 8192, "init", "--bare", "--template="); err != nil {
		return Snapshot{}, err
	}
	fetchHead := func(what string) (string, error) {
		if _, err := run(nil, 8192, "fetch", "--depth=1", "--no-tags", "--no-recurse-submodules", "--", location, what); err != nil {
			return "", err
		}
		sha, err := run(nil, 256, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
		if err != nil {
			return "", err
		}
		if id := strings.TrimSpace(string(sha)); objectID.MatchString(id) {
			return id, nil
		}
		return "", ErrUnavailable
	}
	if s.Manifest.SourceSHA, err = fetchHead(ref); err != nil {
		return Snapshot{}, err
	}
	if options.ExpectSHA != "" && s.Manifest.SourceSHA != options.ExpectSHA {
		// The pull request moved after it was pinned: the pinned commit
		// itself, which the host serves while it is reachable.
		got, err := fetchHead(options.ExpectSHA)
		switch {
		case errors.Is(err, ErrRecoveryRequired):
			return Snapshot{}, err
		case err != nil || got != options.ExpectSHA:
			return Snapshot{}, errors.Join(ErrUnstable, ErrUnavailable)
		}
		s.Manifest.SourceSHA = got
	}
	tree, err := run(nil, int64(limits.MaxFiles)*8192, "ls-tree", "-r", "-z", "--full-tree", s.Manifest.SourceSHA)
	if err != nil {
		return Snapshot{}, err
	}
	type record struct{ mode, oid, path string }
	var records []record
	names := map[string]bool{}
	for _, raw := range bytes.Split(tree, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		if len(records) >= limits.MaxFiles {
			return Snapshot{}, ErrLimit
		}
		header, path, ok := strings.Cut(string(raw), "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 || !objectID.MatchString(fields[2]) || !portablePath(path) {
			return Snapshot{}, ErrUnsupported
		}
		folded := strings.ToLower(path)
		if names[folded] {
			return Snapshot{}, ErrUnsupported
		}
		names[folded] = true
		records = append(records, record{fields[0], fields[2], path})
	}
	var inputs []string
	var entries []Entry
	for _, r := range records {
		mode, err := strconv.ParseUint(r.mode, 8, 32)
		if err != nil {
			return Snapshot{}, ErrUnavailable
		}
		marker := func(dir string) bool { return names[strings.ToLower(dir)+"/pyvenv.cfg"] }
		e := Entry{Path: r.path, Kind: "file", Mode: uint32(mode) & 0777, ExcludedReason: exclusion(r.path, false, marker)}
		switch r.mode {
		case "120000":
			e.Kind = "symlink"
			e.ExcludedReason = "link_not_followed"
		case "160000":
			e.Kind = "submodule"
			e.ExcludedReason = "submodule_not_acquired"
			s.Manifest.Limitations = append(s.Manifest.Limitations, "submodule_objects_not_acquired")
		case "100644", "100755":
		default:
			return Snapshot{}, ErrUnsupported
		}
		entries = append(entries, e)
		if e.ExcludedReason == "" {
			inputs = append(inputs, r.oid)
		}
	}
	// One bounded batch reads object bytes directly: no checkout filters, hooks,
	// export-ignore attributes or source repository scripts are evaluated.
	batch, err := run(strings.NewReader(strings.Join(inputs, "\n")+"\n"), limits.MaxTotalBytes+int64(len(inputs))*256, "cat-file", "--batch")
	if err != nil {
		return Snapshot{}, err
	}
	if err = extractObjects(ctx, &s, entries, batch, inputs, limits); err != nil {
		return Snapshot{}, err
	}
	if err = Remove(gitDir); err != nil {
		return Snapshot{}, ErrUnavailable
	}
	_ = Remove(home)
	if err = finalize(ctx, &s); err != nil {
		return Snapshot{}, err
	}
	success = true
	return s, nil
}
func extractObjects(ctx context.Context, s *Snapshot, entries []Entry, batch []byte, objects []string, limits Limits) error {
	reader := bytes.NewBuffer(batch)
	idx := 0
	var total int64
	for _, entry := range entries {
		if entry.ExcludedReason != "" {
			s.Manifest.Entries = append(s.Manifest.Entries, entry)
			continue
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			return ErrUnavailable
		}
		parts := strings.Fields(line)
		if len(parts) != 3 || parts[0] != objects[idx] || parts[1] != "blob" {
			return ErrUnavailable
		}
		idx++
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || size < 0 {
			return ErrUnavailable
		}
		if size > limits.MaxFileBytes {
			return ErrLimit
		}
		total += size
		if total > limits.MaxTotalBytes {
			return ErrLimit
		}
		data := make([]byte, size)
		if _, err = io.ReadFull(reader, data); err != nil {
			return ErrUnavailable
		}
		delimiter, err := reader.ReadByte()
		if err != nil || delimiter != '\n' {
			return ErrUnavailable
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = writeFile(s.SnapshotDir, entry.Path, data, fs.FileMode(entry.Mode)); err != nil {
			return err
		}
		e := entryFor(entry.Path, data, fs.FileMode(entry.Mode))
		if bytes.HasPrefix(data, []byte("version https://git-lfs.github.com/spec/v1\n")) {
			e.Kind = "lfs_pointer"
			s.Manifest.Limitations = append(s.Manifest.Limitations, "lfs_objects_not_acquired")
		}
		s.Manifest.Entries = append(s.Manifest.Entries, e)
	}
	// With zero requested objects don't interpret git's empty-input diagnostic as a blob.
	if len(objects) > 0 && reader.Len() != 0 {
		return ErrUnavailable
	}
	return nil
}
