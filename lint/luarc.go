package lint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/taybart/midnight/ast"
	"github.com/taybart/midnight/parse"
)

// LuarcName is the lua-language-server config file read for globals.
const LuarcName = ".luarc.json"

// FindLuarc returns the nearest .luarc.json in dir or its parents, or "" if
// there is none.
func FindLuarc(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		path := filepath.Join(dir, LuarcName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// LuarcGlobals returns the globals a .luarc.json makes available, the way
// lua-language-server sees them:
//   - `diagnostics.globals`: names listed directly
//   - `workspace.library`: *.lua files (or directories of them), whose
//     top-level global assignments and functions are definitions (ex. a
//     `---@meta` file w/ `harmony = {}`)
//
// Both the flat ("workspace.library") and nested ({"workspace": {"library"}})
// spellings are read. Library paths are relative to the .luarc.json, missing
// ones are skipped (lua-language-server does the same).
func LuarcGlobals(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	v, err := parseJSONC(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	config, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected an object", path)
	}

	globals := stringList(luarcSetting(config, "diagnostics", "globals"))

	base := filepath.Dir(path)
	for _, lib := range stringList(luarcSetting(config, "workspace", "library")) {
		if strings.Contains(lib, "${") {
			continue // lua-language-server variables (ex. ${3rd})
		}
		if rest, ok := strings.CutPrefix(lib, "~/"); ok {
			if home, err := os.UserHomeDir(); err == nil {
				lib = filepath.Join(home, rest)
			}
		}
		if !filepath.IsAbs(lib) {
			lib = filepath.Join(base, lib)
		}
		names, err := libraryGlobals(lib)
		if err != nil {
			return nil, err
		}
		globals = append(globals, names...)
	}
	return globals, nil
}

// luarcSetting looks up "section.key", flat or nested.
func luarcSetting(config map[string]any, section, key string) any {
	if v, ok := config[section+"."+key]; ok {
		return v
	}
	if nested, ok := config[section].(map[string]any); ok {
		return nested[key]
	}
	return nil
}

func stringList(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// libraryGlobals reads the definitions in a .lua file or a directory of them.
func libraryGlobals(root string) ([]string, error) {
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return fileGlobals(root)
	}

	var globals []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".lua") {
			return nil
		}
		names, err := fileGlobals(path)
		globals = append(globals, names...)
		return err
	})
	return globals, err
}

func fileGlobals(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	chunk, err := parse.Parse(f, path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return DefinedGlobals(chunk), nil
}

// DefinedGlobals returns the globals a chunk defines at its top level:
// `name = ...` and `function name() end` (not fields, ex. `function a.b()`),
// skipping names declared local first.
func DefinedGlobals(chunk []ast.Stmt) []string {
	locals := map[string]bool{}
	seen := map[string]bool{}
	var globals []string
	add := func(expr ast.Expr) {
		ident, ok := expr.(*ast.IdentExpr)
		if ok && !locals[ident.Value] && !seen[ident.Value] {
			seen[ident.Value] = true
			globals = append(globals, ident.Value)
		}
	}

	for _, stmt := range chunk {
		switch st := stmt.(type) {
		case *ast.LocalAssignStmt:
			for _, name := range st.Names {
				locals[name] = true
			}
		case *ast.AssignStmt:
			for _, lhs := range st.Lhs {
				add(lhs)
			}
		case *ast.FuncDefStmt:
			if st.Name.Receiver == nil {
				add(st.Name.Func)
			}
		}
	}
	return globals
}
