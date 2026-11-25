// Package run is the worker process main loop (`codeye worker`). A worker
// executes one attempt at a time, talks only to its agent over the
// codeye-worker/0.1 JSONL pipes, and holds no HTTP client or credential.
package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// HandlerFactory builds the handlers of this build from the agent hello. It
// may probe toolchains through runner (a plain executor: probes run before
// any attempt and are not journaled). unavailable lists "name:reason" codes
// of known handlers that are not advertised.
type HandlerFactory func(ctx context.Context, h *workerproto.Hello, runner executor.Runner) (hs []analysis.Handler, unavailable []string, err error)

// Config wires a worker.
type Config struct {
	In       io.Reader
	Out      io.Writer
	Stderr   io.Writer
	Handlers HandlerFactory
	// PrepareAttempt fixes local adapter settings before source acquisition
	// and returns any additional host paths local projects must not contain.
	PrepareAttempt func(scratch string) []string
	// SerialModel reports whether the model the attempt in scratch resolved
	// (PrepareAttempt) serves one request at a time, as a budget relay does;
	// the attempt's review pairs then run one after another. Nil: only the
	// hello's relay counts.
	SerialModel func(scratch string) bool
	// RefreshHandlers selects local toolchains once before acquiring the source.
	RefreshHandlers func(context.Context, executor.Runner) (*analysis.Registry, error)
	// Runner executes processes; default is a zero executor.Executor.
	Runner executor.Runner
	// HelloTimeout bounds the wait for hello (default 30s).
	HelloTimeout time.Duration
	// ReplyTimeout bounds journal replies (intent/process_started, default 5s).
	ReplyTimeout time.Duration
}

// Exit codes.
const (
	ExitOK       = 0
	ExitProtocol = 3
	ExitSetup    = 4
)

type worker struct {
	cfg      Config
	conn     *workerproto.Conn
	hello    *workerproto.Hello
	registry *analysis.Registry
	runner   executor.Runner

	mu        sync.Mutex
	current   *attempt
	stopNew   bool
	shutdown  bool
	jobs      chan *attempt
	connDone  chan struct{}
	logWriter io.Writer
}

// Main runs the worker until shutdown, stdin EOF or ctx end. On EOF/ctx end
// a running attempt is cancelled and its processes cleaned before return.
func Main(ctx context.Context, cfg Config) int {
	if cfg.Runner == nil {
		cfg.Runner = &executor.Executor{}
	}
	if cfg.HelloTimeout <= 0 {
		cfg.HelloTimeout = 30 * time.Second
	}
	if cfg.ReplyTimeout <= 0 {
		cfg.ReplyTimeout = 5 * time.Second
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	w := &worker{cfg: cfg, runner: cfg.Runner, jobs: make(chan *attempt, 1), logWriter: cfg.Stderr}
	w.conn = workerproto.NewConn(cfg.In, cfg.Out)
	w.conn.Start()

	hello, code := w.awaitHello(ctx)
	if hello == nil {
		return code
	}
	w.hello = hello
	hs, unavailable, err := cfg.Handlers(ctx, hello, w.runner)
	for _, u := range unavailable {
		w.log("warn", "handler_unavailable", u)
	}
	if err == nil {
		w.registry, err = analysis.NewRegistry(hs...)
	}
	if err != nil {
		fmt.Fprintln(w.logWriter, "codeye worker: handler setup failed")
		return ExitSetup
	}
	ready := &workerproto.Ready{Protocol: workerproto.Protocol, PID: os.Getpid(), Handlers: []workerproto.HandlerInfo{}}
	for _, info := range w.registry.Infos() {
		ready.Handlers = append(ready.Handlers, workerproto.HandlerInfo{Name: info.Name, Version: info.Version, Capability: info.Capability})
	}
	if err := w.conn.Send(ready); err != nil {
		return ExitProtocol
	}

	w.connDone = make(chan struct{})
	go w.control(ctx)
	for a := range w.jobs {
		w.runJob(ctx, a)
		w.mu.Lock()
		w.current = nil
		stop := w.shutdown
		w.mu.Unlock()
		if stop {
			break
		}
		select {
		case <-w.connDone:
		default:
			_ = w.conn.Send(&workerproto.Idle{})
		}
	}
	if err := w.conn.Err(); err != nil && errors.Is(err, workerproto.ErrProtocol) {
		return ExitProtocol
	}
	return ExitOK
}

func (w *worker) awaitHello(ctx context.Context) (*workerproto.Hello, int) {
	timer := time.NewTimer(w.cfg.HelloTimeout)
	defer timer.Stop()
	select {
	case m, ok := <-w.conn.Incoming():
		h, isHello := m.(*workerproto.Hello)
		if !ok || !isHello || h.Protocol != workerproto.Protocol || h.WorkerID == "" || !filepath.IsAbs(h.DataRoot) || !filepath.IsAbs(h.ScratchRoot) {
			fmt.Fprintln(w.logWriter, "codeye worker: expected hello")
			return nil, ExitProtocol
		}
		return h, ExitOK
	case <-timer.C:
		return nil, ExitProtocol
	case <-ctx.Done():
		return nil, ExitOK
	}
}

// control consumes agent messages. It never blocks on job execution, so a
// cancel is applied while a task runs.
func (w *worker) control(ctx context.Context) {
	defer close(w.jobs)
	defer close(w.connDone)
	in := w.conn.Incoming()
	for {
		select {
		case <-ctx.Done():
			w.cancelCurrent(workerproto.CancelShutdown)
			w.setShutdown()
			return
		case m, ok := <-in:
			if !ok {
				// stdin EOF or protocol error: cancel and exit.
				w.cancelCurrent(workerproto.CancelShutdown)
				w.setShutdown()
				return
			}
			switch v := m.(type) {
			case *workerproto.Job:
				w.mu.Lock()
				busy := w.current != nil || len(w.jobs) > 0
				w.mu.Unlock()
				if busy {
					w.log("warn", "job_while_busy", "job ignored: worker busy")
					continue
				}
				a := newAttempt(v)
				w.mu.Lock()
				w.current = a
				w.stopNew = false
				w.mu.Unlock()
				w.jobs <- a
			case *workerproto.Cancel:
				w.cancelCurrent(v.Reason)
			case *workerproto.StopNewTasks:
				w.mu.Lock()
				w.stopNew = true
				if w.current != nil {
					w.current.stopNew.Store(true)
				}
				w.mu.Unlock()
			case *workerproto.Shutdown:
				w.mu.Lock()
				idle := w.current == nil
				w.shutdown = true
				w.mu.Unlock()
				if idle {
					return
				}
			default:
				w.log("warn", "unexpected_message", "unexpected message type")
			}
		}
	}
}

func (w *worker) setShutdown() {
	w.mu.Lock()
	w.shutdown = true
	w.mu.Unlock()
}

func (w *worker) cancelCurrent(reason string) {
	w.mu.Lock()
	a := w.current
	w.mu.Unlock()
	if a != nil {
		a.cancel(reason)
	}
}

func (w *worker) log(level, code, msg string) {
	if len(msg) > 2048 {
		msg = msg[:2048]
	}
	_ = w.conn.Send(&workerproto.Log{Level: level, Code: code, Message: msg})
}
