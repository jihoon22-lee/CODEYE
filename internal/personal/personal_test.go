package personal

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent/procid"
)

var tools = []string{"staticcheck", "node", "ruff"}

func cfg(state string, extra string) string {
	return `{"schema_version":"0.1","state_dir":` + q(state) + `,"database":{"mode":"external"},` +
		`"source_roots":[{"label":"work","path":` + q(filepath.Join(filepath.Dir(state), "src")) + `}]` + extra + `}`
}

func q(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

func TestParseDefaultsAndStrictness(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "state")
	c, err := Parse([]byte(cfg(state, `,"agent":{"tools":{"staticcheck":`+q(filepath.Join(base, "sc"))+`}},"quota":{"workspace_bytes":5,"artifacts_bytes":7}`)), tools)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != DefaultListen || c.Agent.MaxWorkers != DefaultMaxWorkers() || c.Agent.MaxWorkers < 1 || c.Agent.MaxWorkers > 4 ||
		len(c.GitHosts) != 1 || c.GitHosts[0] != "github.com" || c.Stop.DrainTimeoutSeconds != DefaultDrainSeconds || c.Stop.CancelTimeoutSeconds != DefaultCancelSeconds || (runtime.GOOS != "windows" && c.Agent.ToolchainPath != DefaultToolchainPath) {
		t.Fatalf("defaults %+v", c)
	}
	if got := c.Quota.NodeLimits(); len(got) != 1 || got["workspace"] != 5 || c.Quota.Limit("artifacts") != 7 {
		t.Fatalf("limits %v", got)
	}
	if c.Origin() != "http://127.0.0.1:7331" {
		t.Fatal(c.Origin())
	}
	for name, raw := range map[string]string{
		"unknown field":     cfg(state, `,"secret_token":"x"`),
		"bad schema":        strings.Replace(cfg(state, ""), `"0.1"`, `"9.9"`, 1),
		"relative state":    cfg("state", ""),
		"public listen":     cfg(state, `,"listen":"0.0.0.0:7331"`),
		"no port":           cfg(state, `,"listen":"127.0.0.1:0"`),
		"unknown db mode":   strings.Replace(cfg(state, ""), `"external"`, `"sqlite"`, 1),
		"unknown tool":      cfg(state, `,"agent":{"tools":{"cppcheck2":"/x"}}`),
		"relative tool":     cfg(state, `,"agent":{"tools":{"ruff":"ruff"}}`),
		"negative quota":    cfg(state, `,"quota":{"logs_bytes":-1}`),
		"model name space":  cfg(state, `,"agent":{"model":{"name":"openai/gpt 6"}}`),
		"model timeout":     cfg(state, `,"agent":{"model":{"timeout_seconds":1801}}`),
		"relay and key":     cfg(state, `,"agent":{"model":{"relay_file":"/r","credential_file":"/k"}}`),
		"short cancel":      cfg(state, `,"stop":{"cancel_timeout_seconds":5}`),
		"low call ceiling":  cfg(state, `,"agent":{"model":{"opencode":`+q(filepath.Join(base, "oc"))+`,"name":"p/m","review_max_calls":14}}`),
		"high call ceiling": cfg(state, `,"agent":{"model":{"opencode":`+q(filepath.Join(base, "oc"))+`,"name":"p/m","review_max_calls":208}}`),
		"backup in state":   cfg(state, `,"backup_dir":`+q(filepath.Join(state, "b"))),
		"git host IP":       cfg(state, `,"git_hosts":["127.0.0.1"]`),
		"git host URL":      cfg(state, `,"git_hosts":["https://github.com"]`),
		"git host twice":    cfg(state, `,"git_hosts":["github.com","GitHub.com"]`),
		"relative git":      cfg(state, `,"agent":{"git":"git"}`),
		"trailing":          cfg(state, "") + "{}",
	} {
		if _, err := Parse([]byte(raw), tools); err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), "secret_token") && name != "unknown field" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A source root may contain the state dir ($HOME holds both); a root
	// inside the state dir is refused.
	home := `{"schema_version":"0.1","state_dir":` + q(state) + `,"database":{"mode":"external"},"source_roots":[{"label":"w","path":` + q(base) + `}]}`
	if _, err := Parse([]byte(home), tools); err != nil {
		t.Fatalf("state_dir inside a source root refused: %v", err)
	}
	bad := `{"schema_version":"0.1","state_dir":` + q(state) + `,"database":{"mode":"external"},"source_roots":[{"label":"w","path":` + q(filepath.Join(state, "x")) + `}]}`
	if _, err := Parse([]byte(bad), tools); err == nil {
		t.Fatal("a source root inside state_dir accepted")
	}
	// Explicit Git hosts are lowercased; [] turns Git sources off.
	if c, err := Parse([]byte(cfg(state, `,"git_hosts":["GHE.example:8443"]`)), tools); err != nil || len(c.GitHosts) != 1 || c.GitHosts[0] != "ghe.example:8443" {
		t.Fatalf("git hosts %v %v", c, err)
	}
	if c, err := Parse([]byte(cfg(state, `,"git_hosts":[]`)), tools); err != nil || len(c.GitHosts) != 0 {
		t.Fatalf("no git hosts %v %v", c, err)
	}
}

