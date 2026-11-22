package parse

import "github.com/taybart/midnight/ast"

// Class declarations, lowered to the usual metatable boilerplate:
//
//	local class Server : Base {
//	  kind = 'lsp'                 -- field:       Server.kind = 'lsp'
//	  new(config) { ... }          -- constructor: function Server.new(config)
//	                               --                local self = setmetatable({}, Server)
//	                               --                ...
//	                               --                return self
//	                               --              end
//	  start(buf) { ... }           -- method:      function Server:start(buf) ... end
//	  static parse(s) { ... }      -- static:      function Server.parse(s) ... end
//	  __tostring() { ... }         -- metamethods are methods
//	}
//
// compiles to
//
//	local Server = {}
//	Server.__index = Server
//	setmetatable(Server, { __index = Base })
//	...members, in order
//
// `super` refers to the parent class inside the body (ex. `super.init(self)`).
// The constructor returns `self` unless its body ends in a `return`. A class
// w/o one gets `new()` (a subclass: the parent's `new`, w/ the subclass'
// metatable). Subclasses copy the parent's metamethods they don't define.

// isClassDeclaration checks for a class declaration. `class` is a contextual
// keyword: it is only a declaration when followed by a name on the same line
// (ex. `class Foo {`), so `class = 'x'`, `local class`, and markup `class`
// attributes are unaffected.
// isConstDeclaration checks for `const` starting a declaration: `const x`,
// `const { a } = t`, or `const [a] = t`. (const is a name everywhere else, ex.
// `const = 1`, or `const[1] = 2` when a variable named const is in scope.)
func (p *midnightParser) isConstDeclaration() bool {
	if p.cur.Type != midnightWord || !p.is("const") {
		return false
	}
	next := p.tokens[p.index+1]
	if next.Line != p.cur.Line {
		return false
	}
	if next.Type == midnightWord {
		return !midnightKeywords[next.Value] && !midnightTerminals[next.Value]
	}
	return (isMidnightSyntax(next, "{") || isMidnightSyntax(next, "[")) && p.lookup("const") == ""
}

func (p *midnightParser) isClassDeclaration() bool {
	if p.cur.Type != midnightWord || !p.is("class") {
		return false
	}
	next := p.tokens[p.index+1]
	return next.Type == midnightWord && next.Line == p.cur.Line &&
		!midnightKeywords[next.Value] && !midnightTerminals[next.Value]
}

// classDeclaration parses `class Name [: Parent] { members }`. `scope` is the
// declaration's scope, or "" for a bare `class` (which, like a bare
// `function`, assigns to the name in scope, or a global).
func (p *midnightParser) classDeclaration(scope string) []ast.Stmt {
	line := p.cur.Line
	p.consume() // class
	name := p.name()
	nameTok := p.lastName

	stmts := []ast.Stmt{}
	emptyTable := func() ast.Expr { return midnightLine(&ast.TableExpr{Fields: []*ast.Field{}}, line) }

	switch scope {
	case midnightScopeLocal:
		p.declareAt(name, scope, nameTok)
		stmts = append(stmts, midnightLocal([]string{name}, []ast.Expr{emptyTable()}, line))
	case "":
		p.checkAssignable(name, false, line) // (a bare `class X` assigns X)
		p.ref(name, nameTok, 0)
		scope = p.lookup(name)
		stmts = append(stmts, midnightAssign(p.target(name, scope, line), emptyTable(), line))
	default:
		p.declareAt(name, scope, nameTok)
		stmts = append(stmts, midnightAssign(p.target(name, scope, line), emptyTable(), line))
	}

	// Fresh nodes for each reference to the class (AST nodes aren't shared).
	class := func(line int) ast.Expr { return p.target(name, scope, line) }

	var parent func(line int) ast.Expr
	if p.branch(":") {
		parts := []string{p.name()}
		p.ref(parts[0], p.lastName, 0)
		for p.branch(".") {
			parts = append(parts, p.name())
		}
		baseScope := p.lookup(parts[0])
		parent = func(line int) ast.Expr {
			expr := p.target(parts[0], baseScope, line)
			for _, part := range parts[1:] {
				expr = midnightAttr(expr, midnightString(part, line), line)
			}
			return expr
		}
	}

	// Class.__index = Class
	stmts = append(stmts, midnightAssign(midnightAttr(class(line), midnightString("__index", line), line), class(line), line))

	// setmetatable(Class, { __index = Parent })
	if parent != nil {
		meta := midnightLine(&ast.TableExpr{Fields: []*ast.Field{{Key: midnightString("__index", line), Value: parent(line)}}}, line)
		call := midnightCall(midnightIdent("setmetatable", line), []ast.Expr{class(line), meta}, line)
		stmts = append(stmts, midnightLine(&ast.FuncCallStmt{Expr: call}, line))
	}

	// `super` is a hidden local holding the parent (declared before the
	// members, if used), so locals in member bodies can't shadow it.
	superName, superAt := "", len(stmts)
	var super func(int) ast.Expr
	if parent != nil {
		super = func(line int) ast.Expr {
			if superName == "" {
				superName = p.newTmpName()
			}
			return midnightIdent(superName, line)
		}
	}

	oldSuper := p.classSuper
	p.classSuper = super
	defer func() { p.classSuper = oldSuper }()

	hasNew := false
	p.expect("{")
	for !p.is("}") {
		if p.cur.Type == midnightEOF {
			p.throwEOF("unexpected eof (expected '}')")
		}
		member, isNew := p.classMember(class)
		hasNew = hasNew || isNew
		stmts = append(stmts, member...)
		if !p.branch(",") {
			p.branch(";")
		}
	}
	end := p.cur.Line
	p.expect("}")

	if superName != "" {
		hoisted := midnightLocal([]string{superName}, []ast.Expr{parent(line)}, line)
		stmts = append(stmts[:superAt], append([]ast.Stmt{hoisted}, stmts[superAt:]...)...)
	}

	if !hasNew {
		stmts = append(stmts, p.defaultConstructor(class, parent, end))
	}
	if parent != nil {
		stmts = append(stmts, p.inheritMetamethods(class, parent, end))
	}

	return stmts
}

