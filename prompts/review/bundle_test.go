package reviewpack

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// diskDigest recomputes a selection digest from the files on disk, apart
// from For: sha256 over path NUL content NUL in ascending path order.
func diskDigest(t *testing.T, names []string) string {
	t.Helper()
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	h := sha256.New()
	for _, name := range sorted {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func guidesOf(files []string) []string {
	var out []string
	for _, f := range files {
		if strings.HasPrefix(f, "languages/") {
			out = append(out, f)
		}
	}
	return out
}

func TestDefaultPackSelection(t *testing.T) {
	id, version, err := Identity()
	if err != nil || id != "review" || version != "2.9.0" {
		t.Fatal(id, version, err)
	}
	pack, err := os.ReadFile("pack.yaml")
	if err != nil || !bytes.Contains(pack, []byte("status: default")) {
		t.Fatal("pack.yaml must mark the pack as the default")
	}
	for _, ecosystems := range [][]string{nil, {"go"}, Languages()} {
		digests := map[string]bool{}
		for _, role := range Roles {
			s, err := For(role, ecosystems)
			if err != nil {
				t.Fatal(role, err)
			}
			if len(s.Digest) != 64 || digests[s.Digest] || !slices.IsSorted(s.Files) || !slices.Contains(s.Files, roleFiles[role]) || !slices.Contains(s.Files, "pack.yaml") {
				t.Fatalf("%s: %+v", role, s)
			}
			digests[s.Digest] = true
			again, _ := For(role, slices.Clone(ecosystems))
			if again.Digest != s.Digest || again.Text != s.Text {
				t.Fatal("selection is not deterministic")
			}
		}
	}
	if _, err := For("context", []string{"go"}); err == nil {
		t.Fatal("unknown role accepted")
	}
}

// pack.yaml lists the same guide table For uses: one guide per source
// ecosystem of the analyzer packs (envdoc/obsdoc names), each a short LF
// Korean guide like languages/go.md.
func TestLanguageGuidesListed(t *testing.T) {
	b, err := os.ReadFile("pack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	in := false
	for s := bufio.NewScanner(bytes.NewReader(b)); s.Scan(); {
		line := s.Text()
		switch {
		case line == "languages:":
			in = true
		case in && strings.HasPrefix(line, "  "):
			k, v, _ := strings.Cut(strings.TrimSpace(line), ": ")
			listed[k] = v
		default:
			in = false
		}
	}
	if len(listed) != len(languageFiles) {
		t.Fatalf("pack.yaml languages %v", listed)
	}
	for eco, file := range languageFiles {
		if listed[eco] != file || file != "languages/"+eco+".md" {
			t.Fatalf("%s: pack.yaml %q, table %q", eco, listed[eco], file)
		}
		text, err := files.ReadFile(file)
		lines := bytes.Count(text, []byte("\n"))
		if err != nil || !bytes.HasPrefix(text, []byte("# ")) || !bytes.HasSuffix(text, []byte(".\n")) || bytes.ContainsRune(text, '\r') || lines < 18 || lines > 26 {
			t.Fatalf("%s: %d lines, %v", file, lines, err)
		}
	}
	if !slices.Equal(Languages(), []string{"cpp", "go", "java", "javascript", "python", "rust"}) {
		t.Fatal(Languages())
	}
}

// For selects exactly the guides of the given in-scope ecosystems, in
// ascending order after the core, role and domain files; an ecosystem
// without a guide adds nothing and an empty set selects none. The digest
// covers exactly the selected files.
func TestLanguageSelection(t *testing.T) {
	all := []string{"languages/cpp.md", "languages/go.md", "languages/java.md", "languages/javascript.md", "languages/python.md", "languages/rust.md"}
	cases := []struct {
		in, want []string
	}{
		{nil, nil},
		{[]string{}, nil},
		{[]string{"go"}, []string{"languages/go.md"}},
		{[]string{"rust", "python", "go", "python"}, []string{"languages/go.md", "languages/python.md", "languages/rust.md"}},
		{[]string{"javascript", "cpp"}, []string{"languages/cpp.md", "languages/javascript.md"}},
		{[]string{"any", "ruby", ""}, nil},
		{[]string{"java"}, []string{"languages/java.md"}},
		{Languages(), all},
	}
	for _, role := range Roles {
		for _, c := range cases {
			s, err := For(role, c.in)
			if err != nil {
				t.Fatal(role, c.in, err)
			}
			if got := guidesOf(s.Files); !slices.Equal(got, c.want) {
				t.Fatalf("%s %v: guides %v, want %v", role, c.in, got, c.want)
			}
			ordered := append([]string{"00-core.md", roleFiles[role], "domains.md"}, c.want...)
			want := append([]string{"pack.yaml"}, ordered...)
			slices.Sort(want)
			if !slices.Equal(s.Files, want) || s.Digest != diskDigest(t, want) {
				t.Fatalf("%s %v: files %v digest %s", role, c.in, s.Files, s.Digest)
			}
			var parts []string
			for _, name := range ordered {
				b, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				parts = append(parts, string(b))
			}
			if s.Text != strings.Join(parts, "\n\n") {
				t.Fatalf("%s %v: text order", role, c.in)
			}
			for _, guide := range all {
				head, _, _ := strings.Cut(string(mustEmbedded(t, guide)), "\n")
				if strings.Contains(s.Text, head+"\n") != slices.Contains(c.want, guide) {
					t.Fatalf("%s %v: text and files disagree on %s", role, c.in, guide)
				}
			}
		}
	}
}

func mustEmbedded(t *testing.T, name string) []byte {
	t.Helper()
	b, err := files.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The Go-only selection (the modules fixture snapshot of examples/review)
// is pinned with scripts/validate_bundle.py and the example records:
// changing an instruction byte changes them together.
func TestGoSelectionDigestsPinned(t *testing.T) {
	want := map[string]string{
		"environment": "03a350ea61a7c729d3807309aa3a93f9ea1ef82e659d2b483a1760a4c7082504",
		"documents":   "832cd692d06a2199e1b98ad9cee2f43ce80b7adbd386996afc3214249526e82b",
		"investigate": "2b1a937d187ca0fbd10602a765caa5b30a4d26e74aa2b33f06b4272ca766a7c1",
		"interpret":   "60f4c1deaad67d5c054b49a713a973860f03219067c226409d419723609e6467",
		"triage":      "ead8df9ed695a66132a9beb004a55718804cc0da546db120672171e4c67aa4dc",
		"verify":      "3062b9e6e6e18764caa5013497cecafcb135d9cde85244637e7dc2bf295d804c",
		"contract":    "ebaee71a115f4cd28420bb5ef49e0ccb291808bdcbb4da30f5b57a48905ced2d",
	}
	for _, role := range Roles {
		s, err := For(role, []string{"go"})
		if err != nil || s.Digest != want[role] {
			t.Errorf("%s: digest %s, pinned %s (%v)", role, s.Digest, want[role], err)
		}
	}
}

// No instruction file refers to an evaluation or test case: regression case
// IDs, build-stage or acceptance IDs, fixture paths, or names from the review
// fixtures. An earlier investigate example carried a regression answer
// (docs/EVALUATION.md#회귀-집합-실행).
func TestNoCaseSpecificText(t *testing.T) {
	banned := regexp.MustCompile(`\bQ\d{2}\b|\bR0\b|\bW\d{2}\b|\bTC-\d+|(?i:fixture|corpus|holdout|svc/|example\.test|caller-closes|gateway)|\bCharge\b|\bForward\b|\bNormalize\b`)
	n := 0
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		if m := banned.Find(b); m != nil {
			t.Errorf("%s refers to a specific case: %q", p, m)
		}
		return nil
	})
	if err != nil || n < 12 {
		t.Fatal(n, err)
	}
}
