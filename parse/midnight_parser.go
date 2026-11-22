package parse

import (
	"fmt"
	"io"
	"strconv"

	"github.com/taybart/midnight/ast"
)

// Midnight parser. Midnight is a Lua dialect, so instead
// of compiling to Lua source (like the Erde reference compiler, erde/compile.erde)
// we lower it directly into gopher-lua's (Lua 5.1) AST. Parsing mirrors the
// reference compiler, so the two should be read side by side.

var midnightKeywords = map[string]bool{
	"break": true, "continue": true, "do": true, "else": true, "elseif": true,
	"for": true, "function": true, "global": true, "if": true, "in": true,
	"local": true, "module": true, "repeat": true, "return": true,
	"until": true, "while": true,
}

var midnightTerminals = map[string]bool{
	"true": true, "false": true, "nil": true, "...": true,
}

type midnightBinop struct {
	prec       int
	rightAssoc bool
}

var midnightBinops = map[string]midnightBinop{
	"||": {3, false},
	"&&": {4, false},
	"==": {5, false}, "!=": {5, false}, "<=": {5, false}, ">=": {5, false}, "<": {5, false}, ">": {5, false},
	"|":  {6, false},
	"~":  {7, false},
	"&":  {8, false},
	"<<": {9, false}, ">>": {9, false},
	"..": {10, false},
	"+":  {11, false}, "-": {11, false},
	"*": {12, false}, "/": {12, false}, "//": {12, false}, "%": {12, false},
	"^": {14, true},
}

const midnightUnopPrec = 13

var midnightBitlibMethods = map[string]string{
	"|": "bor", "~": "bxor", "&": "band", "<<": "lshift", ">>": "rshift",
}

// Names for the library tables used by lowered operators. These are resolved
// like any other name, same as the reference compiler.
const (
	midnightBitlib     = "bit32"
	midnightModuleName = "_MODULE"
)

const (
	midnightScopeLocal  = "local"
	midnightScopeGlobal = "global"
	midnightScopeModule = "module"
)

type midnightLoop struct {
	continueLabel string
	hasContinue   bool
}

type midnightParser struct {
	source string
	tokens []midnightToken
	index  int
	cur    midnightToken

	blockDepth int
	scopes     []map[string]string
	// The names in each scope declared w/ `const`, and their lines.
	consts     []map[string]int
	tmpCounter int

	// Innermost loop, or nil outside of loops (reset at function boundaries).
	loop *midnightLoop

	opts MidnightOptions
	// Token index of the current match arm's `=>`, which ends the arm's
	// patterns instead of starting an arrow function (-1 outside of patterns).
	armArrowIndex int
	// Local holding the automatic markup factory (see markupFactory), or "" if
	// unused.
	markupRuntime string
	// if / match / catch expressions, by the expression they were parsed to,
	// so they can be lowered to statements where they're used (see
	// midnight_cond.go).
	lowered map[ast.Expr]midnightLowerable
	// Lowering into a target in the enclosing function (see hoist).
	hoisting bool
	// catch expressions whose handler exits (`catch e { return nil, e }`),
	// which must be hoisted (see midnight_catch.go).
	exitingCatches []*midnightCatch
	// Token index of the `{` starting the block of the statement whose header
	// is being parsed (ex. `if <header> {`), which can't start a catch body
	// (-1 outside of headers, see header).
	headerBraceIndex int

	// Parent of the class being declared (`super`), or nil (see
	// midnight_class.go).
	classSuper func(line int) ast.Expr

	isVarargs           bool
	isModuleReturnBlock bool
	moduleReturnLine    int
	hasModuleDecls      bool
	// Symbol recording (see midnight_symbols.go): nil unless collecting.
	// declIDs parallels scopes, mapping names to their index in syms.Decls.
	// lastName is the token index of the last name parsed.
	syms     *Symbols
	declIDs  []map[string]int
	lastName int

	// Names declared w/ `module` at the top level, in declaration order.
	moduleNames []string
	// Of those, the ones declared w/ `module const`, and their lines.
	moduleConsts map[string]int
}

// Result of parsing an index chain (ex. `a.b[c]:d()`).
type midnightChain struct {
	expr     ast.Expr
	chainLen int
	isCall   bool
	// The name being assigned to when the chain is a name (`x`) or a module
	// field (`module.x`), else "" (see checkAssignable).
	name       string
	isModule   bool
	finalBase  ast.Expr // `a.b` in `a.b[c]`
	finalIndex ast.Expr // `c` in `a.b[c]`
}

// Result of parsing a destructure (ex. `{ a, b: c = 1 }`).
type midnightDestructure struct {
	tmpName string
	names   []string
	stmts   []ast.Stmt
	// Token index of each name (parallel to names), and for a map
	// destructure, the key of each.
	tokens []int
	keys   []string
}

// declareAll declares the destructure's names.
func (d *midnightDestructure) declareAll(p *midnightParser, scope string) {
	for i, n := range d.names {
		p.declareAt(n, scope, d.tokens[i])
	}
}

