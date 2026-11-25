package main

import (
	"fmt"
	"io"
	"strings"
)

// usageEntry is one command line of the help and what it does. command is
// the command words ("up", "user create") that `codeye COMMAND --help`
// matches; about is one plain line (empty for the expert commands whose
// line says enough).
type usageEntry struct {
	command, line, about string
}

type usageGroup struct {
	title   string
	entries []usageEntry
}

var usageGroups = []usageGroup{
	{"Everyday use: the web service on this machine", []usageEntry{
		{"up", "codeye up [PATH|URL] [--config PATH] [--postgres-bin-dir DIR] [--offline] [--no-open] [--no-probe]",
			"Start the service (PostgreSQL, controller, local agent; Linux), analyze PATH or a repository URL, open the browser signed in."},
		{"up", "codeye up --listen IP:PORT (--plain-http | --tls-cert CERT.pem --tls-key KEY.pem --public-origin https://HOST[:PORT]) [--public-origin URL ...]",
			"Open the service to other machines. It answers on every address of this machine; --public-origin adds others (repeatable). Saved in the configuration."},
		{"up", "codeye up https://github.com/OWNER/REPO/pull/N [--comment[=false]] [--dry-run-comment] [--comment-wait DURATION]",
			"Analyze a pull request; --comment posts one result comment and is remembered, --dry-run-comment prints it without posting."},
		{"open", "codeye open [--config PATH] [--no-open]", "Print and open a new signed-in browser link."},
		{"status", "codeye status [--config PATH] [--format text|json]", "Show the processes, the database, the node and its running work."},
		{"stop", "codeye stop [--config PATH] [--format text|json]", "Stop the service: running work gets stop.drain_timeout_seconds to finish, then is cancelled."},
		{"start", "codeye start [--config PATH]", "Start the service without a project or a browser (the user service runs this)."},
		{"tools", "codeye tools [--config PATH] [--json | --checklist]", "List the analysis tools found here, what is missing and how to add it (--checklist: offline preparation)."},
		{"doctor", "codeye doctor [--format text|json] [--work-dir .codeye/work] [--timeout 5s] [--config PATH]", "Check this machine: work disk, database, tools, the service's configuration."},
		{"version", "codeye version [--json]", "Print the version."},
	}},
	{"Operations", []usageEntry{
		{"backup", "codeye backup [--config PATH] [--output DIR] [--format text|json]", "Back up the database and artifacts (default output: <backup_dir>/codeye-<UTC>)."},
		{"restore", "codeye restore [--config PATH] --from DIR [--format text|json]", "Restore a backup into a new empty database and state_dir."},
		{"cleanup", "codeye cleanup [--config PATH | --artifact-dir DIR] [--agent-state-dir DIR] [--older-than 30d] [--apply] [--format text|json]", "Remove old raw evidence and work copies (a dry run unless --apply)."},
		{"service", "codeye service install|uninstall|print [--config PATH] [--name codeye]", "A systemd user unit running codeye start/stop (Linux)."},
		{"run recover", "codeye run recover --id RUN_ID --confirm-cleanup [--config PATH]", "Finish a recovery_required run after you checked that none of its processes is left on the node."},
		{"model probe", "codeye model probe [--config PATH] [--timeout 600s] [--json]", "Ask the configured review model one small question."},
		{"model probe", "codeye model probe --executable PATH --model PROVIDER/MODEL [--credential-file PATH | --relay-file PATH] [--timeout 600s] [--json]", "The same for a given OpenCode and model."},
	}},
	{"Administration of a separately run controller and nodes (CODEYE_DATABASE_URL)", []usageEntry{
		{"migrate", "codeye migrate", ""},
		{"user create", "codeye user create [--name NAME] [--username NAME [--password-file PATH]] [--admin]", ""},
		{"user passwd", "codeye user passwd (--id USER_ID | --username NAME) [--password-file PATH] [--must-change]", ""},
		{"user list", "codeye user list", ""},
		{"user disable", "codeye user disable (--id USER_ID | --username NAME)", ""},
		{"user admin", "codeye user admin (--id USER_ID | --username NAME) (--set | --unset)", ""},
		{"source-root add", "codeye source-root add --label LABEL --path ABSOLUTE_DIR", ""},
		{"source-root grant", "codeye source-root grant --root ROOT_ID --user USER_ID", ""},
		{"provider add", "codeye provider add --kind github|ghes --host HOST [--api-url https://HOST/api/v3]", ""},
		{"login-link", "codeye login-link --user USER_ID [--output .codeye/login-link] [--origin http://127.0.0.1:7331 | https://HOST[:PORT]]", ""},
		{"serve", "codeye serve [--listen 127.0.0.1:7331] [--web-dir web/dist] [--artifact-dir .codeye/artifacts] [--reconcile-interval 5s] [--user USER_ID [--login-link-file .codeye/login-link]] [--session-ttl 12h (at most 168h)] [--state-dir DIR] [--local-listen 127.0.0.1:PORT]\n" +
			"             [--tls-cert CERT.pem --tls-key KEY.pem --public-origin https://HOST[:PORT] ... | --plain-http [--public-origin http://HOST:PORT ...]]",
			"The controller API, node protocol, pool autoscaler and schedules. A non-loopback --listen needs TLS or --plain-http; it answers on this machine's own names and addresses and on each --public-origin."},
		{"node token create", "codeye node token create --pool NAME [--source-root ID[,ID...]] [--output .codeye/enroll-token] [--ttl 10m]", ""},
		{"node token revoke", "codeye node token revoke --id TOKEN_ID", ""},
		{"node list", "codeye node list", ""},
		{"node revoke", "codeye node revoke --id NODE_ID", ""},
		{"scheduler set", "codeye scheduler set [--global-max-running N] [--project-max-running N] [--artifact-quota-bytes N]", ""},
		{"pool set", "codeye pool set --name NAME [--min-workers N] [--max-workers N] [--idle-timeout-seconds S]\n" +
			"                  [--scale-up-cooldown-seconds S] [--scale-down-cooldown-seconds S] [--expect-revision R]", ""},
		{"pool list", "codeye pool list", ""},
		{"agent", "codeye agent --controller URL --state-dir DIR [--enroll-token-file PATH] [--name NAME] [--source-root ID=/abs/path ...]\n" +
			"             [--workers N | --autoscale [--max-workers N]] [--toolchain-path PATH] [--git /abs/git --git-host HOST ... [--git-credential-file PATH]] [--controller-sources] [--go /abs/go] [--go-mod-cache DIR] [--node /abs/node] [--staticcheck /abs/staticcheck] [--errcheck /abs/errcheck]\n" +
			"             [--cppcheck /abs/cppcheck] [--clang-tidy /abs/clang-tidy] [--gcc /abs/gcc]\n" +
			"             [--ruff /abs/ruff] [--mypy /abs/mypy] [--pyright /abs/pyright/index.js] [--python-both-type-checkers] [--bandit /abs/bandit] [--import-linter /abs/lint-imports] [--advertise-without-baseline]\n" +
			"             [--eslint /abs/eslint/bin/eslint.js] [--tsc /abs/typescript/bin/tsc] [--dependency-cruiser /abs/dependency-cruiser.mjs]\n" +
			"             [--cargo /abs/toolchain/bin/cargo [--clippy /abs/toolchain/bin/cargo-clippy]]\n" +
			"             [--java /abs/jdk/bin/java] [--pmd /abs/pmd/lib/pmd-cli.jar] [--spotbugs /abs/spotbugs/lib/spotbugs.jar]\n" +
			"             [--java-classpath-catalog /abs/java-classpath.json]\n" +
			"             [--test-go-run REGEXP] [--test-go-race] [--test-maven-repository DIR] [--test-gradle-home DIR]\n" +
			"             [--lizard /abs/lizard] [--jscpd /abs/jscpd/run-jscpd.js] [--gitleaks /abs/gitleaks] [--opengrep /abs/opengrep]\n" +
			"             [--repo-code-source-root ID ...] [--project-analysis-policy] [--storage-quota AREA=BYTES ...] [--storage-log-dir DIR]\n" +
			"             [--opencode /abs/opencode --model PROVIDER/MODEL [--model-config-file PATH] [--model-credential-file PATH | --model-relay-file PATH]] [--model-source-root ID ...] [--review-max-calls 15..2013]\n" +
			"             [--review-parallel 1..16] [--review-request-bytes 40000..1048576] [--review-output-tokens 1536..65536]\n" +
			"             [--offline] [--ca-bundle /abs/ca-bundle.pem]",
			"A node agent. --offline: no built-in public registry or model endpoint; --ca-bundle: the CAs the fetch, Git and model processes trust."},
	}},
}