func TestRecordsAndIdentityArePrivate(t *testing.T) {
	if Supported() != nil {
		t.Skip("personal process management is Linux-only")
	}
	l := Layout{Root: filepath.Join(t.TempDir(), "state")}
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{l.Root, l.RunDir(), l.LogDir(), l.AgentDir(), l.ArtifactDir()} {
		st, err := os.Stat(d)
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode %v", d, st.Mode())
		}
	}
	if r, err := l.ReadRecord(RoleAgent); r != nil || err != nil {
		t.Fatal("no record expected")
	}
	if err := l.WriteRecord(Record{Role: RoleAgent, PID: os.Getpid(), StartTime: 1, Executable: "/x", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(l.RunDir(), "agent.json"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("record mode %v", st.Mode())
	}
	r, err := l.ReadRecord(RoleAgent)
	if err != nil || r.PID != os.Getpid() {
		t.Fatal(err)
	}
	// Our pid with another start time is a reused pid: never "running".
	if got := r.State(); got != ProcReused {
		t.Fatalf("state %s", got)
	}
	if err := l.WriteIdentity("u-1"); err != nil {
		t.Fatal(err)
	}
	if id, err := l.ReadIdentity(); err != nil || id.UserID != "u-1" {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.RunDir(), "controller.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadRecord(RoleController); err == nil {
		t.Fatal("corrupt record accepted")
	}
	// A group-writable state dir is restricted to 0700 again.
	if err := os.Chmod(l.Root, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(l.Root); st.Mode().Perm() != 0o700 {
		t.Fatal("state dir not restricted")
	}
	// A symlinked state dir is refused.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(l.Root, link); err != nil {
		t.Fatal(err)
	}
	if err := (Layout{Root: link}).Prepare(); err == nil {
		t.Fatal("symlinked state dir accepted")
	}
}

// TC-125 consistency part: stale records, a reused pid, an agent holding its
// lock without a record, the lock is exclusive, and a verified signal never
// reaches a process whose pid was reused.
func TestInspectFindsStaleAndUnrecordedProcesses(t *testing.T) {
	if Supported() != nil {
		t.Skip("personal process management is Linux-only")
	}
	l := Layout{Root: filepath.Join(t.TempDir(), "state")}
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	c := l.Inspect("", true)
	if len(c.Issues) != 0 || c.Running(RoleAgent) {
		t.Fatalf("empty state %+v", c)
	}
	// A process that ran and exited.
	cmd, rec, err := Spawn(RoleController, "/bin/true", nil, nil, l.Root, l.LogFile(RoleController))
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if err := l.WriteRecord(rec); err != nil {
		t.Fatal(err)
	}
	c = l.Inspect("", true)
	if strings.Join(c.Issues, ",") != IssueStaleRecord || c.Processes[0].State != ProcExited {
		t.Fatalf("stale %+v", c)
	}
	// A live process recorded with another start time (pid reuse) is not
	// ours: not running, and a verified signal does not reach it.
	sleeper := exec.Command("/bin/sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })
	reused := Record{Role: RoleAgent, PID: sleeper.Process.Pid, StartTime: 1, Executable: "/bin/sleep", StartedAt: time.Now()}
	if err := l.WriteRecord(reused); err != nil {
		t.Fatal(err)
	}
	if err := reused.Terminate(); err != ErrProcessReused {
		t.Fatalf("terminate reused: %v", err)
	}
	if err := reused.Drain(); err != ErrProcessReused {
		t.Fatalf("drain reused: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if id, err := procid.Read(sleeper.Process.Pid); err != nil || id.Zombie {
		t.Fatal("unrelated process was signalled")
	}
	if c = l.Inspect("", true); c.Processes[1].State != ProcReused {
		t.Fatalf("reused %+v", c)
	}
	_ = l.RemoveRecord(RoleAgent)
	// An agent lock held by a process without a record.
	if err := os.WriteFile(filepath.Join(l.AgentDir(), "agent.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	holder := exec.Command("/usr/bin/flock", filepath.Join(l.AgentDir(), "agent.lock"), "/bin/sleep", "60")
	if _, err := exec.LookPath("/usr/bin/flock"); err != nil {
		t.Skip("flock(1) not available")
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if held, _ := l.AgentLockHeld(); held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lock never observed held")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c = l.Inspect("", true); !strings.Contains(strings.Join(c.Issues, ","), IssueAgentWithoutRecord) {
		t.Fatalf("unrecorded agent %+v", c)
	}
	if c = l.Inspect("", false); strings.Contains(strings.Join(c.Issues, ","), IssueAgentWithoutRecord) {
		t.Fatal("lock probed although disabled")
	}
	// The personal lock is exclusive.
	unlock, err := l.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Lock(); err != ErrBusy {
		t.Fatalf("second lock: %v", err)
	}
	unlock()
	if runtime.GOOS != "linux" {
		t.Skip()
	}
}
