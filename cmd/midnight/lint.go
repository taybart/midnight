package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/taybart/midnight/lint"
	"github.com/taybart/midnight/parse"
)

// lintIgnoreComment suppresses diagnostics on the line it appears on.
const lintIgnoreComment = "midnight-lint: ignore"

const lintUsage = `Usage: midnight lint [options] [paths]
Checks Midnight files (or all *.mn and *.midnight files under directories) for likely mistakes.
Paths default to the current directory, '-' reads stdin. Explicitly named *.lua
files are checked as Lua. Prints 'path:line: message (rule)' and exits 1 if
there are any problems.

Rules:
  undefined-global  reading a global that is never set
  set-global        setting an undeclared global (use 'global' or 'local')
  unused-local      a local that is never read (names starting w/ '_' are ok)
  shadow            a local that shadows another local
  redefined         a local declared twice in the same scope
  unreachable       code after return / break / continue / goto
  fat-arrow-component
                    a '=>' function that builds markup but doesn't use self
                    (a component's props would be self, use '->')
  syntax            the file doesn't parse

Add '` + lintIgnoreComment + `' in a comment to ignore a line.

Globals are also read from the nearest .luarc.json (lua-language-server's
config): names in 'diagnostics.globals', and the top-level globals defined by
the *.lua files in 'workspace.library'.

Available options are:
  -g list  additional globals, comma separated (ex. ones set by the host)
  -markup-factory name
           function markup elements call (default: h, see docs/MARKUP.md)`

// lintCommand implements `midnight lint`.
func lintCommand(args []string) int {
	var opt_g string
	var midnightOpts parse.MidnightOptions
	flags := flag.NewFlagSet("lint", flag.ContinueOnError)
	flags.StringVar(&opt_g, "g", "", "")
	flags.Var(markupFactoryFlag{&midnightOpts}, "markup-factory", "")
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}

	paths, err := parseInterspersed(flags, args)
	if err == flag.ErrHelp {
		fmt.Println(lintUsage)
		return 0
	} else if err != nil {
		fmt.Println(err.Error())
		return 1
	}

	var lintOpts lint.Options
	for _, name := range strings.Split(opt_g, ",") {
		if name = strings.TrimSpace(name); name != "" {
			lintOpts.Globals = append(lintOpts.Globals, name)
		}
	}

	if len(paths) == 0 {
		paths = []string{"."}
	}

	luarcs := luarcCache{}
	problems := 0
	for _, root := range paths {
		if root == "-" {
			source, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Println(err.Error())
				return 1
			}
			opts, err := luarcs.options(".", lintOpts)
			if err != nil {
				fmt.Println(err.Error())
				return 1
			}
			problems += lintSource(source, "<stdin>", true, midnightOpts, opts)
			continue
		}

		info, err := os.Stat(root)
		if err != nil {
			fmt.Println(err.Error())
			return 1
		}

		if !info.IsDir() {
			problems += lintFile(root, !strings.HasSuffix(root, ".lua"), midnightOpts, lintOpts, luarcs)
			continue
		}

		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && parse.IsMidnightPath(path) {
				problems += lintFile(path, true, midnightOpts, lintOpts, luarcs)
			}
			return nil
		})
		if err != nil {
			fmt.Println(err.Error())
			return 1
		}
	}

	if problems > 0 {
		return 1
	}
	return 0
}

func lintFile(path string, isMidnight bool, midnightOpts parse.MidnightOptions, lintOpts lint.Options, luarcs luarcCache) int {
	source, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err.Error())
		return 1
	}
	opts, err := luarcs.options(filepath.Dir(path), lintOpts)
	if err != nil {
		fmt.Println(err.Error())
		return 1
	}
	return lintSource(source, path, isMidnight, midnightOpts, opts)
}

// luarcCache holds the globals of each .luarc.json seen, by path.
type luarcCache map[string][]string

// options adds the globals from the .luarc.json nearest dir (if any) to opts.
func (c luarcCache) options(dir string, opts lint.Options) (lint.Options, error) {
	path := lint.FindLuarc(dir)
	if path == "" {
		return opts, nil
	}
	globals, ok := c[path]
	if !ok {
		var err error
		if globals, err = lint.LuarcGlobals(path); err != nil {
			return opts, err
		}
		c[path] = globals
	}
	opts.Globals = append(append([]string{}, opts.Globals...), globals...)
	return opts, nil
}

// lintSource prints the problems in a file, returning how many there were.
func lintSource(source []byte, path string, isMidnight bool, midnightOpts parse.MidnightOptions, lintOpts lint.Options) int {
	lines := strings.Split(string(source), "\n")

	chunk, err := parseSource(source, path, isMidnight, midnightOpts)
	if err != nil {
		var perr *parse.Error
		if !errors.As(err, &perr) {
			fmt.Printf("%s: %s (syntax)\n", path, err)
			return 1
		}
		line := perr.Pos.Line
		if line == parse.EOF {
			line = len(strings.Split(strings.TrimRight(string(source), "\n"), "\n"))
		}
		fmt.Printf("%s:%d: %s (syntax)\n", path, line, strings.TrimSpace(perr.Message))
		return 1
	}

	problems := 0
	for _, d := range lint.Check(chunk, lintOpts) {
		if d.Line >= 1 && d.Line <= len(lines) && strings.Contains(lines[d.Line-1], lintIgnoreComment) {
			continue
		}
		fmt.Printf("%s:%d: %s (%s)\n", path, d.Line, d.Message, d.Rule)
		problems++
	}
	return problems
}
