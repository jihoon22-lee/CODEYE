package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProbeHelper(t *testing.T) {
	mode := os.Getenv("CODEYE_PROBE_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "env":
		if os.Getenv("CODEYE_SENTINEL_SECRET") != "" {
			fmt.Print("leaked")
			os.Exit(3)
		}
		cwd, _ := os.Getwd()
		if cwd != os.Getenv("HOME") {
			os.Exit(4)
		}
		fmt.Print("version 1.2.3")
	case "flood":
		fmt.Print(strings.Repeat("x", 20000))
	case "wait":
		time.Sleep(10 * time.Second)
	case "fail":
		fmt.Print("secret")
		os.Exit(7)
	}
	os.Exit(0)
}
func TestNativeRunner(t *testing.T) {
	t.Setenv("CODEYE_SENTINEL_SECRET", "must-not-inherit")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode, reason string
		timeout      time.Duration
	}{
		{"env", "", time.Second}, {"flood", "output_limit", time.Second}, {"fail", "probe_failed", time.Second}, {"wait", "recovery_required", 50 * time.Millisecond},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			start := time.Now()
			r := (NativeRunner{}).Run(context.Background(), Command{Name: binary, Args: []string{"-test.run=^TestProbeHelper$"}, Env: []string{"CODEYE_PROBE_HELPER=" + tc.mode, "GORACE=atexit_sleep_ms=0"}, Timeout: tc.timeout})
			if r.Reason != tc.reason {
				t.Fatalf("got %q, want %q", r.Reason, tc.reason)
			}
			if len(r.Output) > 8192 || time.Since(start) > 3*time.Second {
				t.Fatal("unbounded probe")
			}
			if tc.mode == "env" && r.Output != "version 1.2.3" {
				t.Fatal("environment or cwd not isolated")
			}
		})
	}
	r := (NativeRunner{}).Run(context.Background(), Command{Name: "codeye-definitely-missing-binary", Timeout: time.Second})
	if r.Reason != "executable_missing" {
		t.Fatalf("%+v", r)
	}
}
