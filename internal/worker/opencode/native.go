package opencode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/jsonc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

const maxNativeConfigBytes = 1 << 20

var errNativeConfig = errors.New("the OpenCode configuration must be a readable JSON or JSONC file of at most 1 MiB")
var errNativeModel = errors.New("the OpenCode configuration needs a valid model (PROVIDER/MODEL), or set an explicit model override")
var errNativeProvider = errors.New("the selected OpenCode provider has unsupported settings; use a bundled provider and literal connection settings, or supply a credential file")
var errNativeFile = errors.New("the OpenCode provider's file reference must point to a readable private key file")

// ErrNativeEndpoint means an offline OpenCode configuration lacks an explicit
// endpoint supported by the managed adapter.
var ErrNativeEndpoint = errors.New("offline mode needs an explicit OpenCode provider endpoint (options.baseURL or credential base_url)")

// ErrNativeKeyMissing means the selected model's public provider needs a key.
var ErrNativeKeyMissing = errors.New("the selected OpenCode provider needs a matching API credential (auth.json or CODEYE credential file)")

// NativeKeyReference is an API key expected from an environment variable in
// the native profile. CODEYE may satisfy it with a managed credential without
// inheriting the environment of the invoking shell.
type NativeKeyReference struct {
	Variable string
	APIKey   bool
}

func (e *NativeKeyReference) Error() string {
	return "the selected OpenCode provider references an environment API key; provide its auth.json or CODEYE credential file"
}

var nativeReference = regexp.MustCompile(`\{(env|file):([^}]+)\}`)

type nativeConfig struct {
	digest    string
	Model     string                     `json:"model"`
	Provider  map[string]json.RawMessage `json:"provider"`
	Providers json.RawMessage            `json:"providers"`
}

type nativeProvider struct {
	Package string                     `json:"npm"`
	API     string                     `json:"api"`
	Options json.RawMessage            `json:"options"`
	Models  map[string]json.RawMessage `json:"models"`
}

type nativeOptions struct {
	BaseURL       string            `json:"baseURL"`
	APIKey        string            `json:"apiKey"`
	Headers       map[string]string `json:"headers"`
	Timeout       json.RawMessage   `json:"timeout"`
	HeaderTimeout json.RawMessage   `json:"headerTimeout"`
	ChunkTimeout  int               `json:"chunkTimeout"`
	IncludeUsage  *bool             `json:"includeUsage"`
}

type nativeModel struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Limit    map[string]int    `json:"limit"`
	Headers  map[string]string `json:"headers"`
	Provider struct {
		Package string `json:"npm"`
		API     string `json:"api"`
	} `json:"provider"`
}

// NativeSelection reads the explicit file, keeping only the chosen provider
// and model. An explicit credential overrides inline authentication and an
// endpoint in the credential takes precedence over the native endpoint.
func NativeSelection(path, override, credential string, offline bool, home string) (string, *executor.ModelProvider, error) {
	return nativeSelection(path, override, credential, offline, home, "")
}

func nativeSelection(path, override, credential string, offline bool, home, expectedDigest string) (string, *executor.ModelProvider, error) {
	cfg, err := readNativeConfig(path)
	if err != nil {
		return "", nil, err
	}
	if expectedDigest != "" && cfg.digest != expectedDigest {
		return "", nil, errNativeConfig
	}
	name, provider, model, err := cfg.selection(override)
	if err != nil {
		return "", nil, err
	}
	p, err := cfg.selectedProvider(provider, model, credential, offline, filepath.Dir(path), home)
	return name, p, err
}

func (cfg nativeConfig) selection(override string) (name, provider, model string, err error) {
	name = cfg.Model
	if override != "" {
		name = override
		if !strings.Contains(name, "/") {
			provider, _, _ := strings.Cut(cfg.Model, "/")
			if !executor.ValidOpenCodeModel(cfg.Model) && len(cfg.Provider) == 1 {
				for key := range cfg.Provider {
					provider = key
				}
			}
			name = provider + "/" + name
		}
	}
	provider, model, _ = strings.Cut(name, "/")
	if !executor.ValidOpenCodeModel(name) || provider == executor.GatewayProvider {
		return "", "", "", errNativeModel
	}
	return name, provider, model, nil
}