// ParseMidnight parses Midnight source into a Lua chunk, which can be passed to
// `lua.Compile` like any chunk returned by `Parse`.
func ParseMidnight(reader io.Reader, name string) ([]ast.Stmt, error) {
	return ParseMidnightWithOptions(reader, name, MidnightOptions{})
}

// ParseMidnightWithOptions is like ParseMidnight, but w/ options (ex. the markup
// factory).
func ParseMidnightWithOptions(reader io.Reader, name string, opts MidnightOptions) (chunk []ast.Stmt, err error) {
	text, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	tokens, err := midnightTokenize(string(text), name)
	if err != nil {
		return nil, err
	}

	// Module names are hoisted: they're visible in the whole file, not just
	// after their declaration (they're fields of the module table, so they're
	// looked up when used, same as `M.x` in Lua). A first pass collects them.
	// (and so is whether they're const)
	var pre *midnightParser
	if midnightHasModuleKeyword(tokens) {
		pre = newMidnightParser(name, tokens, opts, nil)
		if _, err := pre.run(); err != nil {
			return nil, err
		}
	}

	p := newMidnightParser(name, tokens, opts, pre)
	if opts.Symbols != nil {
		p.syms, p.declIDs = opts.Symbols, []map[string]int{{}}
		defer p.linkSymbols()
	}
	return p.run()
}

// newMidnightParser returns a parser for tokens. pre is the first pass, which
// collected the module names (or nil, when there's no first pass).
func newMidnightParser(name string, tokens []midnightToken, opts MidnightOptions, pre *midnightParser) *midnightParser {
	root := map[string]string{}
	rootConsts := map[string]int{}
	if pre != nil {
		for _, n := range pre.moduleNames {
			root[n] = midnightScopeModule
		}
		for n, line := range pre.moduleConsts {
			rootConsts[n] = line
		}
	}
	return &midnightParser{
		source:              name,
		tokens:              tokens,
		cur:                 tokens[0],
		blockDepth:          1,
		scopes:              []map[string]string{root},
		consts:              []map[string]int{rootConsts},
		isVarargs:           true,
		isModuleReturnBlock: true,
		opts:                opts,
		armArrowIndex:       -1,
		headerBraceIndex:    -1,
	}
}

func (p *midnightParser) run() (chunk []ast.Stmt, err error) {
	defer func() {
		if e := recover(); e != nil {
			if perr, ok := e.(*Error); ok {
				chunk, err = nil, perr
				return
			}
			panic(e)
		}
	}()

	return p.module(), nil
}

func midnightHasModuleKeyword(tokens []midnightToken) bool {
	for _, t := range tokens {
		if t.Type == midnightWord && t.Value == "module" {
			return true
		}
	}
	return false
}

/* Parse helpers {{{ */

func (p *midnightParser) throw(line int, format string, args ...interface{}) {
	panic(&Error{
		Pos:     ast.Position{Source: p.source, Line: line},
		Message: fmt.Sprintf(format, args...),
		Token:   p.cur.Value,
	})
}

// throwEOF throws an error for input that ends early. These are reported at
// line `EOF` (same as the Lua parser), so callers can tell incomplete input
// apart from invalid input (ex. to continue reading lines in a REPL).
func (p *midnightParser) throwEOF(format string, args ...interface{}) {
	panic(&Error{
		Pos:     ast.Position{Source: p.source, Line: EOF},
		Message: fmt.Sprintf(format, args...),
	})
}

func (p *midnightParser) consume() string {
	value := p.cur.Value
	p.index++
	p.cur = p.tokens[p.index]
	return value
}

func (p *midnightParser) prevLine() int { return p.tokens[p.index-1].Line }

func isMidnightSyntax(token midnightToken, value string) bool {
	switch token.Type {
	case midnightSymbol, midnightWord, midnightInterpolation:
		return token.Value == value
	}
	return false
}

func (p *midnightParser) is(value string) bool { return isMidnightSyntax(p.cur, value) }

func (p *midnightParser) isArrow(index int) bool {
	token := p.tokens[index]
	return index != p.armArrowIndex && (isMidnightSyntax(token, "->") || isMidnightSyntax(token, "=>"))
}

func (p *midnightParser) branch(value string) bool {
	if p.is(value) {
		p.consume()
		return true
	}
	return false
}

func (p *midnightParser) expect(value string) {
	if p.cur.Type == midnightEOF {
		p.throwEOF("unexpected eof (expected %s)", value)
	}
	if !p.is(value) {
		p.throw(p.cur.Line, "expected '%s' got '%s'", value, p.cur.Value)
	}
	p.consume()
}

func (p *midnightParser) list(callback func()) {
	for {
		callback()
		if !p.branch(",") {
			return
		}
	}
}

// surroundList parses `open {item ,} close`, allowing a trailing comma.
func (p *midnightParser) surroundList(open, close string, allowEmpty bool, callback func()) {
	p.expect(open)
	if !p.is(close) || !allowEmpty {
		for {
			callback()
			if !p.branch(",") || p.is(close) {
				break
			}
		}
	}
	p.expect(close)
}

var midnightSurroundEnds = map[string]string{"(": ")", "[": "]", "{": "}"}

