package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SecretRef represents a secret value that may be provided inline (legacy string),
// or referenced from an external provider (env/file).
//
// JSON forms:
//   - String: "sk-..." (inline secret; discouraged but supported for backward compat)
//   - Object: {"env":"OPENAI_API_KEY"} or {"file":"/run/secrets/openai_api_key"}
//
// NOTE: SecretRef is intended to keep plaintext secrets out of config files while
// still allowing the runtime to materialize secrets into an in-memory snapshot.
type SecretRef struct {
	Inline string `json:"-"`
	Env    string `json:"env,omitempty"`
	File   string `json:"file,omitempty"`
}

type secretRefJSON struct {
	Env  string `json:"env,omitempty"`
	File string `json:"file,omitempty"`
	// Value supports explicit inline secrets via object form, though string form is preferred.
	Value string `json:"value,omitempty"`
}

func (s SecretRef) IsZero() bool {
	return strings.TrimSpace(s.Inline) == "" &&
		strings.TrimSpace(s.Env) == "" &&
		strings.TrimSpace(s.File) == ""
}

// Present reports whether the reference is configured (inline/env/file is set).
// It does not attempt to resolve env/file.
func (s SecretRef) Present() bool {
	return !s.IsZero()
}

func (s SecretRef) describe() string {
	if strings.TrimSpace(s.Env) != "" {
		return "env:" + strings.TrimSpace(s.Env)
	}
	if strings.TrimSpace(s.File) != "" {
		return "file:" + strings.TrimSpace(s.File)
	}
	if strings.TrimSpace(s.Inline) != "" {
		return "inline"
	}
	return "unset"
}

// Normalize rewrites file refs to absolute paths when baseDir is provided.
// This keeps resolution logic simple for downstream code that only sees SecretRef.
func (s SecretRef) Normalize(baseDir string) SecretRef {
	out := s
	out.Env = strings.TrimSpace(out.Env)
	out.File = strings.TrimSpace(out.File)

	if out.File == "" {
		return out
	}

	// Expand "~/" in file paths.
	if strings.HasPrefix(out.File, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if out.File == "~" {
				out.File = home
			} else if strings.HasPrefix(out.File, "~/") {
				out.File = filepath.Join(home, out.File[2:])
			}
		}
	}

	if filepath.IsAbs(out.File) {
		out.File = filepath.Clean(out.File)
		return out
	}

	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return out
	}

	out.File = filepath.Clean(filepath.Join(baseDir, out.File))
	return out
}

// Resolve materializes the secret. baseDir is used only when s.File is relative.
// It returns an error when the secret is present but cannot be resolved.
func (s SecretRef) Resolve(baseDir string) (string, error) {
	if strings.TrimSpace(s.Inline) != "" {
		return s.Inline, nil
	}

	env := strings.TrimSpace(s.Env)
	if env != "" {
		val, ok := os.LookupEnv(env)
		if !ok {
			return "", fmt.Errorf("secret %s not set", s.describe())
		}
		val = strings.TrimSpace(val)
		if val == "" {
			return "", fmt.Errorf("secret %s is empty", s.describe())
		}
		return val, nil
	}

	file := strings.TrimSpace(s.File)
	if file != "" {
		if !filepath.IsAbs(file) {
			baseDir = strings.TrimSpace(baseDir)
			if baseDir == "" {
				return "", fmt.Errorf("secret %s uses relative file path but base dir is unknown", s.describe())
			}
			file = filepath.Join(baseDir, file)
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("secret %s read failed: %w", s.describe(), err)
		}
		val := strings.TrimSpace(string(b))
		if val == "" {
			return "", fmt.Errorf("secret %s is empty", s.describe())
		}
		return val, nil
	}

	return "", nil
}

func (s *SecretRef) UnmarshalJSON(data []byte) error {
	if s == nil {
		return fmt.Errorf("SecretRef: nil receiver")
	}

	if len(data) == 0 {
		*s = SecretRef{}
		return nil
	}

	// null
	if strings.TrimSpace(string(data)) == "null" {
		*s = SecretRef{}
		return nil
	}

	// Accept string form for backward compatibility.
	var inline string
	if err := json.Unmarshal(data, &inline); err == nil {
		*s = SecretRef{Inline: inline}
		return nil
	}

	var raw secretRefJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	env := strings.TrimSpace(raw.Env)
	file := strings.TrimSpace(raw.File)
	val := raw.Value

	nonEmpty := 0
	if env != "" {
		nonEmpty++
	}
	if file != "" {
		nonEmpty++
	}
	if strings.TrimSpace(val) != "" {
		nonEmpty++
	}
	if nonEmpty > 1 {
		return fmt.Errorf("SecretRef: only one of env/file/value may be set (got env=%t file=%t value=%t)",
			env != "",
			file != "",
			strings.TrimSpace(val) != "",
		)
	}

	*s = SecretRef{
		Inline: val,
		Env:    env,
		File:   file,
	}
	return nil
}

func (s SecretRef) MarshalJSON() ([]byte, error) {
	if strings.TrimSpace(s.Env) != "" || strings.TrimSpace(s.File) != "" {
		return json.Marshal(secretRefJSON{
			Env:  strings.TrimSpace(s.Env),
			File: strings.TrimSpace(s.File),
		})
	}
	return json.Marshal(s.Inline)
}
