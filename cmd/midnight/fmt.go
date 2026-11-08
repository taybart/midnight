package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/parse"
)

const fmtUsage = `Usage: midnight fmt [options] [paths]
Formats Midnight files (or all *.mn and *.midnight files under directories) in place. Paths
default to the current directory, '-' formats stdin to stdout.
Available options are:
  -check   don't write files, list unformatted files and exit 1 if there are any`

// fmtCommand implements `midnight fmt`.
func fmtCommand(args []string) int {
	var opt_check bool
	flags := flag.NewFlagSet("fmt", flag.ContinueOnError)
	flags.BoolVar(&opt_check, "check", false, "")
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}

	paths, err := parseInterspersed(flags, args)
	if err == flag.ErrHelp {
		fmt.Println(fmtUsage)
		return 0
	} else if err != nil {
		fmt.Println(err.Error())
		return 1
	}

	var opts format.Options

	if len(paths) == 0 {
		paths = []string{"."}
	}

	status := 0
	for _, root := range paths {
		if root == "-" {
			source, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				return 1
			}
			formatted, err := format.Format(source, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "<stdin>: %s\n", err)
				return 1
			}
			if opt_check {
				if !bytes.Equal(source, formatted) {
					fmt.Println("<stdin>")
					status = 1
				}
			} else {
				os.Stdout.Write(formatted)
			}
			continue
		}

		info, err := os.Stat(root)
		if err != nil {
			fmt.Println(err.Error())
			return 1
		}

		if !info.IsDir() {
			if !fmtFile(root, opt_check, opts) {
				status = 1
			}
			continue
		}

		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && parse.IsMidnightPath(path) && !fmtFile(path, opt_check, opts) {
				status = 1
			}
			return nil
		})
		if err != nil {
			fmt.Println(err.Error())
			return 1
		}
	}

	return status
}

// fmtFile formats a file in place (or checks it), returning false if it
// failed (or needs formatting, when checking).
func fmtFile(path string, check bool, opts format.Options) bool {
	source, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err.Error())
		return false
	}

	formatted, err := format.Format(source, opts)
	if err != nil {
		fmt.Printf("%s: %s\n", path, err)
		return false
	}

	if bytes.Equal(source, formatted) {
		return true
	}

	if check {
		fmt.Println(path)
		return false
	}

	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		fmt.Println(err.Error())
		return false
	}
	fmt.Println(path)
	return true
}
