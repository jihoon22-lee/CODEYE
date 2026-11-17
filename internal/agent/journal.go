package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Journal record kinds (contract §7).
const (
	RecWorkerStarted   = "worker_started"
	RecWorkerExited    = "worker_exited"
	RecAttemptAssigned = "attempt_assigned"
	RecAttemptDone     = "attempt_done"
	RecIntent          = "intent"
	RecProcessStarted  = "process_started"
	RecProcessEnded    = "process_ended"
	RecCommandApplied  = "command_applied"
	RecClaimCounter    = "claim_counter"
	RecCleanupPending  = "cleanup_pending"
	RecCleanupAcked    = "cleanup_acked"
)

// Record is one journal line. Fields are kind-specific; empty ones omitted.
type Record struct {
	Kind         string    `json:"kind"`
	At           time.Time `json:"at"`
	WorkerID     string    `json:"worker_id,omitempty"`
	AttemptID    string    `json:"attempt_id,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	Epoch        int64     `json:"epoch,omitempty"`
	PID          int       `json:"pid,omitempty"`
	StartTime    uint64    `json:"start_time,omitempty"`
	PGID         int       `json:"pgid,omitempty"`
	Containment  string    `json:"containment,omitempty"`
	TaskKey      string    `json:"task_key,omitempty"`
	Ordinal      int       `json:"ordinal,omitempty"`
	Intent       string    `json:"intent,omitempty"`
	Result       string    `json:"result,omitempty"`
	Exit         string    `json:"exit,omitempty"`
	CommandID    string    `json:"command_id,omitempty"`
	Sequence     int64     `json:"sequence,omitempty"`
	Outcome      string    `json:"outcome,omitempty"`
	CleanupState string    `json:"cleanup_state,omitempty"`
	Method       string    `json:"method,omitempty"`
	VerifiedAt   time.Time `json:"verified_at,omitzero"`
	Cleanup      string    `json:"cleanup,omitempty"`
	Counter      int64     `json:"counter,omitempty"`
}

// JWorker is a journaled worker process.
type JWorker struct {
	ID        string
	PID       int
	StartTime uint64
	PGID      int
	Exited    bool
}

// JProc is a journaled task process (keyed by per-attempt ordinal).
type JProc struct {
	TaskKey     string
	PID         int
	StartTime   uint64
	PGID        int
	Containment string
	Started     bool
	Ended       bool
	Cleanup     string
}

// JAttempt is the journaled state of one attempt.
type JAttempt struct {
	ID, WorkerID, RunID string
	Epoch               int64
	Done                bool
	Outcome             string
	CleanupState        string
	// SourceOpen: source_acquire_started without source_acquire_done.
	SourceOpen bool
	// Procs by ordinal: task_exec_started opens one; process_ended closes it.
	Procs map[int]*JProc
	// Uncertain is set when a process ended with uncertain cleanup.
	Uncertain bool
}

// Report is a pending cleanup report.
type Report struct {
	AttemptID, WorkerID string
	Epoch               int64
	CleanupState        string
	Method              string
	VerifiedAt          time.Time
}

// Journal is the agent's append-only, fsynced process journal. Its state is
// rebuilt on open; a torn final line (crash mid-write) is cut off and
// recorded as Torn, which makes every unfinished attempt uncertain.
type Journal struct {
	mu   sync.Mutex
	path string
	f    *os.File

	Workers       map[string]*JWorker
	Attempts      map[string]*JAttempt
	Commands      map[string]int64
	LastSequence  int64
	ClaimCounters map[string]int64
	Pending       map[string]*Report
	Torn          bool
}

// OpenJournal opens (creating 0600) and replays path.
func OpenJournal(path string) (*Journal, error) {
	j := &Journal{path: path}
	j.reset()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	good, err := j.replay(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Truncate(good); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(good, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	j.f = f
	return j, nil
}

func (j *Journal) reset() {
	j.Workers = map[string]*JWorker{}
	j.Attempts = map[string]*JAttempt{}
	j.Commands = map[string]int64{}
	j.ClaimCounters = map[string]int64{}
	j.Pending = map[string]*Report{}
	j.LastSequence = 0
}

// replay applies every complete line and returns the offset after the last
// one. A trailing line without newline, or an undecodable line, is torn.
func (j *Journal) replay(f *os.File) (int64, error) {
	r := bufio.NewReader(f)
	var good int64
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			var rec Record
			if json.Unmarshal(bytes.TrimSpace(line), &rec) != nil || rec.Kind == "" {
				// Corrupt line: stop replaying, everything after is untrusted.
				j.Torn = true
				return good, nil
			}
			j.apply(rec)
			good += int64(len(line))
			continue
		}
		if len(line) > 0 {
			j.Torn = true
		}
		if err == nil || errors.Is(err, io.EOF) {
			return good, nil
		}
		return good, err
	}
}

func (j *Journal) attempt(id string) *JAttempt {
	a := j.Attempts[id]
	if a == nil {
		a = &JAttempt{ID: id, Procs: map[int]*JProc{}}
		j.Attempts[id] = a
	}
	return a
}

func (j *Journal) apply(r Record) {
	switch r.Kind {
	case RecWorkerStarted:
		j.Workers[r.WorkerID] = &JWorker{ID: r.WorkerID, PID: r.PID, StartTime: r.StartTime, PGID: r.PGID}
	case RecWorkerExited:
		if w := j.Workers[r.WorkerID]; w != nil && w.PID == r.PID && w.StartTime == r.StartTime {
			w.Exited = true
		}
	case RecAttemptAssigned:
		a := j.attempt(r.AttemptID)
		a.WorkerID, a.RunID, a.Epoch = r.WorkerID, r.RunID, r.Epoch
	case RecAttemptDone:
		a := j.attempt(r.AttemptID)
		a.Done, a.Outcome, a.CleanupState = true, r.Outcome, r.CleanupState
	case RecIntent:
		a := j.attempt(r.AttemptID)
		switch r.Intent {
		case "source_acquire_started":
			a.SourceOpen = true
		case "source_acquire_done":
			a.SourceOpen = false
			if r.Result == "recovery_required" {
				a.Uncertain = true
			}
		case "task_exec_started":
			a.Procs[r.Ordinal] = &JProc{TaskKey: r.TaskKey}
		}
	case RecProcessStarted:
		a := j.attempt(r.AttemptID)
		p := a.Procs[r.Ordinal]
		if p == nil {
			p = &JProc{TaskKey: r.TaskKey}
			a.Procs[r.Ordinal] = p
		}
		p.PID, p.StartTime, p.PGID, p.Containment, p.Started = r.PID, r.StartTime, r.PGID, r.Containment, true
	case RecProcessEnded:
		a := j.attempt(r.AttemptID)
		p := a.Procs[r.Ordinal]
		if p == nil {
			p = &JProc{TaskKey: r.TaskKey}
			a.Procs[r.Ordinal] = p
		}
		p.Ended, p.Cleanup = true, r.Cleanup
		if r.Cleanup == "uncertain" {
			a.Uncertain = true
		}
	case RecCommandApplied:
		j.Commands[r.CommandID] = r.Sequence
		if r.Sequence > j.LastSequence {
			j.LastSequence = r.Sequence
		}
	case RecClaimCounter:
		if r.Counter > j.ClaimCounters[r.WorkerID] {
			j.ClaimCounters[r.WorkerID] = r.Counter
		}
	case RecCleanupPending:
		j.Pending[r.AttemptID] = &Report{AttemptID: r.AttemptID, WorkerID: r.WorkerID, Epoch: r.Epoch, CleanupState: r.CleanupState, Method: r.Method, VerifiedAt: r.VerifiedAt}
	case RecCleanupAcked:
		delete(j.Pending, r.AttemptID)
	}
}

// Append writes r, fsyncs, and applies it to the in-memory state.
func (j *Journal) Append(r Record) error {
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return errors.New("journal closed")
	}
	if _, err := j.f.Write(b); err != nil {
		return err
	}
	if err := j.f.Sync(); err != nil {
		return err
	}
	j.apply(r)
	return nil
}

// With runs fn under the journal lock for consistent reads of the state.
func (j *Journal) With(fn func(j *Journal)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(j)
}

// Close closes the file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// Compact rewrites the journal keeping only what recovery still needs:
// unfinished workers/attempts, pending reports, recent commands and claim
// counters. The new file is fsynced and atomically renamed.
func (j *Journal) Compact() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	var recs []Record
	now := time.Now().UTC()
	// Keep running workers and every worker an unfinished attempt refers to
	// (its exit is the positive evidence reconciliation needs).
	needed := map[string]bool{}
	for _, a := range j.Attempts {
		if !a.Done {
			needed[a.WorkerID] = true
		}
	}
	for _, w := range j.Workers {
		if !w.Exited || needed[w.ID] {
			recs = append(recs, Record{Kind: RecWorkerStarted, At: now, WorkerID: w.ID, PID: w.PID, StartTime: w.StartTime, PGID: w.PGID})
			if w.Exited {
				recs = append(recs, Record{Kind: RecWorkerExited, At: now, WorkerID: w.ID, PID: w.PID, StartTime: w.StartTime, Exit: "compacted"})
			}
		}
	}
	for _, a := range j.Attempts {
		if a.Done {
			continue
		}
		recs = append(recs, Record{Kind: RecAttemptAssigned, At: now, AttemptID: a.ID, WorkerID: a.WorkerID, RunID: a.RunID, Epoch: a.Epoch})
		if a.SourceOpen {
			recs = append(recs, Record{Kind: RecIntent, At: now, AttemptID: a.ID, Intent: "source_acquire_started"})
		}
		for ord, p := range a.Procs {
			recs = append(recs, Record{Kind: RecIntent, At: now, AttemptID: a.ID, Intent: "task_exec_started", TaskKey: p.TaskKey, Ordinal: ord})
			if p.Started {
				recs = append(recs, Record{Kind: RecProcessStarted, At: now, AttemptID: a.ID, TaskKey: p.TaskKey, Ordinal: ord, PID: p.PID, StartTime: p.StartTime, PGID: p.PGID, Containment: p.Containment})
			}
			if p.Ended {
				recs = append(recs, Record{Kind: RecProcessEnded, At: now, AttemptID: a.ID, TaskKey: p.TaskKey, Ordinal: ord, Cleanup: p.Cleanup})
			}
		}
	}
	for _, p := range j.Pending {
		recs = append(recs, Record{Kind: RecCleanupPending, At: now, AttemptID: p.AttemptID, WorkerID: p.WorkerID, Epoch: p.Epoch, CleanupState: p.CleanupState, Method: p.Method, VerifiedAt: p.VerifiedAt})
	}
	type cmd struct {
		id  string
		seq int64
	}
	var cmds []cmd
	for id, seq := range j.Commands {
		cmds = append(cmds, cmd{id, seq})
	}
	sort.Slice(cmds, func(a, b int) bool { return cmds[a].seq > cmds[b].seq })
	if len(cmds) > 256 {
		cmds = cmds[:256]
	}
	for _, c := range cmds {
		recs = append(recs, Record{Kind: RecCommandApplied, At: now, CommandID: c.id, Sequence: c.seq})
	}
	if j.LastSequence > 0 && len(cmds) == 0 {
		recs = append(recs, Record{Kind: RecCommandApplied, At: now, CommandID: "cursor", Sequence: j.LastSequence})
	}
	for w, c := range j.ClaimCounters {
		recs = append(recs, Record{Kind: RecClaimCounter, At: now, WorkerID: w, Counter: c})
	}
	tmp := j.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, r := range recs {
		b, _ := json.Marshal(r)
		w.Write(append(b, '\n'))
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Rename(tmp, j.path); err != nil {
		return err
	}
	syncDir(filepath.Dir(j.path))
	nf, err := os.OpenFile(j.path, os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("reopen journal: %w", err)
	}
	j.f.Close()
	j.f = nf
	// Torn data was dropped by the rewrite, but the uncertainty it implied was
	// already turned into reports by reconciliation.
	j.Torn = false
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// ReadJournal replays path read-only (diagnostics/tests); it never truncates.
func ReadJournal(path string) (*Journal, error) {
	j := &Journal{path: path}
	j.reset()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := j.replay(f); err != nil {
		return nil, err
	}
	return j, nil
}
