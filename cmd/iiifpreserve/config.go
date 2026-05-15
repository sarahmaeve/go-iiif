package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// defaultStoreName is the persistent image-library dir created under $HOME
// when no -store flag and no config `store=` is given.
const defaultStoreName = "iiif-images"

// configPath is the tiny key=value config file (no YAML/TOML dependency —
// the project is deliberately stdlib-only).
func configPath(home string) string {
	return filepath.Join(home, ".config", "iiifpreserve", "config")
}

// parseConfig reads `key = value` lines. Blank lines and `#` comments are
// ignored; keys and values are trimmed. A missing file is not this
// function's concern (the caller passes nil on absence).
func parseConfig(r io.Reader) (map[string]string, error) {
	cfg := make(map[string]string)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("config: malformed line %q (want key=value)", line)
		}
		cfg[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("config: reading: %w", err)
	}
	return cfg, nil
}

// loadConfig reads configPath(home) if it exists; absence yields an empty
// map (config is optional).
func loadConfig(home string) (map[string]string, error) {
	f, err := os.Open(configPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("config: %w", err)
	}
	defer func() { _ = f.Close() }()
	return parseConfig(f)
}

// resolveStore picks the persistent storage root: -store flag wins, then the
// config `store=` value, then $HOME/iiif-images. A leading ~/ is expanded.
func resolveStore(flagVal string, cfg map[string]string, home string) string {
	switch {
	case flagVal != "":
		return expandHome(flagVal, home)
	case cfg["store"] != "":
		return expandHome(cfg["store"], home)
	default:
		return filepath.Join(home, defaultStoreName)
	}
}

// expandHome turns a leading ~/ (or bare ~) into home.
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
