package workerproto

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// pair returns two connected Conns (a ↔ b).
func pair(t *testing.T) (*Conn, *Conn) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	a, b := NewConn(ar, aw), NewConn(br, bw)
	a.Start()
	b.Start()
	t.Cleanup(func() { aw.Close(); bw.Close() })
	return a, b
}

func TestRequestReplyAndIncoming(t *testing.T) {
	a, b := pair(t)
	go func() {
		for m := range b.Incoming() {
			if in, ok := m.(*Intent); ok {
				_ = b.ReplyOK(in.ID, map[string]string{"recorded": in.Kind})
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := a.Request(ctx, &Intent{AttemptID: "x", Kind: IntentSourceAcquireStarted})
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]string
	if err := r.Check(&res); err != nil || res["recorded"] != IntentSourceAcquireStarted {
		t.Fatalf("reply %+v %v", res, err)
	}
}

func TestReplyErrorCode(t *testing.T) {
	a, b := pair(t)
	go func() {
		for m := range b.Incoming() {
			if ev, ok := m.(*Event); ok {
				_ = b.ReplyError(ev.ID, ErrStaleAttempt)
			}
		}
	}()
	r, err := a.Request(context.Background(), &Event{Kind: "task_created", Payload: []byte(`{"key":"k","required":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	var re *ReplyErr
	if err := r.Check(nil); !errors.As(err, &re) || re.Code != ErrStaleAttempt {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownTypeIsProtocolError(t *testing.T) {
	c := NewConn(strings.NewReader(`{"type":"exec_shell","id":"1","cmd":"rm -rf /"}`+"\n"), io.Discard)
	c.Start()
	<-c.Done()
	if !errors.Is(c.Err(), ErrProtocol) {
		t.Fatalf("err = %v", c.Err())
	}
}

func TestUnknownFieldIsProtocolError(t *testing.T) {
	c := NewConn(strings.NewReader(`{"type":"idle","id":"1","extra":true}`+"\n"), io.Discard)
	c.Start()
	<-c.Done()
	if !errors.Is(c.Err(), ErrProtocol) {
		t.Fatalf("err = %v", c.Err())
	}
}

func TestOversizedLineIsProtocolError(t *testing.T) {
	big := `{"type":"log","id":"1","level":"info","code":"x","message":"` + strings.Repeat("a", MaxLine) + `"}` + "\n"
	c := NewConn(strings.NewReader(big), io.Discard)
	c.Start()
	<-c.Done()
	if !errors.Is(c.Err(), ErrProtocol) {
		t.Fatalf("err = %v", c.Err())
	}
}

func TestRequestFailsWhenPeerCloses(t *testing.T) {
	r, w := io.Pipe()
	c := NewConn(r, io.Discard)
	c.Start()
	go func() { time.Sleep(50 * time.Millisecond); w.Close() }()
	if _, err := c.Request(context.Background(), &Idle{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Request(context.Background(), &Idle{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close err = %v", err)
	}
}

func TestTranscriptRecordsBothDirections(t *testing.T) {
	var tr bytes.Buffer
	in := strings.NewReader(`{"type":"idle","id":"a"}` + "\n")
	c := NewConn(in, io.Discard)
	c.Transcript = &tr
	c.Start()
	_ = c.Send(&Log{Level: "info", Code: "x", Message: "m"})
	<-c.Done()
	if !strings.Contains(tr.String(), "< {\"type\":\"idle\"") || !strings.Contains(tr.String(), "> {\"type\":\"log\"") {
		t.Fatalf("transcript %q", tr.String())
	}
}