// lookPastSurround returns the token (and its index) following the surround
// (parens, brackets, or braces) starting at `start`.
func (p *midnightParser) lookPastSurround(start int) (midnightToken, int) {
	open := p.tokens[start]
	close := midnightSurroundEnds[open.Value]
	depth := 1

	i := start + 1
	for depth > 0 {
		token := p.tokens[i]
		if token.Type == midnightEOF {
			p.throwEOF("unexpected eof, missing '%s'", close)
		}
		if isMidnightSyntax(token, open.Value) {
			depth++
		} else if isMidnightSyntax(token, close) {
			depth--
		}
		i++
	}

	return p.tokens[i], i
}

func (p *midnightParser) newTmpName() string {
	p.tmpCounter++
	// Not a valid identifier, so it can never clash w/ user names.
	return "(midnight " + strconv.Itoa(p.tmpCounter) + ")"
}

/* }}} */

/* Scopes {{{ */

func (p *midnightParser) pushScope() {
	p.scopes = append(p.scopes, map[string]string{})
	p.consts = append(p.consts, map[string]int{})
	if p.syms != nil {
		p.declIDs = append(p.declIDs, map[string]int{})
	}
}

func (p *midnightParser) popScope() {
	p.scopes = p.scopes[:len(p.scopes)-1]
	p.consts = p.consts[:len(p.consts)-1]
	if p.syms != nil {
		p.declIDs = p.declIDs[:len(p.declIDs)-1]
	}
}

// declare declares name in the current scope. (A name declared again, ex.
// `local x` after `const x`, is a new variable, so it isn't const.)
func (p *midnightParser) declare(name, scope string) {
	if scope == midnightScopeModule && len(p.scopes) == 1 {
		p.moduleNames = append(p.moduleNames, name)
	}
	p.scopes[len(p.scopes)-1][name] = scope
	delete(p.consts[len(p.consts)-1], name)
}

// declareConst marks name, just declared in the current scope, as const.
func (p *midnightParser) declareConst(name, scope string, line int) {
	p.consts[len(p.consts)-1][name] = line
	if scope == midnightScopeModule && len(p.scopes) == 1 {
		if p.moduleConsts == nil {
			p.moduleConsts = map[string]int{}
		}
		p.moduleConsts[name] = line
	}
}

// constLine returns the line name was declared on, if the name in scope is
// const, else 0. (Only names in scope: a global can't be const.)
func (p *midnightParser) constLine(name string) int {
	for i := len(p.scopes) - 1; i >= 0; i-- {
		if _, ok := p.scopes[i][name]; ok {
			return p.consts[i][name]
		}
	}
	return 0
}

// moduleConstLine returns the line of `module const name`, else 0.
func (p *midnightParser) moduleConstLine(name string) int {
	if p.scopes[0][name] != midnightScopeModule {
		return 0
	}
	return p.consts[0][name]
}

// checkAssignable throws if name (in scope, or `module.name` when isModule)
// is const.
func (p *midnightParser) checkAssignable(name string, isModule bool, line int) {
	if name == "" {
		return
	}
	declared := p.constLine(name)
	if isModule {
		declared = p.moduleConstLine(name)
	}
	if declared != 0 {
		p.throw(line, "cannot assign to const '%s' (declared on line %d)", name, declared)
	}
}

func (p *midnightParser) lookup(name string) string {
	for i := len(p.scopes) - 1; i >= 0; i-- {
		if scope, ok := p.scopes[i][name]; ok {
			return scope
		}
	}
	return ""
}

// target returns the expression referring to `name` when declared w/ `scope`.
func (p *midnightParser) target(name, scope string, line int) ast.Expr {
	switch scope {
	case midnightScopeModule:
		return midnightAttr(midnightIdent(midnightModuleName, line), midnightString(name, line), line)
	case midnightScopeGlobal:
		return midnightAttr(midnightIdent("_G", line), midnightString(name, line), line)
	}
	return midnightIdent(name, line)
}

// isModuleAccess checks for `module.x`, which always refers to the module's
// `x` (ex. when a local of the same name shadows it).
func (p *midnightParser) isModuleAccess() bool {
	return p.is("module") && isMidnightSyntax(p.tokens[p.index+1], ".")
}

// nameExpr parses the name starting an index chain: a name in scope, or
// `module` in `module.x`.
func (p *midnightParser) nameExpr(line int) ast.Expr {
	if p.isModuleAccess() {
		p.consume()
		p.hasModuleDecls = true
		if p.syms != nil && p.tokens[p.index+1].Type == midnightWord {
			p.refModule(p.tokens[p.index+1].Value, p.index+1)
		}
		return midnightIdent(midnightModuleName, line)
	}
	name := p.name()
	p.ref(name, p.lastName, 0)
	return p.resolve(name, line)
}

func (p *midnightParser) resolve(name string, line int) ast.Expr {
	scope := p.lookup(name)
	if name == "super" && scope == "" && p.classSuper != nil {
		return p.classSuper(line)
	}
	return p.target(name, scope, line)
}

/* }}} */