// PinNativeConfig binds an attempt to the native config inspected before its
// source was read. It returns private file paths used by the selected
// connection, without reading their bytes.
func PinNativeConfig(path, override, home string) (string, []string) {
	cfg, err := readNativeConfig(path)
	if err != nil {
		return "", nil
	}
	_, id, model, err := cfg.selection(override)
	if err != nil {
		return "", nil
	}
	var provider nativeProvider
	var options nativeOptions
	var selected nativeModel
	if !decodeSelected(cfg.Provider[id], &provider) {
		return "", nil
	}
	_ = decodeSelected(provider.Options, &options)
	_ = decodeSelected(provider.Models[model], &selected)
	values := []string{options.APIKey}
	for _, group := range []map[string]string{options.Headers, selected.Headers} {
		for _, value := range group {
			values = append(values, value)
		}
	}
	var paths []string
	for _, value := range values {
		for _, ref := range nativeReference.FindAllStringSubmatch(value, -1) {
			if ref[1] == "file" {
				if file := nativeFilePath(ref[2], filepath.Dir(path), home); file != "" {
					paths = append(paths, file)
				}
			}
		}
	}
	return cfg.digest, paths
}

func (cfg nativeConfig) selectedProvider(id, model, credential string, offline bool, directory, home string) (*executor.ModelProvider, error) {
	var provider nativeProvider
	var options nativeOptions
	var selected nativeModel
	if !decodeSelected(cfg.Provider[id], &provider) || !decodeNativeOptions(provider.Options, &options) || !decodeSelected(provider.Models[model], &selected) {
		return nil, errNativeProvider
	}
	p := &executor.ModelProvider{ID: id, APIKey: options.APIKey, BaseURL: firstNativeSetting(options.BaseURL, selected.Provider.API, provider.API)}
	packageName := firstNativeSetting(selected.Provider.Package, provider.Package, builtinNativePackage(id))
	if credential != "" {
		key, err := ReadCredential(credential, id)
		if err != nil {
			return nil, ErrCredentialUnavailable
		}
		p.APIKey = key.APIKey
		p.BaseURL = firstNativeSetting(key.BaseURL, p.BaseURL)
		if key.OpenAICompatible {
			packageName = "@ai-sdk/openai-compatible"
		}
	} else if parts := nativeReference.FindStringSubmatch(p.APIKey); len(parts) > 0 && parts[0] == p.APIKey && parts[1] == "env" {
		return nil, &NativeKeyReference{Variable: parts[2], APIKey: true}
	} else {
		var err error
		p.APIKey, err = expandNativeFiles(p.APIKey, directory, home)
		if err != nil {
			return nil, err
		}
	}
	if packageName == "" {
		// OpenCode gives config-defined providers without a package the
		// OpenAI-compatible SDK. Require a real endpoint for that setup.
		if p.BaseURL == "" && id != "opencode" {
			return nil, errNativeProvider
		}
		if id != "opencode" {
			packageName = "@ai-sdk/openai-compatible"
		}
	}
	if (packageName != "" && !bundledNativePackage(packageName)) || (p.APIKey != "" && !executor.ValidProvider(p)) || !literalNativeString(p.APIKey) ||
		(p.BaseURL != "" && (!executor.ValidBaseURL(p.BaseURL) || !literalNativeString(p.BaseURL) || strings.Contains(p.BaseURL, "${"))) ||
		(selected.ID != "" && !executor.ValidOpenCodeModel("p/"+selected.ID)) || !literalNativeString(selected.Name) || len(selected.Name) > 256 {
		return nil, errNativeProvider
	}
	if offline && p.BaseURL == "" {
		return nil, ErrNativeEndpoint
	}
	if p.APIKey == "" && id != "opencode" && p.BaseURL == "" {
		return nil, ErrNativeKeyMissing
	}
	limits := map[string]int{}
	for _, field := range []string{"context", "input", "output"} {
		if size, ok := selected.Limit[field]; ok {
			if size <= 0 || size > 100_000_000 {
				return nil, errNativeProvider
			}
			limits[field] = size
		}
	}
	headers, ok := nativeHeaders(options.Headers, selected.Headers)
	if !ok {
		return nil, errNativeProvider
	}
	for name, value := range headers {
		resolved, err := expandNativeFiles(value, directory, home)
		if err != nil {
			return nil, err
		}
		headers[name] = resolved
	}
	timeouts := map[string]any{}
	for field, raw := range map[string]json.RawMessage{"timeout": options.Timeout, "headerTimeout": options.HeaderTimeout} {
		if len(raw) == 0 {
			continue
		}
		if string(raw) == "false" {
			timeouts[field] = false
			continue
		}
		var ms int
		if json.Unmarshal(raw, &ms) != nil || ms <= 0 {
			return nil, errNativeProvider
		}
		timeouts[field] = ms
	}
	if options.ChunkTimeout > 0 {
		timeouts["chunkTimeout"] = options.ChunkTimeout
	}
	p.Native = &executor.NativeModel{Package: packageName, ID: selected.ID, Name: selected.Name, Limit: limits,
		Headers: headers, Timeouts: timeouts, IncludeUsage: options.IncludeUsage}
	return p, nil
}

