package workerproto

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

var (
	// ErrProtocol: malformed, oversized or unknown message. The peer must be
	// treated as broken (the agent cancels the worker).
	ErrProtocol = errors.New("worker_protocol_error")
	// ErrClosed: the connection ended before a reply arrived.
	ErrClosed = errors.New("worker_connection_closed")
)

// Conn is one side of the JSONL connection. Replies are routed to the
// waiting Request by reply_to; every other message is delivered on
// Incoming() in arrival order. The reader never blocks on a reply, so a slow
// consumer of Incoming() cannot stall Request.
type Conn struct {
	r   *bufio.Reader
	w   io.Writer
	wmu sync.Mutex

	pmu     sync.Mutex
	pending map[string]chan *Reply
	closed  bool

	incoming chan any
	done     chan struct{}
	errMu    sync.Mutex
	err      error

	// Transcript, if set before Start, receives a copy of every line sent and
	// received (tests check it for secrets).
	Transcript io.Writer
	tmu        sync.Mutex
}

// NewConn wraps a reader (peer→us) and a writer (us→peer).
func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{r: bufio.NewReaderSize(r, 64<<10), w: w, pending: map[string]chan *Reply{}, incoming: make(chan any, 256), done: make(chan struct{})}
}

// Start begins reading. Incoming is closed when the stream ends or fails.
func (c *Conn) Start() { go c.readLoop() }

// Incoming delivers non-reply messages (pointers to message structs).
func (c *Conn) Incoming() <-chan any { return c.incoming }

// Done is closed when reading stopped; Err tells why (nil on clean EOF).
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns the terminal read error, nil for EOF.
func (c *Conn) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

func (c *Conn) record(dir string, line []byte) {
	if c.Transcript == nil {
		return
	}
	c.tmu.Lock()
	defer c.tmu.Unlock()
	_, _ = c.Transcript.Write([]byte(dir))
	_, _ = c.Transcript.Write(line)
	if len(line) == 0 || line[len(line)-1] != '\n' {
		_, _ = c.Transcript.Write([]byte("\n"))
	}
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > MaxLine {
			return nil, fmt.Errorf("%w: line exceeds %d bytes", ErrProtocol, MaxLine)
		}
		line = append(line, chunk...)
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return nil, fmt.Errorf("%w: truncated final line", ErrProtocol)
		}
		return nil, err
	}
}

// Decode parses one line strictly into its typed message.
func Decode(line []byte) (any, error) {
	line = bytes.TrimRight(line, "\r\n")
	var h Header
	if err := json.Unmarshal(line, &h); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	m := newByType(h.Type)
	if m == nil || h.ID == "" {
		return nil, fmt.Errorf("%w: unknown type or missing id", ErrProtocol)
	}
	if err := domain.DecodeStrict(line, m); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrProtocol, h.Type)
	}
	return m, nil
}

func (c *Conn) readLoop() {
	defer func() {
		close(c.incoming)
		c.pmu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.pmu.Unlock()
		close(c.done)
	}()
	for {
		line, err := readLine(c.r)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.setErr(err)
			}
			return
		}
		c.record("< ", line)
		m, err := Decode(line)
		if err != nil {
			c.setErr(err)
			return
		}
		if r, ok := m.(*Reply); ok {
			c.pmu.Lock()
			ch := c.pending[r.ReplyTo]
			delete(c.pending, r.ReplyTo)
			c.pmu.Unlock()
			if ch != nil {
				ch <- r
			}
			continue
		}
		c.incoming <- m
	}
}

func (c *Conn) setErr(err error) {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

// typeOf returns the wire type of a message pointer.
func typeOf(m any) string {
	switch m.(type) {
	case *Hello:
		return TypeHello
	case *Job:
		return TypeJob
	case *Reply:
		return TypeReply
	case *StopNewTasks:
		return TypeStopNewTasks
	case *Cancel:
		return TypeCancel
	case *Shutdown:
		return TypeShutdown
	case *Ready:
		return TypeReady
	case *Event:
		return TypeEvent
	case *Artifact:
		return TypeArtifact
	case *Intent:
		return TypeIntent
	case *ProcessStarted:
		return TypeProcessStarted
	case *ProcessEnded:
		return TypeProcessEnded
	case *Complete:
		return TypeComplete
	case *Idle:
		return TypeIdle
	case *Log:
		return TypeLog
	}
	return ""
}

// Send writes one message, filling Type and a fresh ID when empty.
func (c *Conn) Send(m any) error {
	h := header(m)
	if h == nil {
		return fmt.Errorf("%w: unsupported message %T", ErrProtocol, m)
	}
	h.Type = typeOf(m)
	if h.ID == "" {
		h.ID = domain.NewID()
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxLine {
		return fmt.Errorf("%w: message exceeds %d bytes", ErrProtocol, MaxLine)
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.record("> ", b)
	_, err = c.w.Write(b)
	return err
}

// Request sends m and waits for the matching reply or ctx end.
func (c *Conn) Request(ctx context.Context, m any) (*Reply, error) {
	h := header(m)
	if h == nil {
		return nil, fmt.Errorf("%w: unsupported message %T", ErrProtocol, m)
	}
	if h.ID == "" {
		h.ID = domain.NewID()
	}
	ch := make(chan *Reply, 1)
	c.pmu.Lock()
	if c.closed {
		c.pmu.Unlock()
		return nil, ErrClosed
	}
	c.pending[h.ID] = ch
	c.pmu.Unlock()
	if err := c.Send(m); err != nil {
		c.pmu.Lock()
		delete(c.pending, h.ID)
		c.pmu.Unlock()
		return nil, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, ErrClosed
		}
		return r, nil
	case <-ctx.Done():
		c.pmu.Lock()
		delete(c.pending, h.ID)
		c.pmu.Unlock()
		return nil, ctx.Err()
	}
}

// ReplyOK answers request id with an optional result object.
func (c *Conn) ReplyOK(to string, result any) error {
	r := &Reply{Header: Header{ReplyTo: to}, OK: true}
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		r.Result = b
	}
	return c.Send(r)
}

// ReplyError answers request id with a stable error code.
func (c *Conn) ReplyError(to, code string) error {
	return c.Send(&Reply{Header: Header{ReplyTo: to}, OK: false, Error: &ReplyError{Code: code}})
}

// ReplyErr converts a failed Reply into an error carrying its code.
type ReplyErr struct{ Code string }

func (e *ReplyErr) Error() string { return "worker reply error: " + e.Code }

// Check returns nil for ok replies, *ReplyErr otherwise, and decodes result into v.
func (r *Reply) Check(v any) error {
	if !r.OK {
		code := ErrInvalid
		if r.Error != nil && r.Error.Code != "" {
			code = r.Error.Code
		}
		return &ReplyErr{Code: code}
	}
	if v != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, v)
	}
	return nil
}
