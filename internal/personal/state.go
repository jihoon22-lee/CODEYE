package personal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent/procid"
)

// Process roles recorded in the state directory.
const (
	RoleController = "controller"
	RoleAgent      = "agent"
)

// Roles in start order.
var Roles = []string{RoleController, RoleAgent}

// Layout is the state directory (0700) of one personal installation.
type Layout struct{ Root string }

func (l Layout) RunDir() string       { return filepath.Join(l.Root, "run") }
func (l Layout) LogDir() string       { return filepath.Join(l.Root, "logs") }
func (l Layout) AgentDir() string     { return filepath.Join(l.Root, "agent") }
func (l Layout) ArtifactDir() string  { return filepath.Join(l.Root, "artifacts") }
func (l Layout) IdentityFile() string { return filepath.Join(l.Root, "personal.json") }
func (l Layout) LoginLink() string    { return filepath.Join(l.Root, "login-link") }
func (l Layout) EnrollToken() string  { return filepath.Join(l.RunDir(), "enroll-token") }
func (l Layout) LockFile() string     { return filepath.Join(l.Root, "personal.lock") }
func (l Layout) LogFile(role string) string {
	return filepath.Join(l.LogDir(), role+".log")
}
func (l Layout) recordFile(role string) string { return filepath.Join(l.RunDir(), role+".json") }

// AgentNodeFile is the agent's enrolled identity (it also holds the node
// secret; only node_id is ever read here).
func (l Layout) AgentNodeFile() string { return filepath.Join(l.AgentDir(), "node.json") }

// Prepare creates the state directory tree with mode 0700 and refuses a
// directory that is not a directory, is a symlink, or that others can write.
func (l Layout) Prepare() error {
	if err := os.MkdirAll(l.Root, 0o700); err != nil {
		return errors.New("cannot create state_dir")
	}
	if err := checkPrivateDir(l.Root); err != nil {
		return err
	}
	for _, d := range []string{l.RunDir(), l.LogDir(), l.AgentDir(), l.ArtifactDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return errors.New("cannot create the state directory tree")
		}
		if err := checkPrivateDir(d); err != nil {
			return err
		}
	}
	return nil
}

// Record is one process started by `codeye start`: pid plus the OS start
// time marker, so stop never signals a process that reused the pid.
type Record struct {
	SchemaVersion string    `json:"schema_version"`
	Role          string    `json:"role"`
	PID           int       `json:"pid"`
	StartTime     uint64    `json:"start_time"`
	Executable    string    `json:"executable"`
	StartedAt     time.Time `json:"started_at"`
	// Listen is the controller's address (it may differ from the configured
	// default when that port was busy).
	Listen string `json:"listen,omitempty"`
	// Args is the agent argv without the one-time enrollment flag; a
	// different argv from the configuration means the agent is restarted.
	Args []string `json:"args,omitempty"`
}

// Process states.
const (
	ProcRunning  = "running"    // the recorded process is alive
	ProcExited   = "exited"     // no process with the recorded identity
	ProcReused   = "pid_reused" // the pid now belongs to another process (ours exited)
	ProcNone     = "not_recorded"
	ProcUnknown  = "unknown" // identity could not be read
	recordSchema = "0.1"
)

// ReadRecord returns the record of role, or (nil, nil) when none exists.
func (l Layout) ReadRecord(role string) (*Record, error) {
	b, err := os.ReadFile(l.recordFile(role))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil || r.Role != role || r.PID <= 1 || r.SchemaVersion != recordSchema {
		return nil, fmt.Errorf("process record %s is corrupt", filepath.Base(l.recordFile(role)))
	}
	return &r, nil
}

// WriteRecord stores r (0600, atomic rename).
func (l Layout) WriteRecord(r Record) error {
	r.SchemaVersion = recordSchema
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writePrivate(l.recordFile(r.Role), b)
}

// RemoveRecord deletes the record of role (missing is fine).
func (l Layout) RemoveRecord(role string) error {
	err := os.Remove(l.recordFile(role))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// State of a recorded process.
func (r *Record) State() string {
	if r == nil {
		return ProcNone
	}
	st, err := procid.Check(r.PID, r.StartTime)
	if err != nil {
		return ProcUnknown
	}
	switch st {
	case procid.Same:
		return ProcRunning
	case procid.Reused:
		return ProcReused
	}
	return ProcExited
}

// Identity is the personal installation's identity file (no secret).
type Identity struct {
	SchemaVersion string `json:"schema_version"`
	UserID        string `json:"user_id"`
}

// ReadIdentity returns the personal user, or (nil, nil) when none is stored.
func (l Layout) ReadIdentity() (*Identity, error) {
	b, err := os.ReadFile(l.IdentityFile())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil || id.UserID == "" || id.SchemaVersion != recordSchema {
		return nil, errors.New("personal.json is corrupt")
	}
	return &id, nil
}

// WriteIdentity stores the personal user (0600).
func (l Layout) WriteIdentity(userID string) error {
	b, err := json.Marshal(Identity{SchemaVersion: recordSchema, UserID: userID})
	if err != nil {
		return err
	}
	return writePrivate(l.IdentityFile(), b)
}

// AgentNodeID returns the enrolled node ID of the local agent ("" if not
// enrolled). Only node_id is decoded; the secret is never copied.
func (l Layout) AgentNodeID() (string, error) {
	b, err := os.ReadFile(l.AgentNodeFile())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var id struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(b, &id); err != nil || id.NodeID == "" {
		return "", errors.New("agent node.json is corrupt")
	}
	return id.NodeID, nil
}

// writePrivate writes data to path with mode 0600 through a same-directory
// rename (a symlink at path is replaced, never followed).
// WriteRunFile replaces <state>/run/name with data (0600, atomically).
func (l Layout) WriteRunFile(name string, data []byte) error {
	if name == "" || filepath.Base(name) != name {
		return errors.New("invalid run file name")
	}
	if err := os.MkdirAll(l.RunDir(), 0o700); err != nil {
		return err
	}
	return writePrivate(filepath.Join(l.RunDir(), name), data)
}

func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
