package javapack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

var (
	javaVersionLine     = regexp.MustCompile(`(?m)^(?:openjdk|java) ([0-9]+(?:\.[0-9]+){0,3})(?:[ +\r\n]|$)`)
	pmdVersionLine      = regexp.MustCompile(`(?m)^PMD (7\.[0-9]+\.[0-9]+)(?:[ (\r\n]|$)`)
	spotbugsVersionLine = regexp.MustCompile(`(?m)^SpotBugs (4\.[0-9]+\.[0-9]+)(?:[ \r\n]|$)`)
)

func parsed(pattern *regexp.Regexp, stdout []byte) (string, bool) {
	m := pattern.FindSubmatch(stdout)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

func ParseJavaVersion(stdout []byte) (string, bool) { return parsed(javaVersionLine, stdout) }

func Descriptor(name string) pack.Descriptor {
	d := pack.Descriptor{ID: "java." + name, Name: name, Tool: name, Ecosystem: "java", Required: name == PMD, Toolchains: []string{"java"}}
	if name == PMD {
		d.VersionArgs = []string{"--version"}
		d.ParseVersion = func(out []byte) (string, bool) { return parsed(pmdVersionLine, out) }
	} else {
		d.ParseVersion = func(out []byte) (string, bool) { return parsed(spotbugsVersionLine, out) }
	}
	return d
}

type Analyzer struct {
	name, version, jar string
	java               *anypack.Tool
	jdkVersion         string
	ClasspathCatalog   string
}

func New(name, jar string, java pack.Toolchain) (*Analyzer, error) {
	if name != PMD && name != SpotBugs {
		return nil, errors.New("unknown Java analyzer")
	}
	if !filepath.IsAbs(jar) {
		return nil, errors.New("Java analyzer JAR path must be absolute")
	}
	resolved, err := nativepath.Resolve(jar)
	if err != nil {
		return nil, errors.New("Java analyzer JAR path cannot be resolved")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || filepath.Ext(resolved) != ".jar" {
		return nil, errors.New("Java analyzer requires an installed JAR")
	}
	if name == PMD && !strings.HasPrefix(filepath.Base(resolved), "pmd-cli-") {
		return nil, errors.New("PMD requires the CLI JAR in its distribution lib directory")
	}
	if name == SpotBugs && filepath.Base(resolved) != "spotbugs.jar" {
		return nil, errors.New("SpotBugs requires lib/spotbugs.jar from its distribution")
	}
	rt, err := anypack.NewTool(java.Executable, nil)
	if err != nil {
		return nil, err
	}
	return &Analyzer{name: name, java: rt, jar: resolved, jdkVersion: java.Version}, nil
}

func (a *Analyzer) Descriptor() pack.Descriptor { return Descriptor(a.name) }

func (a *Analyzer) commandArgs(probe bool, args ...string) []string {
	base := []string{"-Xmx512m", "-Dfile.encoding=UTF-8", "-Djava.awt.headless=true"}
	if a.name == PMD {
		lib := filepath.Dir(a.jar)
		base = append(base, "-cp", filepath.Join(lib, "*")+string(os.PathListSeparator)+filepath.Join(filepath.Dir(lib), "conf"), "net.sourceforge.pmd.cli.PmdCli")
	} else if probe {
		// SpotBugs' CLI -version can check the network for updates. Read the
		// installed version class directly instead.
		base = append(base, "-cp", a.jar, "edu.umd.cs.findbugs.Version")
	} else {
		base = append(base, "-jar", a.jar, "-textui")
	}
	return append(base, args...)
}

func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	d := a.Descriptor()
	version, err := a.java.Probe(ctx, runner, dir, a.commandArgs(true, d.VersionArgs...), d.ParseVersion)
	if err == nil {
		a.version = version
	}
	return version, err
}

func ToolProbeArguments(name, jar, java string) ([]string, error) {
	a, err := New(name, jar, pack.Toolchain{Executable: java})
	if err != nil {
		return nil, err
	}
	return a.commandArgs(true, a.Descriptor().VersionArgs...), nil
}

func VersionTested(name, version string) bool {
	return name == PMD && strings.HasPrefix(version, "7.29.") || name == SpotBugs && strings.HasPrefix(version, "4.10.")
}

func (a *Analyzer) compiler() (*anypack.Tool, error) {
	name := "javac"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return anypack.NewTool(filepath.Join(filepath.Dir(a.java.Resolved), name), nil)
}
