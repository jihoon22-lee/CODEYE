package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
)

// `codeye service install|uninstall|print` (docs/OPERATIONS.md#사용자-서비스): a
// systemd user unit that keeps personal mode running after the terminal
// closes. The unit runs `codeye start` (it never prints a login link, so no
// token reaches the journal) and `codeye stop` when the service stops.
// codeye start spawns its processes and exits, so the unit is oneshot with
// RemainAfterExit: systemd keeps the processes in the unit's cgroup and runs
// ExecStop first; whatever `codeye stop` leaves behind (recovery_required)
// then gets SIGTERM, the agent's cancel-and-confirm path.

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// serviceUnit is the unit text. exe and config are absolute; path is the
// PATH codeye start resolves the tools and PostgreSQL programs with (a
// user manager does not read the login shell's PATH).
func serviceUnit(exe, config, path string, stop time.Duration) string {
	quote := func(s string) string {
		// systemd command lines split on spaces; quote and escape the path.
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s) + `"`
	}
	env := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(path)
	return "# Written by codeye service install. Remove with codeye service uninstall.\n" +
		"[Unit]\n" +
		"Description=CODEYE personal mode (PostgreSQL, controller, agent)\n" +
		"\n" +
		"[Service]\n" +
		"Type=oneshot\n" +
		"RemainAfterExit=yes\n" +
		`Environment="PATH=` + env + "\"\n" +
		"Environment=LANG=C.UTF-8\n" +
		"ExecStart=" + quote(exe) + " start --config " + quote(config) + "\n" +
		"ExecStop=" + quote(exe) + " stop --config " + quote(config) + "\n" +
		"TimeoutStartSec=300\n" +
		"TimeoutStopSec=" + strconv.Itoa(int(stop/time.Second)) + "\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
}

// userUnitDir is $XDG_CONFIG_HOME/systemd/user (default ~/.config).
func userUnitDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "systemd", "user"), nil
}

func serviceCommand(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "install" && args[0] != "uninstall" && args[0] != "print") {
		fmt.Fprintln(stderr, "usage: codeye service install|uninstall|print [--config PATH] [--name codeye]")
		return 2
	}
	action := args[0]
	var name, config string
	if !parse("service "+action, args[1:], stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "codeye", "")
		fs.StringVar(&config, "config", "", "")
	}) {
		return 2
	}
	config, err := personalConfigPath(config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	// The unit names this file: it must already be a valid configuration.
	cfg, err := personal.Load(config, knownTools())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !serviceName.MatchString(name) {
		fmt.Fprintln(stderr, "--name must be lower-case letters, digits and dashes")
		return 2
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "codeye service writes a systemd user unit and works on Linux only")
		return 1
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintln(stderr, "cannot resolve the codeye executable")
		return 1
	}
	// The stop budget: the drain, then the cancellation, then a minute for
	// the controller and the managed PostgreSQL.
	stop := time.Duration(cfg.Stop.DrainTimeoutSeconds+cfg.Stop.CancelTimeoutSeconds+60) * time.Second
	unit := serviceUnit(exe, config, os.Getenv("PATH"), stop)
	if action == "print" {
		fmt.Fprint(out, unit)
		return 0
	}
	dir, err := userUnitDir()
	if err != nil {
		fmt.Fprintln(stderr, "cannot find the user configuration directory")
		return 1
	}
	file := filepath.Join(dir, name+".service")
	if action == "uninstall" {
		systemctl(ctx, out, "disable", "--now", name+".service")
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "cannot remove", file)
			return 1
		}
		systemctl(ctx, out, "daemon-reload")
		fmt.Fprintln(out, "removed", file)
		return 0
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(stderr, "cannot create", dir)
		return 1
	}
	if existing, err := os.ReadFile(file); err == nil && !strings.HasPrefix(string(existing), "# Written by codeye service install.") {
		fmt.Fprintln(stderr, file, "exists and was not written by codeye; not replaced")
		return 1
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(unit), 0o644); err != nil {
		fmt.Fprintln(stderr, "cannot write", file)
		return 1
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintln(stderr, "cannot write", file)
		return 1
	}
	fmt.Fprintln(out, "wrote", file)
	reached := systemctl(ctx, out, "daemon-reload") && systemctl(ctx, out, "enable", name+".service")
	if !reached {
		fmt.Fprintln(out, "systemctl --user was not reachable; run: systemctl --user daemon-reload && systemctl --user enable --now "+name+".service")
	}
	fmt.Fprintln(out, "start now:            systemctl --user start "+name+".service")
	fmt.Fprintln(out, "keep after logout:    loginctl enable-linger "+currentUser())
	fmt.Fprintln(out, "status and the link:  codeye status; codeye open (a new sign-in link)")
	return 0
}

// systemctl runs systemctl --user with argv (no shell). A missing
// XDG_RUNTIME_DIR (a shell without a login session) is filled with the
// user's runtime directory when it exists. It reports success.
func systemctl(ctx context.Context, out io.Writer, args ...string) bool {
	bin, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, append([]string{"--user"}, args...)...)
	cmd.Env = os.Environ()
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		dir := "/run/user/" + strconv.Itoa(os.Getuid())
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			cmd.Env = append(cmd.Env, "XDG_RUNTIME_DIR="+dir)
		}
	}
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run() == nil
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "$USER"
}