func decodeSelected(raw json.RawMessage, value any) bool {
	return len(raw) == 0 || json.Unmarshal(raw, value) == nil
}

func decodeNativeOptions(raw json.RawMessage, options *nativeOptions) bool {
	if len(raw) == 0 {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for key := range fields {
		switch key {
		case "baseURL", "apiKey", "headers", "timeout", "headerTimeout", "chunkTimeout", "includeUsage":
		default:
			return false
		}
	}
	return decodeSelected(raw, options)
}

func expandNativeFiles(value, directory, home string) (string, error) {
	var resolveErr error
	value = nativeReference.ReplaceAllStringFunc(value, func(whole string) string {
		if resolveErr != nil {
			return ""
		}
		parts := nativeReference.FindStringSubmatch(whole)
		if parts[1] != "file" {
			resolveErr = &NativeKeyReference{Variable: parts[2]}
			return ""
		}
		path := nativeFilePath(parts[2], directory, home)
		if path == "" {
			resolveErr = errNativeFile
			return ""
		}
		b, err := readPrivate(path, 8192)
		text := strings.TrimSpace(string(b))
		if err != nil || text == "" {
			resolveErr = errNativeFile
		}
		return text
	})
	if resolveErr == nil && (len(value) > 8192 || !literalNativeString(value)) {
		resolveErr = errNativeProvider
	}
	return value, resolveErr
}

func nativeFilePath(path, directory, home string) string {
	switch {
	case strings.HasPrefix(path, "~/") && filepath.IsAbs(home):
		return filepath.Join(home, path[2:])
	case strings.HasPrefix(path, "~/"):
		return ""
	case !filepath.IsAbs(path):
		return filepath.Join(directory, path)
	default:
		return filepath.Clean(path)
	}
}

func firstNativeSetting(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func literalNativeString(value string) bool {
	return !strings.ContainsAny(value, "\r\n\x00") && !strings.Contains(value, "{env:") && !strings.Contains(value, "{file:")
}

func nativeHeaders(groups ...map[string]string) (map[string]string, bool) {
	headers := map[string]string{}
	for _, group := range groups {
		if len(group) > 64 {
			return nil, false
		}
		seen := map[string]bool{}
		for name, value := range group {
			for _, ch := range name {
				if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", ch)) {
					return nil, false
				}
			}
			name = http.CanonicalHeaderKey(name)
			if name == "" || len(name) > 128 || seen[name] || len(value) > 8192 {
				return nil, false
			}
			seen[name], headers[name] = true, value
		}
	}
	return headers, true
}

func builtinNativePackage(id string) string {
	switch id {
	case "openai", "anthropic", "azure", "google", "xai", "mistral", "groq", "deepinfra", "cerebras", "cohere", "togetherai", "perplexity":
		return "@ai-sdk/" + id
	case "openrouter":
		return "@openrouter/ai-sdk-provider"
	}
	return ""
}

// Bundled API-key providers in the tested OpenCode release. Exclude cloud SDKs
// with independent discovery or credential chains and packages that need npm.
func bundledNativePackage(name string) bool {
	switch name {
	case "@ai-sdk/openai-compatible", "@ai-sdk/openai", "@ai-sdk/anthropic", "@ai-sdk/azure", "@ai-sdk/google", "@openrouter/ai-sdk-provider",
		"@ai-sdk/xai", "@ai-sdk/mistral", "@ai-sdk/groq", "@ai-sdk/deepinfra", "@ai-sdk/cerebras", "@ai-sdk/cohere", "@ai-sdk/togetherai", "@ai-sdk/perplexity":
		return true
	}
	return false
}

func readNativeConfig(path string) (nativeConfig, error) {
	var cfg nativeConfig
	if !filepath.IsAbs(path) {
		return cfg, errNativeConfig
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxNativeConfigBytes {
		return cfg, errNativeConfig
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg, errNativeConfig
	}
	defer f.Close()
	if current, err := f.Stat(); err != nil || !os.SameFile(st, current) {
		return cfg, errNativeConfig
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxNativeConfigBytes+1))
	if err != nil || len(raw) > maxNativeConfigBytes {
		return cfg, errNativeConfig
	}
	clean := strings.TrimSpace(string(jsonc.StripJSONC(raw)))
	if !strings.HasPrefix(clean, "{") || json.Unmarshal([]byte(clean), &cfg) != nil {
		return cfg, errNativeConfig
	}
	if len(cfg.Providers) != 0 {
		return cfg, errors.New("the installed adapter uses OpenCode's provider configuration format")
	}
	digest := sha256.Sum256(raw)
	cfg.digest = hex.EncodeToString(digest[:])
	return cfg, nil
}
