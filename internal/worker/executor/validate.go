package executor

import (
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"

	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	defaultStreamLimit = 1 << 20
	maxStreamLimit     = 256 << 20
	defaultGrace       = 2 * time.Second
	defaultKillWait    = 10 * time.Second
	redactedMarker     = "[REDACTED]"
)

// specError is a validation failure; code is a stable machine reason.
type specError struct {
	status Status
	code   string
	detail string
}

func (e *specError) Error() string { return e.code + ": " + e.detail }

func reject(code, format string, args ...any) *specError {
	return &specError{status: StatusRejected, code: code, detail: fmt.Sprintf(format, args...)}
}

// prepared is a validated Spec with resolved paths.
type prepared struct {
	executable string // as given (absolute)
	resolved   string // symlink-resolved executable
	dir        string // resolved cwd
	protected  []string
	tempParent string
	env        map[string]string
	stdoutCap  int64
	stderrCap  int64
	grace      time.Duration
}

func prepare(spec Spec) (*prepared, *specError) {
	if spec.Resources.RequireEnforced {
		return nil, &specError{status: StatusUnsupported, code: "resource_enforcement_unavailable",
			detail: "the native executor does not enforce CPU/memory limits"}
	}
	if spec.Timeout <= 0 {
		return nil, reject("invalid_timeout", "timeout must be > 0")
	}
	if spec.Grace < 0 {
		return nil, reject("invalid_grace", "grace must be >= 0")
	}
	p := &prepared{grace: spec.Grace, stdoutCap: spec.StdoutLimit, stderrCap: spec.StderrLimit}
	if p.grace == 0 {
		p.grace = defaultGrace
	}
	for _, c := range []*int64{&p.stdoutCap, &p.stderrCap} {
		if *c == 0 {
			*c = defaultStreamLimit
		}
		if *c < 0 || *c > maxStreamLimit {
			return nil, reject("invalid_limit", "stream limit must be within 0..%d", maxStreamLimit)
		}
	}
	for _, i := range spec.SensitiveArgs {
		if i < 0 || i >= len(spec.Args) {
			return nil, reject("invalid_sensitive_arg", "sensitive arg index %d out of range", i)
		}
	}
	for _, a := range spec.Args {
		if strings.IndexByte(a, 0) >= 0 {
			return nil, reject("invalid_argument", "argument contains NUL")
		}
	}

	// Executable: absolute, regular, executable, never searched in PATH.
	if !filepath.IsAbs(spec.Executable) {
		return nil, reject("invalid_executable", "executable must be an absolute path")
	}
	p.executable = filepath.Clean(spec.Executable)
	resolved, err := nativepath.Resolve(p.executable)
	if err != nil {
		return nil, reject("executable_not_found", "executable cannot be resolved")
	}
	p.resolved = resolved
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, reject("executable_not_regular", "executable is not a regular file")
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{p.executable, resolved} {
			switch strings.ToLower(filepath.Ext(name)) {
			case ".bat", ".cmd":
				return nil, reject("batch_script_unsupported", "batch scripts run through cmd.exe and are not direct executables")
			}
		}
	} else if info.Mode().Perm()&0o111 == 0 {
		return nil, reject("executable_not_executable", "executable has no execute permission")
	}

	// Protected roots (read-only snapshots / original sources).
	var protectedForms []string
	for _, root := range spec.ProtectedRoots {
		if !filepath.IsAbs(root) {
			return nil, reject("invalid_protected_root", "protected root must be absolute")
		}
		p.protected = append(p.protected, resolveLoose(root))
		protectedForms = append(protectedForms, filepath.Clean(root), resolveLoose(root))
	}
	// Running a tool from the read-only snapshot/original is refused whether
	// the alias or its target lies there. This is hygiene, not OS isolation.
	execForms := []string{p.executable, filepath.Join(resolveLoose(filepath.Dir(p.executable)), filepath.Base(p.executable)), p.resolved}
	for _, pr := range protectedForms {
		for _, ex := range execForms {
			if within(ex, pr) {
				return nil, reject("executable_in_protected_root", "executable is inside a protected source path")
			}
		}
	}

	// Workspace and cwd containment on resolved paths.
	if !filepath.IsAbs(spec.WorkspaceRoot) || !filepath.IsAbs(spec.Dir) {
		return nil, reject("invalid_workspace", "workspace root and dir must be absolute")
	}
	ws, err := resolveDir(spec.WorkspaceRoot)
	if err != nil {
		return nil, reject("invalid_workspace", "workspace root is not an existing directory")
	}
	dir, err := resolveDir(spec.Dir)
	if err != nil {
		return nil, reject("invalid_dir", "dir is not an existing directory")
	}
	for _, pr := range p.protected {
		if overlaps(ws, pr) {
			return nil, reject("workspace_overlaps_protected_root", "workspace root overlaps a protected source path")
		}
		if within(dir, pr) {
			return nil, reject("cwd_in_protected_root", "dir is inside a protected source path")
		}
	}
	if !within(dir, ws) {
		return nil, reject("cwd_outside_workspace", "dir is outside the workspace root")
	}
	p.dir = dir

	p.tempParent = spec.TempParent
	if p.tempParent == "" {
		p.tempParent = os.TempDir()
	}
	if !filepath.IsAbs(p.tempParent) {
		return nil, reject("invalid_temp_parent", "temp parent must be absolute")
	}
	tp, err := resolveDir(p.tempParent)
	if err != nil {
		return nil, reject("invalid_temp_parent", "temp parent is not an existing directory")
	}
	for _, pr := range p.protected {
		if overlaps(tp, pr) {
			return nil, reject("temp_overlaps_protected_root", "temp parent overlaps a protected source path")
		}
	}
	p.tempParent = tp

	env, serr := validateEnv(spec.Env)
	if serr != nil {
		return nil, serr
	}
	if spec.OpenCodeModel != "" || spec.OpenCodeProvider != nil {
		if !validOpenCode(spec.OpenCodeModel, spec.OpenCodeProvider) {
			return nil, reject("invalid_opencode_model", "invalid model identifier")
		}
		for key, value := range managedOpenCodeEnv(spec.OpenCodeModel, spec.OpenCodeProvider) {
			env[key] = value
		}
		if p := spec.OpenCodeProvider; p != nil && p.Native != nil {
			// OpenCode 1.19.5 otherwise applies /etc/opencode after the
			// isolated profile, allowing a second endpoint or plugins there
			// to override the selected native connection.
			env["OPENCODE_TEST_MANAGED_CONFIG_DIR"] = filepath.Join(dir, ".managed-opencode")
		}
	}
	p.env = env
	return p, nil
}

