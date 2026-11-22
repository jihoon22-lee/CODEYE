package instance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// RuntimeToolNames are the executable overrides editable on a managed installation.
var RuntimeToolNames = [...]string{"python", "java", "go", "cargo", "node"}

func (c *Config) RuntimePaths() map[string]string {
	paths := map[string]string{}
	for _, name := range RuntimeToolNames {
		value := c.Agent.Tools[name]
		if name == "go" {
			value = c.Agent.Go
		}
		if value != "" {
			paths[name] = value
		}
	}
	return paths
}

func editRuntimePaths(doc *document, changes map[string]string) error {
	agent := map[string]json.RawMessage{}
	if raw := doc.values["agent"]; raw != nil {
		if err := json.Unmarshal(raw, &agent); err != nil {
			return err
		}
	}
	tools := map[string]string{}
	if raw := agent["tools"]; raw != nil {
		if err := json.Unmarshal(raw, &tools); err != nil {
			return err
		}
	}
	if agent == nil {
		agent = map[string]json.RawMessage{}
	}
	if tools == nil {
		tools = map[string]string{}
	}
	for name, value := range changes {
		if !slices.Contains(RuntimeToolNames[:], name) || value != "" && !absClean(value) {
			return errors.New("invalid runtime path")
		}
		if name == "go" {
			if value == "" {
				delete(agent, "go")
			} else {
				agent["go"], _ = json.Marshal(value)
			}
		} else if value == "" {
			delete(tools, name)
		} else {
			tools[name] = value
		}
	}
	if len(tools) == 0 {
		delete(agent, "tools")
	} else {
		agent["tools"], _ = json.Marshal(tools)
	}
	if len(agent) == 0 {
		doc.unset("agent")
		return nil
	}
	return doc.set("agent", agent)
}

// The worker's isolated HOME and PATH are intentionally different from the
// service user's. Keep the service's automatic choices across manual edits.
const runtimeDefaultsFile = "runtime-defaults.json"

func (l Layout) WriteRuntimeDefaults(paths map[string]string) error {
	data, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	return l.WriteRunFile(runtimeDefaultsFile, data)
}

func (l Layout) RuntimeDefaults() (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(l.RunDir(), runtimeDefaultsFile))
	if err != nil {
		return nil, err
	}
	var paths map[string]string
	if len(b) > 64<<10 || json.Unmarshal(b, &paths) != nil || paths == nil {
		return nil, errors.New("invalid runtime defaults")
	}
	for name, p := range paths {
		if !slices.Contains(RuntimeToolNames[:], name) || !absClean(p) {
			return nil, errors.New("invalid runtime default")
		}
	}
	return paths, nil
}