// defaultConstructor generates `new` for a class without one:
//
//	function Class.new() return setmetatable({}, Class) end
//
// or for a subclass, the parent's constructor w/ the subclass' metatable:
//
//	function Class.new(...) return setmetatable(Parent.new(...), Class) end
func (p *midnightParser) defaultConstructor(class, parent func(int) ast.Expr, line int) ast.Stmt {
	parList := &ast.ParList{Names: []string{}}
	var instance ast.Expr = midnightLine(&ast.TableExpr{Fields: []*ast.Field{}}, line)
	if parent != nil {
		parList.HasVargs = true
		instance = midnightCall(midnightAttr(parent(line), midnightString("new", line), line), []ast.Expr{midnightLine(&ast.Comma3Expr{}, line)}, line)
	}

	create := midnightCall(midnightIdent("setmetatable", line), []ast.Expr{instance, class(line)}, line)
	fn := midnightLine(&ast.FunctionExpr{ParList: parList, Stmts: []ast.Stmt{midnightLine(&ast.ReturnStmt{Exprs: []ast.Expr{create}}, line)}}, line)
	target := midnightAttr(class(line), midnightString("new", line), line)
	return midnightLine(&ast.FuncDefStmt{Name: &ast.FuncName{Func: target}, Func: fn}, line)
}

// inheritMetamethods copies the parent's metamethods the class doesn't define
// (Lua looks metamethods up on the metatable itself, not through `__index`):
//
//	for k, v in pairs(Parent) do
//	  if rawget(Class, k) == nil and type(k) == 'string' and k:sub(1, 2) == '__' then Class[k] = v end
//	end
func (p *midnightParser) inheritMetamethods(class, parent func(int) ast.Expr, line int) ast.Stmt {
	k, v := p.newTmpName(), p.newTmpName()
	str := func(s string) ast.Expr { return midnightString(s, line) }
	eq := func(lhs, rhs ast.Expr) ast.Expr {
		return midnightLine(&ast.RelationalOpExpr{Operator: "==", Lhs: lhs, Rhs: rhs}, line)
	}
	and := func(lhs, rhs ast.Expr) ast.Expr {
		return midnightLine(&ast.LogicalOpExpr{Operator: "and", Lhs: lhs, Rhs: rhs}, line)
	}
	call := func(fn ast.Expr, args ...ast.Expr) ast.Expr { return midnightCall(fn, args, line) }

	unset := eq(call(midnightIdent("rawget", line), class(line), midnightIdent(k, line)), midnightLine(&ast.NilExpr{}, line))
	isString := eq(call(midnightIdent("type", line), midnightIdent(k, line)), str("string"))
	prefix := eq(call(midnightAttr(midnightIdent("string", line), str("sub"), line), midnightIdent(k, line), midnightLine(&ast.NumberExpr{Value: "1"}, line), midnightLine(&ast.NumberExpr{Value: "2"}, line)), str("__"))

	copy := midnightAssign(midnightAttr(class(line), midnightIdent(k, line), line), midnightIdent(v, line), line)
	ifStmt := midnightLine(&ast.IfStmt{Condition: and(and(unset, isString), prefix), Then: []ast.Stmt{copy}, Else: []ast.Stmt{}}, line)

	return midnightLine(&ast.GenericForStmt{
		Names: []string{k, v},
		Exprs: []ast.Expr{call(midnightIdent("pairs", line), parent(line))},
		Stmts: []ast.Stmt{ifStmt},
	}, line)
}

