package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCommandHelpExitsZero(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"up", "--help"}, []string{"codeye up [PATH|URL]", "codeye up --listen IP:PORT", "codeye up https://github.com/OWNER/REPO/pull/N"}},
		{[]string{"up", "-h"}, []string{"codeye up [PATH|URL]"}},
		// Help wins over the other arguments and an unknown flag.
		{[]string{"up", "/some/path", "--frob", "--help"}, []string{"codeye up [PATH|URL]"}},
		{[]string{"start", "-h"}, []string{"codeye start [--config PATH]"}},
		{[]string{"stop", "--help"}, []string{"codeye stop [--config PATH]", "stop.drain_timeout_seconds"}},
		{[]string{"status", "--help"}, []string{"codeye status [--config PATH]"}},
		{[]string{"open", "-h"}, []string{"codeye open [--config PATH]"}},
		{[]string{"tools", "--help"}, []string{"codeye tools [--config PATH]"}},
		{[]string{"doctor", "-help"}, []string{"codeye doctor [--format text|json]"}},
		{[]string{"user", "--help"}, []string{"codeye user create", "codeye user passwd", "codeye user admin"}},
		{[]string{"user", "create", "--help"}, []string{"codeye user create"}},
		{[]string{"run", "recover", "-h"}, []string{"codeye run recover --id RUN_ID"}},
		{[]string{"model", "probe", "--help"}, []string{"codeye model probe [--config PATH]", "codeye model probe --executable PATH"}},
		{[]string{"agent", "--help"}, []string{"codeye agent --controller URL", "--ca-bundle"}},
	}
	for _, c := range cases {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), c.args, &out, &stderr); code != 0 {
			t.Errorf("%v: exit %d, stderr %q", c.args, code, stderr.String())
			continue
		}
		text := out.String()
		if !strings.HasPrefix(text, "Usage:\n") {
			t.Errorf("%v: no Usage heading:\n%s", c.args, text)
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%v: missing %q:\n%s", c.args, w, text)
			}
		}
		if strings.Contains(text, "Operations:") {
			t.Errorf("%v: printed the whole help instead of the command's:\n%s", c.args, text)
		}
	}
	// "user create --help" shows only that command, not the other user commands.
	var out bytes.Buffer
	run(context.Background(), []string{"user", "create", "--help"}, &out, &bytes.Buffer{})
	if strings.Contains(out.String(), "codeye user passwd") {
		t.Errorf("user create --help shows other commands:\n%s", out.String())
	}
}

func TestHelpPutsEverydayCommandsFirst(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"-h"}, {"help"}, {"frob", "--help"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, stderr.String())
		}
		text := out.String()
		order := []string{"Everyday use", "codeye up [PATH|URL]", "codeye up --listen", "codeye open", "codeye status", "codeye stop",
			"codeye tools", "codeye doctor", "Operations:", "codeye backup", "codeye run recover", "codeye model probe",
			"Administration of a separately run controller", "codeye migrate", "codeye serve", "codeye agent", "Notes:"}
		at := -1
		for _, w := range order {
			i := strings.Index(text, w)
			if i < 0 {
				t.Fatalf("%v: missing %q:\n%s", args, w, text)
			}
			if i < at {
				t.Errorf("%v: %q comes before the entry listed ahead of it", args, w)
			}
			at = i
		}
		if !strings.Contains(text, "codeye COMMAND --help") {
			t.Errorf("%v: does not say how to get one command's help", args)
		}
	}
}

// Every command line of the help belongs to its entry's command, so
// "codeye COMMAND --help" finds it.
func TestUsageEntriesMatchTheirLines(t *testing.T) {
	for _, g := range usageGroups {
		for _, e := range g.entries {
			if !strings.HasPrefix(e.line, "codeye "+e.command) {
				t.Errorf("entry %q has line %q", e.command, e.line)
			}
		}
	}
}
