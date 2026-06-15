// Package remote manages external configuration and the GitHub Contents
// backend for optional remote storage of the encrypted .fin file.
package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// GitHub holds the coordinates of the remote file on GitHub.
type GitHub struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Path   string `json:"path"`   // e.g. "finador.fin"
	Branch string `json:"branch"` // e.g. "master"
}

// Config is stored in configDir()/config.json. It is read before the
// encrypted file can be located, so it must live outside the .fin file.
type Config struct {
	Source        string  `json:"source"` // "local" | "github"
	GitHub        *GitHub `json:"github,omitempty"`
	ReadPullAfter string  `json:"readPullAfter,omitempty"` // a Go duration, default "1h"
}

// ReadPullDuration parses the ReadPullAfter field. Returns 1h when empty or invalid.
func (c Config) ReadPullDuration() time.Duration {
	if c.ReadPullAfter == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(c.ReadPullAfter)
	if err != nil || d <= 0 {
		return time.Hour
	}
	return d
}

// configDir returns the directory that holds config.json.
// The env var FINADOR_CONFIG_DIR overrides the platform default.
func configDir() (string, error) {
	if dir := os.Getenv("FINADOR_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return filepath.Join(base, "finador"), nil
}

// configPath returns the path to config.json.
func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads and validates the config file. A missing file is not an error;
// it returns Config{Source: "local"}.
func Load() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Source: "local"}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return validate(c)
}

// Save writes c to disk. The directory is created if needed. The write is
// atomic (tmp file + rename) and the file is set to mode 0600.
func Save(c Config) error {
	c, err := validate(c)
	if err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	// Atomic write: write to a temp file in the same directory, then rename.
	tmp, err := os.CreateTemp(dir, "config.*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = os.Remove(tmpName) // no-op if rename succeeded
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// validate normalises and validates c. Returns a validated copy.
func validate(c Config) (Config, error) {
	if c.Source == "" {
		c.Source = "local"
	}
	switch c.Source {
	case "local":
		// nothing required
	case "github":
		if c.GitHub == nil {
			return Config{}, fmt.Errorf("config: source=github requires a [github] section")
		}
		if c.GitHub.Owner == "" {
			return Config{}, fmt.Errorf("config: github.owner is required")
		}
		if c.GitHub.Repo == "" {
			return Config{}, fmt.Errorf("config: github.repo is required")
		}
		if c.GitHub.Path == "" {
			return Config{}, fmt.Errorf("config: github.path is required")
		}
		if c.GitHub.Branch == "" {
			c.GitHub.Branch = "master"
		}
	default:
		return Config{}, fmt.Errorf("config: unknown source %q (want \"local\" or \"github\")", c.Source)
	}
	return c, nil
}