const usageNotes = `Notes:
  The configuration defaults to $XDG_CONFIG_HOME/codeye/config.json (codeye up creates it). database.mode managed (the default) runs PostgreSQL in the state directory; external uses CODEYE_DATABASE_URL.
  Only "migrate" changes the schema, except that up/start with a managed database migrate after saving a pg_dump copy.
  Sign-in links: codeye up and codeye open print them and open the browser; codeye start and codeye login-link write them to a 0600 file. Enrollment tokens go to a 0600 file and are never printed.
  Passwords are read from --password-file (mode 0600) or standard input (echo off on a Linux terminal), never from arguments.
  Runs stay queued (waiting_reason) until an enrolled node advertises a matching analysis capability.
`

// writeUsage prints the whole help: the everyday commands first.
func writeUsage(out io.Writer) {
	fmt.Fprintln(out, "CODEYE: documents, environment, static analysis and code review of a project, in one web service")
	fmt.Fprintln(out, "Help for one command: codeye COMMAND --help")
	for _, g := range usageGroups {
		fmt.Fprintf(out, "\n%s:\n", g.title)
		for _, e := range g.entries {
			writeEntry(out, e)
		}
	}
	fmt.Fprintln(out)
	fmt.Fprint(out, usageNotes)
}

func writeEntry(out io.Writer, e usageEntry) {
	fmt.Fprintf(out, "  %s\n", e.line)
	if e.about != "" {
		fmt.Fprintf(out, "      %s\n", e.about)
	}
}

// helpRequested reports a -h, -help or --help among a command's arguments
// (before a "--" that ends the options).
func helpRequested(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// commandUsage prints the entries of the command named by args (the longest
// run of leading words some entry starts with: "user create", "user",
// "up"); false when no entry matches.
func commandUsage(out io.Writer, args []string) bool {
	var words []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		words = append(words, a)
	}
	for n := len(words); n > 0; n-- {
		name := strings.Join(words[:n], " ")
		var found []usageEntry
		for _, g := range usageGroups {
			for _, e := range g.entries {
				if e.command == name || strings.HasPrefix(e.command, name+" ") {
					found = append(found, e)
				}
			}
		}
		if len(found) > 0 {
			fmt.Fprintln(out, "Usage:")
			for _, e := range found {
				writeEntry(out, e)
			}
			return true
		}
	}
	return false
}
