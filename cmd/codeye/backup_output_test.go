package main

import (
	"errors"
	"io"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/backup"
)

func TestBackupReceiptReturnsOutputErrors(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			reader, writer := io.Pipe()
			reader.Close()
			defer writer.Close()
			if err := writeBackupReceipt(writer, format, "backup", backup.Manifest{}); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("receipt returned %v", err)
			}
		})
	}
}
