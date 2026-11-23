package websocket

import (
	"bytes"
	"testing"
)

// The client starts the closing handshake, the server's default handler
// answers it, and the client then reads that answer. Both read calls must
// report the peer's close frame as a *CloseError with code 1000.
func TestClientReadsCloseReplyAsCloseError(t *testing.T) {
	var b1, b2 bytes.Buffer

	client := newTestConn(&b2, &b1, false)
	server := newTestConn(&b1, &b2, true)

	msg := FormatCloseMessage(CloseNormalClosure, "")
	if err := client.WriteMessage(CloseMessage, msg); err != nil {
		t.Fatalf("unexpected error when writing close message, %v", err)
	}

	if _, _, err := server.NextReader(); !IsCloseError(err, CloseNormalClosure) {
		t.Fatalf("server expects a close message, %v returned", err)
	}

	if _, _, err := client.NextReader(); !IsCloseError(err, CloseNormalClosure) {
		t.Fatalf("client expects a close message, %v returned", err)
	}
}
