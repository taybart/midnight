package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"strings"

	"github.com/chzyer/readline"
	"github.com/taybart/midnight"
	"github.com/taybart/midnight/ast"
	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

func main() {
	os.Exit(mainAux())
}

func mainAux() int {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "compile":
			return compileCommand(os.Args[2:])
		case "clean":
			return cleanCommand(os.Args[2:])
		case "lint":
			return lintCommand(os.Args[2:])
		case "fmt":
			return fmtCommand(os.Args[2:])
		}
	}

	var opt_e, opt_l, opt_p, opt_lang string
	var midnightOpts parse.MidnightOptions
	var opt_i, opt_v, opt_dt, opt_dc, opt_c bool
	var opt_m int
	flag.StringVar(&opt_e, "e", "", "")
	flag.StringVar(&opt_l, "l", "", "")
	flag.StringVar(&opt_p, "p", "", "")
	flag.StringVar(&opt_lang, "lang", "", "")
	flag.Var(markupFactoryFlag{&midnightOpts}, "markup-factory", "")
	flag.IntVar(&opt_m, "mx", 0, "")
	flag.BoolVar(&opt_i, "i", false, "")
	flag.BoolVar(&opt_v, "v", false, "")
	flag.BoolVar(&opt_dt, "dt", false, "")
	flag.BoolVar(&opt_dc, "dc", false, "")
	flag.BoolVar(&opt_c, "c", false, "")
	flag.Usage = func() {
		fmt.Println(`Usage: midnight [options] [script [args]].
       midnight compile [options] [paths]  (see 'midnight compile -h')
       midnight clean [paths]              (see 'midnight clean -h')
       midnight lint [options] [paths]     (see 'midnight lint -h')
       midnight fmt [options] [paths]      (see 'midnight fmt -h')
Available options are:
  -e stat  execute string 'stat'
  -lang l  source language, 'midnight' or 'lua' (default: 'lua' for *.lua
           scripts, otherwise 'midnight')
  -l name  require library 'name'
  -markup-factory name
           function markup elements call (default: h, see docs/MARKUP.md)
  -mx MB   memory limit(default: unlimited)
  -dt      dump AST trees
  -dc      dump VM codes
  -c       print 'script' compiled to Lua (without running it)
  -i       enter interactive mode after executing 'script'
  -p file  write cpu profiles to the file
  -v       show version information
A script of '-' is read from stdin.`)
	}
	flag.Parse()
	if opt_lang != "" && opt_lang != "midnight" && opt_lang != "lua" {
		fmt.Printf("invalid -lang '%s' (expected 'midnight' or 'lua')\n", opt_lang)
		return 1
	}
	if len(opt_p) != 0 {
		f, err := os.Create(opt_p)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	if len(opt_e) == 0 && !opt_i && !opt_v && flag.NArg() == 0 {
		opt_i = true
	}

	status := 0

	L := lua.NewState(lua.Options{Midnight: midnightOpts})
	defer L.Close()
	if opt_m > 0 {
		L.SetMx(opt_m)
	}

	if opt_v || opt_i {
		fmt.Println(lua.MidnightCopyRight)
	}

	if len(opt_l) > 0 {
		if err := L.DoFile(opt_l); err != nil {
			fmt.Println(err.Error())
		}
	}

	if nargs := flag.NArg(); nargs > 0 {
		script := flag.Arg(0)
		argtb := L.NewTable()
		for i := 1; i < nargs; i++ {
			L.RawSet(argtb, lua.LNumber(i), lua.LString(flag.Arg(i)))
		}
		L.SetGlobal("arg", argtb)
		source, err := readScript(script)
		if err != nil {
			fmt.Println(err.Error())
			return 1
		}
		isMidnight := opt_lang == "midnight" || opt_lang == "" && !strings.HasSuffix(script, ".lua")
		if opt_c {
			chunk, err := parseSource(source, script, isMidnight, midnightOpts)
			if err != nil {
				fmt.Println(err.Error())
				return 1
			}
			fmt.Print(luagen.Generate(chunk))
			return 0
		}
		if opt_dt || opt_dc {
			chunk, err2 := parseSource(source, script, isMidnight, midnightOpts)
			if err2 != nil {
				fmt.Println(err2.Error())
				return 1
			}
			if opt_dt {
				fmt.Println(parse.Dump(chunk))
			}
			if opt_dc {
				proto, err3 := lua.Compile(chunk, script)
				if err3 != nil {
					fmt.Println(err3.Error())
					return 1
				}
				fmt.Println(proto.String())
			}
		}
		if err := doSource(L, source, script, isMidnight); err != nil {
			fmt.Println(err.Error())
			status = 1
		}
	}

	if len(opt_e) > 0 {
		if err := doSource(L, []byte(opt_e), "<string>", opt_lang != "lua"); err != nil {
			fmt.Println(err.Error())
			status = 1
		}
	}

	if opt_i {
		doREPL(L, opt_lang != "lua")
	}
	return status
}

