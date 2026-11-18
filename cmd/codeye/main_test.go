package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLIRejectsUnsupportedAndSensitiveOptions(t *testing.T) {
	for _, args := range [][]string{{"start", "--config=secret"}, {"stop", "--config", "/secret/none.json"}, {"status", "--format=secret"}, {"start", "secret"},
		{"backup", "--config=secret"}, {"restore", "--config=secret"}, {"secret"}, {"doctor", "--url=secret"}, {"doctor", "--format=secret"}, {"doctor", "--timeout=0"}, {"doctor", "extra"}, {"doctor", "--config", "/secret/none.json"}} {
		var out, stderr bytes.Buffer
		if run(context.Background(), args, &out, &stderr) != 2 {
			t.Fatalf("accepted %v", args)
		}
		if strings.Contains(out.String()+stderr.String(), "secret") {
			t.Fatal("leaked rejected input")
		}
	}
}
func TestHelp(t *testing.T) {
	var b bytes.Buffer
	if run(context.Background(), nil, &b, &b) != 0 || !strings.Contains(b.String(), "codeye start [--config PATH]") || strings.Contains(b.String(), "not implemented") {
		t.Fatal(b.String())
	}
}

func TestMigrateRequiresExplicitSafeConfiguration(t *testing.T) {
	t.Setenv("CODEYE_DATABASE_URL", "secret-invalid-url")
	var out, stderr bytes.Buffer
	if run(context.Background(), []string{"migrate"}, &out, &stderr) != 1 {
		t.Fatal("missing migration failure")
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatal("credential leaked")
	}
	stderr.Reset()
	if run(context.Background(), []string{"migrate", "--dsn=secret"}, &out, &stderr) != 2 {
		t.Fatal("unexpected arguments accepted")
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatal("credential leaked")
	}
}