// classMember parses a field (`name = value`), the constructor (`new(...) {}`),
// a method (`name(...) {}`), or a static function (`static name(...) {}`).
func (p *midnightParser) classMember(class func(int) ast.Expr) (stmts []ast.Stmt, isNew bool) {
	line := p.cur.Line
	name := p.name()

	// `static` is contextual: `static = 1` and `static() {}` are a field and
	// a method named static.
	if name == "static" && p.cur.Type == midnightWord {
		name = p.name()
		if !p.is("(") {
			if p.is("=") {
				p.throw(p.cur.Line, "fields are already static (remove 'static')")
			}
			if p.cur.Type == midnightEOF {
				p.throwEOF("unexpected eof (expected '(')")
			}
			p.throw(p.cur.Line, "unexpected token '%s' (expected '(')", p.cur.Value)
		}
		// function Class.name(...), w/o self. A static `new` is a constructor
		// that builds its instance itself.
		target := midnightAttr(class(line), midnightString(name, line), line)
		fn := p.functionBody(line)
		return []ast.Stmt{midnightLine(&ast.FuncDefStmt{Name: &ast.FuncName{Func: target}, Func: fn}, line)}, name == "new"
	}

	if p.branch("=") {
		exprs := []ast.Expr{p.expression(1)}
		stmts = append(stmts, p.hoistCond(exprs, line)...)
		return append(stmts, midnightAssign(midnightAttr(class(line), midnightString(name, line), line), exprs[0], line)), false
	}

	if !p.is("(") {
		if p.cur.Type == midnightEOF {
			p.throwEOF("unexpected eof (expected '(' or '=')")
		}
		p.throw(p.cur.Line, "unexpected token '%s' (expected '(' or '=')", p.cur.Value)
	}

	if name == "new" {
		// local self = setmetatable({}, Class)
		instance := midnightLine(&ast.TableExpr{Fields: []*ast.Field{}}, line)
		create := midnightCall(midnightIdent("setmetatable", line), []ast.Expr{instance, class(line)}, line)
		self := midnightLocal([]string{"self"}, []ast.Expr{create}, line)

		fn := p.classFunction(line, []ast.Stmt{self})
		if n := len(fn.Stmts); n == 0 || !isReturn(fn.Stmts[n-1]) {
			ret := midnightLine(&ast.ReturnStmt{Exprs: []ast.Expr{midnightIdent("self", fn.LastLine())}}, fn.LastLine())
			fn.Stmts = append(fn.Stmts, ret)
		}

		target := midnightAttr(class(line), midnightString(name, line), line)
		return []ast.Stmt{midnightLine(&ast.FuncDefStmt{Name: &ast.FuncName{Func: target}, Func: fn}, line)}, true
	}

	// function Class:name(...)
	fn := p.classFunction(line, nil)
	return []ast.Stmt{midnightLine(&ast.FuncDefStmt{Name: &ast.FuncName{Receiver: class(line), Method: name}, Func: fn}, line)}, false
}

// classFunction parses the parameters and body of a class member, w/ `self`
// declared, and `setup` run before the parameter defaults.
func (p *midnightParser) classFunction(line int, setup []ast.Stmt) *ast.FunctionExpr {
	oldIsVarargs := p.isVarargs

	p.pushScope()
	defer p.popScope()
	p.declare("self", midnightScopeLocal)

	parList, prelude := p.parameters()
	p.isVarargs = parList.HasVargs
	body, lastLine := p.functionBlock()
	p.isVarargs = oldIsVarargs

	stmts := append(append(append([]ast.Stmt{}, setup...), prelude...), body...)
	fn := &ast.FunctionExpr{ParList: parList, Stmts: stmts}
	fn.SetLine(line)
	fn.SetLastLine(lastLine)
	return fn
}

func isReturn(stmt ast.Stmt) bool {
	_, ok := stmt.(*ast.ReturnStmt)
	return ok
}
