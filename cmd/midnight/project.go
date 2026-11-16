package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/taybart/midnight/lint"
)

// projectFileName is the project config (JSON, w/ comments and trailing
// commas allowed, like .luarc.json), looked up from the working directory (or
// a file being compiled) and its parents:
//
//	{
//	  "compile": { "src": "midnight", "out": "lua" }
//	}
//
// `compile.src` / `compile.out` are relative to the config file. `midnight
// compile` w/o paths compiles src into out, and files under src compile to
// the same path under out (instead of next to the source).
const projectFileName = "midnight.json"

type projectConfig struct {
	// absolute paths (src / out resolved against the config's directory)
	path, src, out string
}

// findProject returns the nearest midnight.json in dir or its parents, or nil
// if there is none (or it doesn't configure compile.src / compile.out).
func findProject(dir string) (*projectConfig, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for {
		path := filepath.Join(dir, projectFileName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return loadProject(path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

func loadProject(path string) (*projectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	v, err := lint.ParseJSONC(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	root, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected an object", path)
	}
	compile, _ := root["compile"].(map[string]any)
	src, _ := compile["src"].(string)
	out, _ := compile["out"].(string)
	if src == "" || out == "" {
		return nil, nil
	}
	base := filepath.Dir(path)
	return &projectConfig{path: path, src: filepath.Join(base, src), out: filepath.Join(base, out)}, nil
}

// outputFor returns where the project compiles path to, if it's under src.
func (cfg *projectConfig) outputFor(path string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(cfg.src, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(cfg.out, rel), true
}

// displayPath shortens path relative to the working directory, when it's
// under it.
func displayPath(path string) string {
	wd, err := os.Getwd()
	if err != nil || !filepath.IsAbs(path) {
		return path
	}
	if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