func resolveDir(path string) (string, error) {
	r, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(r)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	return r, nil
}

// resolveLoose resolves symlinks of the longest existing prefix so that a
// not-yet-existing protected path is still compared on real paths.
func resolveLoose(path string) string {
	path = filepath.Clean(path)
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(resolveLoose(parent), filepath.Base(path))
}

func normCase(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// within reports whether child equals parent or is below it.
func within(child, parent string) bool {
	rel, err := filepath.Rel(normCase(parent), normCase(child))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func overlaps(a, b string) bool { return within(a, b) || within(b, a) }

// reservedEnv are set by the executor to per-run directories.
var reservedEnv = []string{"HOME", "USERPROFILE", "TMPDIR", "TMP", "TEMP"}

var sensitiveExact = map[string]bool{
	"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true, "SSH_ASKPASS": true, "GIT_ASKPASS": true,
	"DATABASE_URL": true, "PGPASSWORD": true, "PGPASSFILE": true, "PGSERVICE": true,
	"PGSERVICEFILE": true, "PGSSLKEY": true, "KUBECONFIG": true, "NETRC": true,
	"GOOGLE_APPLICATION_CREDENTIALS": true, "DOCKER_CONFIG": true, "NPM_CONFIG_USERCONFIG": true,
}

var sensitiveParts = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "APIKEY",
	"PRIVATE_KEY", "CREDENTIAL", "ACCESS_KEY", "DATABASE_URL", "_DSN"}

var sensitivePrefixes = []string{"AWS_", "AZURE_", "CODEYE_DB", "ANTHROPIC_", "OPENAI_", "OPENCODE_"}

func isSensitiveEnv(key string) bool {
	k := strings.ToUpper(key)
	if sensitiveExact[k] {
		return true
	}
	for _, p := range sensitiveParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	for _, p := range sensitivePrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func validateEnv(in map[string]string) (map[string]string, *specError) {
	out := make(map[string]string, len(in))
	seen := map[string]bool{}
	for k, v := range in {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.IndexByte(v, 0) >= 0 {
			return nil, reject("invalid_env", "invalid environment entry")
		}
		norm := k
		if runtime.GOOS == "windows" {
			norm = strings.ToUpper(k)
		}
		if seen[norm] {
			return nil, reject("invalid_env", "duplicate environment key")
		}
		seen[norm] = true
		for _, r := range reservedEnv {
			if strings.EqualFold(k, r) {
				return nil, reject("reserved_env", "environment key %s is set by the executor", r)
			}
		}
		if isSensitiveEnv(k) {
			// Key name only; the value is never echoed.
			return nil, reject("sensitive_env_rejected", "credential-like environment key %s is not passed to native tools", k)
		}
		out[k] = v
	}
	return out, nil
}

func buildEnv(env map[string]string, home, tmp string) ([]string, []string) {
	all := make(map[string]string, len(env)+5)
	for k, v := range env {
		all[k] = v
	}
	all["HOME"] = home
	all["USERPROFILE"] = home
	all["TMPDIR"] = tmp
	all["TMP"] = tmp
	all["TEMP"] = tmp
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]string, 0, len(keys))
	for _, k := range keys {
		list = append(list, k+"="+all[k])
	}
	return list, keys
}

// secretValues returns the non-empty Redact values plus sensitive argument
// values, deduplicated and sorted longest first so that a secret containing
// another is masked whole.
func secretValues(spec Spec) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range spec.Redact {
		add(v)
	}
	if spec.OpenCodeProvider != nil {
		add(spec.OpenCodeProvider.APIKey)
		if n := spec.OpenCodeProvider.Native; n != nil {
			for _, value := range n.Headers {
				add(value)
				// Providers may report an authorization header's value
				// with its scheme removed.
				if scheme, token, ok := strings.Cut(value, " "); ok && strings.EqualFold(scheme, "Bearer") {
					add(token)
				}
			}
		}
	}
	for _, i := range spec.SensitiveArgs {
		if i >= 0 && i < len(spec.Args) {
			add(spec.Args[i])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func redactString(s string, values []string) string {
	for _, v := range values {
		s = strings.ReplaceAll(s, v, redactedMarker)
	}
	return s
}

func recordArgv(spec Spec, secrets []string) []string {
	sensitive := map[int]bool{}
	for _, i := range spec.SensitiveArgs {
		sensitive[i] = true
	}
	argv := []string{redactString(spec.Executable, secrets)}
	for i, a := range spec.Args {
		if sensitive[i] {
			argv = append(argv, redactedMarker)
			continue
		}
		argv = append(argv, redactString(a, secrets))
	}
	return argv
}