// readScript reads a script ('-' for stdin). Works for pipes / process
// substitution (ex. `midnight <(cat script.mn)`), which have no file extension.
func readScript(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func parseSource(source []byte, name string, isMidnight bool, midnightOpts parse.MidnightOptions) ([]ast.Stmt, error) {
	if isMidnight {
		return parse.ParseMidnightWithOptions(bytes.NewReader(source), name, midnightOpts)
	}
	if bytes.HasPrefix(source, []byte("#")) {
		// Skip the shebang line, but keep the newline so line numbers match.
		if i := bytes.IndexByte(source, '\n'); i >= 0 {
			source = source[i:]
		} else {
			source = nil
		}
	}
	return parse.Parse(bytes.NewReader(source), name)
}

func doSource(L *lua.LState, source []byte, name string, isMidnight bool) error {
	chunk, err := parseSource(source, name, isMidnight, L.Options.Midnight)
	if err != nil {
		return err
	}
	proto, err := lua.Compile(chunk, name)
	if err != nil {
		return err
	}
	L.Push(L.NewFunctionFromProto(proto))
	return L.PCall(0, lua.MultRet, nil)
}

// do read/eval/print/loop
func doREPL(L *lua.LState, isMidnight bool) {
	rl, err := readline.New("> ")
	if err != nil {
		panic(err)
	}
	defer rl.Close()
	for {
		fn, err := loadline(rl, L, isMidnight)
		if err == io.EOF || err == readline.ErrInterrupt {
			return
		} else if err != nil {
			fmt.Println(err)
			continue
		}

		top := L.GetTop()
		L.Push(fn)
		if err := L.PCall(0, lua.MultRet, nil); err != nil {
			fmt.Println(err)
			continue
		}

		// print returned values (ex. from `1 + 1`)
		if L.GetTop() > top {
			results := make([]string, 0, L.GetTop()-top)
			for i := top + 1; i <= L.GetTop(); i++ {
				results = append(results, L.ToStringMeta(L.Get(i)).String())
			}
			fmt.Println(strings.Join(results, "\t"))
			L.SetTop(top)
		}
	}
}

func loadString(L *lua.LState, source string, isMidnight bool) (*lua.LFunction, error) {
	if isMidnight {
		return L.LoadMidnightString(source)
	}
	return L.LoadString(source)
}

func incomplete(err error) bool {
	if lerr, ok := err.(*lua.ApiError); ok {
		if perr, ok := lerr.Cause.(*parse.Error); ok {
			return perr.Pos.Line == parse.EOF
		}
	}
	return false
}

// loadline reads a chunk, prompting for more lines while it is incomplete.
// Expressions are evaluated as `return <expression>` so their values print.
func loadline(rl *readline.Instance, L *lua.LState, isMidnight bool) (*lua.LFunction, error) {
	rl.SetPrompt("> ")
	chunk, err := rl.Readline()
	if err != nil {
		return nil, err
	}

	for {
		if fn, err := loadString(L, "return "+chunk, isMidnight); err == nil {
			return fn, nil
		}

		fn, err := loadString(L, chunk, isMidnight)
		if !incomplete(err) {
			return fn, err
		}

		rl.SetPrompt(">> ")
		line, err := rl.Readline()
		if err != nil {
			return nil, err
		}
		chunk += "\n" + line
	}
}

// markupFactoryFlag is the -markup-factory flag (see
// parse.MidnightOptions.MarkupFactory).
type markupFactoryFlag struct{ opts *parse.MidnightOptions }

func (f markupFactoryFlag) String() string {
	if f.opts == nil {
		return ""
	}
	return f.opts.MarkupFactory
}

func (f markupFactoryFlag) Set(value string) error { return f.opts.SetMarkupFactory(value) }
