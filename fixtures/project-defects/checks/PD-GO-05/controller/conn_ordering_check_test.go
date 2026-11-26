package controller

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

// scriptedTransport hands the agent a fixed list of packets, then reports the end of the stream.
type scriptedTransport struct {
	mu      sync.Mutex
	packets []*pb.Packet
}

func (t *scriptedTransport) Recv() (*pb.Packet, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.packets) == 0 {
		return nil, io.EOF
	}
	pkt := t.packets[0]
	t.packets = t.packets[1:]
	return pkt, nil
}

func (t *scriptedTransport) Send(*pb.Packet) error             { return nil }
func (t *scriptedTransport) StreamContext() context.Context    { return context.Background() }
func (t *scriptedTransport) StartKeepAlive()                   {}
func (t *scriptedTransport) Close() (error, error)             { return nil, nil }

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// One round: several write packets of the same client connection arrive back to back. The agent
// must open exactly one upstream TCP connection for them and deliver every payload byte.
func tcpBurstRound(t *testing.T, writes int) (accepted int64, received int64) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	var conns []net.Conn
	var connsMu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&accepted, 1)
			connsMu.Lock()
			conns = append(conns, c)
			connsMu.Unlock()
			go func(c net.Conn) {
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					atomic.AddInt64(&received, int64(n))
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	defer func() {
		connsMu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		connsMu.Unlock()
	}()

	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	sid := "sid-burst"
	transport := &scriptedTransport{}
	for i := 0; i < writes; i++ {
		transport.packets = append(transport.packets, &pb.Packet{
			Type:    pbagent.TCPConnectionWrite,
			Payload: []byte("x"),
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:   []byte(sid),
				pb.SpecClientConnectionID: []byte("1"),
			},
		})
	}
	agent := New(transport, &config.Config{}, nil)
	agent.connStore.Set(sid, &pb.AgentConnectionParams{
		ConnectionType: string(pb.ConnectionTypeTCP),
		EnvVars: map[string]any{
			"envvar:HOST": b64("127.0.0.1"),
			"envvar:PORT": b64(port),
		},
	})
	_ = agent.Run()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt64(&received) < int64(writes) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	return atomic.LoadInt64(&accepted), atomic.LoadInt64(&received)
}

func TestBurstOfWritesUsesOneUpstreamConnection(t *testing.T) {
	const rounds, writes = 30, 6
	for r := 0; r < rounds; r++ {
		accepted, received := tcpBurstRound(t, writes)
		if accepted != 1 || received != writes {
			t.Fatalf("round %d: %d write packets of one client connection opened %d upstream connections "+
				"(want 1) and delivered %d bytes (want %d)", r, writes, accepted, received, writes)
		}
	}
}
