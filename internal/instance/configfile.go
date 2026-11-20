package instance

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// The configuration file is the operator's document: `codeye up` creates it
// when it is missing and changes only the fields it owns (a new source root,
// database.postgres_bin_dir), keeping the other fields and their order. It
// never writes defaults into the file.

// EnsureFile writes a configuration holding only schema_version when path
// does not exist (directory 0700, file 0600). It reports whether it wrote one.
func EnsureFile(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, errors.New("cannot read the configuration file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, errors.New("cannot create the configuration directory")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, errors.New("cannot create the configuration file")
	}
	_, werr := f.WriteString("{\n  \"schema_version\": \"" + SchemaVersion + "\"\n}\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		return false, errors.New("cannot write the configuration file")
	}
	return true, nil
}

// document is a JSON object with its key order.
type document struct {
	keys   []string
	values map[string]json.RawMessage
}

func parseDocument(raw []byte) (*document, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if d, ok := tok.(json.Delim); err != nil || !ok || d != '{' {
		return nil, errors.New("config is not a JSON object")
	}
	doc := &document{values: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			return nil, errors.New("config is not a JSON object")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, errors.New("config is not valid JSON")
		}
		if _, dup := doc.values[key]; !dup {
			doc.keys = append(doc.keys, key)
		}
		doc.values[key] = v
	}
	return doc, nil
}

func (d *document) set(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, ok := d.values[key]; !ok {
		d.keys = append(d.keys, key)
	}
	d.values[key] = raw
	return nil
}

func (d *document) unset(key string) {
	if _, ok := d.values[key]; !ok {
		return
	}
	delete(d.values, key)
	for i, k := range d.keys {
		if k == key {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
}

func (d *document) bytes() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range d.keys {
		name, _ := json.Marshal(k)
		var v bytes.Buffer
		if err := json.Indent(&v, d.values[k], "  ", "  "); err != nil {
			return nil, err
		}
		b.WriteString("  ")
		b.Write(name)
		b.WriteString(": ")
		b.Write(v.Bytes())
		if i < len(d.keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

// ConfigEdit is one change `codeye up` makes to the configuration file.
type ConfigEdit struct {
	// AddRoot appends a source root.
	AddRoot *SourceRoot
	// PostgresBinDir sets database.postgres_bin_dir.
	PostgresBinDir string
	// Offline sets "offline": true (codeye up --offline).
	Offline bool
	// Serve replaces how the service listens (codeye up --listen ...):
	// listen, tls_cert, tls_key, public_origin and plain_http; empty values
	// remove their key.
	Serve *ServeEdit
}

// ServeEdit is the listening part of the configuration.
type ServeEdit struct {
	Listen, TLSCert, TLSKey, PublicOrigin string
	PlainHTTP                             bool
}

// EditFile applies e to the file at path, validates the result like Load and
// replaces the file atomically (0600). The returned configuration is the
// validated new one.
func EditFile(path string, knownTools []string, e ConfigEdit) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read the configuration file")
	}
	doc, err := parseDocument(raw)
	if err != nil {
		return nil, err
	}
	if e.PostgresBinDir != "" {
		db := map[string]json.RawMessage{}
		if v, ok := doc.values["database"]; ok {
			if err := json.Unmarshal(v, &db); err != nil {
				return nil, errors.New("config is not valid: database must be an object")
			}
		}
		dir, _ := json.Marshal(e.PostgresBinDir)
		db["postgres_bin_dir"] = dir
		if err := doc.set("database", db); err != nil {
			return nil, err
		}
	}
	if e.Offline {
		if err := doc.set("offline", true); err != nil {
			return nil, err
		}
	}
	if s := e.Serve; s != nil {
		for key, v := range map[string]string{"listen": s.Listen, "tls_cert": s.TLSCert, "tls_key": s.TLSKey, "public_origin": s.PublicOrigin} {
			if v == "" {
				doc.unset(key)
			} else if err := doc.set(key, v); err != nil {
				return nil, err
			}
		}
		if s.PlainHTTP {
			if err := doc.set("plain_http", true); err != nil {
				return nil, err
			}
		} else {
			doc.unset("plain_http")
		}
	}
	if e.AddRoot != nil {
		var roots []json.RawMessage
		if v, ok := doc.values["source_roots"]; ok && string(v) != "null" {
			if err := json.Unmarshal(v, &roots); err != nil {
				return nil, errors.New("config is not valid: source_roots must be an array")
			}
		}
		r, err := json.Marshal(struct {
			Label string `json:"label"`
			Path  string `json:"path"`
		}{e.AddRoot.Label, e.AddRoot.Path})
		if err != nil {
			return nil, err
		}
		if err := doc.set("source_roots", append(roots, r)); err != nil {
			return nil, err
		}
	}
	out, err := doc.bytes()
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(out, knownTools)
	if err != nil {
		return nil, err
	}
	// A symlinked configuration (dotfiles) is written through to its target.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	if err := writePrivate(path, out); err != nil {
		return nil, errors.New("cannot write the configuration file")
	}
	return cfg, nil
}
