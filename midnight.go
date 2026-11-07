package lua

// Midnight support. Midnight is implemented entirely as
// a second parser front end (parse.ParseMidnight), which produces the same AST as
// the Lua parser. The compiler and VM are unchanged gopher-lua.
//
// To keep merging upstream gopher-lua changes easy, Midnight additions live in new
// files wherever possible. Changes to upstream files are marked w/ `midnight`.

import (
	"io"
	"strings"

	"github.com/taybart/midnight/parse"
)

const MidnightName = "midnight"
const MidnightVersion = "0.1.0"
const MidnightCopyRight = MidnightName + " " + MidnightVersion + " (based on " + PackageName + " " + PackageVersion + ")"

func isMidnightPath(path string) bool {
	return parse.IsMidnightPath(path)
}

// LoadMidnight is like Load, but for Midnight source.
func (ls *LState) LoadMidnight(reader io.Reader, name string) (*LFunction, error) {
	chunk, err := parse.ParseMidnightWithOptions(reader, name, ls.Options.Midnight)
	if err != nil {
		return nil, newApiErrorE(ApiErrorSyntax, err)
	}
	proto, err := Compile(chunk, name)
	if err != nil {
		return nil, newApiErrorE(ApiErrorSyntax, err)
	}
	return newLFunctionL(proto, ls.currentEnv(), 0), nil
}

// LoadMidnightString is like LoadString, but for Midnight source.
func (ls *LState) LoadMidnightString(source string) (*LFunction, error) {
	return ls.LoadMidnight(strings.NewReader(source), "<string>")
}

// DoMidnightString is like DoString, but for Midnight source.
func (ls *LState) DoMidnightString(source string) error {
	if fn, err := ls.LoadMidnightString(source); err != nil {
		return err
	} else {
		ls.Push(fn)
		return ls.PCall(0, MultRet, nil)
	}
}
