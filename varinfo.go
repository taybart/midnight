package lua

import (
	"fmt"
	"strings"
)

// midnight: errors name the variable a bad value came from, like PUC Lua's
// varinfo: "attempt to call a nil value (local 'f')", "attempt to index a nil
// value (field 'repo') with key 'name'".

// callInfo describes fn, if the running instruction is a call of it.
func (ls *LState) callInfo(fn LValue) string {
	cf := ls.currentFrame
	inst, ok := runningInst(cf)
	if !ok {
		return ""
	}
	switch opGetOpCode(inst) {
	case OP_CALL, OP_TAILCALL:
		return ls.regInfo(cf, opGetArgA(inst), fn)
	}
	return ""
}

// indexInfo describes obj, if the running instruction indexes it.
func (ls *LState) indexInfo(obj LValue) string {
	cf := ls.currentFrame
	inst, ok := runningInst(cf)
	if !ok {
		return ""
	}
	switch opGetOpCode(inst) {
	case OP_GETTABLE, OP_GETTABLEKS, OP_SELF:
		return ls.regInfo(cf, opGetArgB(inst), obj)
	case OP_SETTABLE, OP_SETTABLEKS:
		return ls.regInfo(cf, opGetArgA(inst), obj)
	}
	return ""
}

// runningInst returns the instruction a lua frame is running (its pc has
// already moved past it).
func runningInst(cf *callFrame) (uint32, bool) {
	if cf == nil || cf.Fn == nil || cf.Fn.IsG || cf.Pc < 1 || cf.Pc > len(cf.Fn.Proto.Code) {
		return 0, false
	}
	return cf.Fn.Proto.Code[cf.Pc-1], true
}

// regInfo describes register reg of cf, if it holds v (so a value that came
// from somewhere else, ex. through a metamethod, isn't misnamed).
func (ls *LState) regInfo(cf *callFrame, reg int, v LValue) string {
	if ls.reg.Get(cf.LocalBase+reg) != v {
		return ""
	}
	kind, name := objName(cf.Fn, cf.Pc-1, reg)
	if kind == "" {
		return ""
	}
	return fmt.Sprintf(" (%s '%s')", kind, name)
}

// objName returns what register reg holds at pc: a local, global, field,
// method, or upvalue, and its name ("" if it's a temporary w/o one).
func objName(fn *LFunction, pc, reg int) (string, string) {
	if name, ok := fn.LocalName(reg+1, pc); ok && !internalName(name) {
		return "local", name
	}
	// (an internal local is a temporary: describe what was put in it)
	p := fn.Proto
	setter := findSetReg(p, pc, reg)
	if setter < 0 {
		return "", ""
	}
	inst := p.Code[setter]
	a, b, c := opGetArgA(inst), opGetArgB(inst), opGetArgC(inst)
	switch opGetOpCode(inst) {
	case OP_MOVE:
		if b < a {
			return objName(fn, setter, b)
		}
	case OP_GETGLOBAL:
		if s, ok := p.Constants[opGetArgBx(inst)].(LString); ok {
			return "global", string(s)
		}
	case OP_GETTABLE, OP_GETTABLEKS:
		if name, ok := constantName(p, c); ok {
			return "field", name
		}
	case OP_SELF:
		if name, ok := constantName(p, c); ok {
			return "method", name
		}
	case OP_GETUPVAL:
		if b < len(p.DbgUpvalues) {
			return "upvalue", p.DbgUpvalues[b]
		}
	}
	return "", ""
}

// internalName returns whether a local is the compiler's, not the
// program's: lua's (ex. "(for index)"), and midnight's temporaries ("(midnight
// 1)", or "__MIDNIGHT_TMP_1__" in compiled lua).
func internalName(name string) bool {
	return strings.HasPrefix(name, "(") || strings.HasPrefix(name, "__MIDNIGHT_TMP_")
}

// constantName returns the string constant an RK operand is, if it is one.
func constantName(p *FunctionProto, rk int) (string, bool) {
	if !opIsK(rk) {
		return "", false
	}
	s, ok := p.Constants[opIndexK(rk)].(LString)
	return string(s), ok
}

// findSetReg returns the pc of the last instruction before lastpc that set
// register reg, or -1 (also if it was set in code that was jumped over, so
// it's unknown which instruction set it).
func findSetReg(p *FunctionProto, lastpc, reg int) int {
	setreg, jmptarget := -1, 0
	for pc := 0; pc < lastpc; pc++ {
		inst := p.Code[pc]
		op, a := opGetOpCode(inst), opGetArgA(inst)
		var change bool
		switch op {
		case OP_LOADNIL:
			change = a <= reg && reg <= opGetArgB(inst)
		case OP_SELF:
			change = reg == a || reg == a+1
		case OP_TFORLOOP:
			change = reg >= a+2
		case OP_CALL, OP_TAILCALL, OP_VARARG:
			change = reg >= a
		case OP_JMP:
			dest := pc + 1 + opGetArgSbx(inst)
			if pc < dest && dest <= lastpc && dest > jmptarget {
				jmptarget = dest
			}
		default:
			change = op <= opCodeMax && opProps[op].SetRegA && reg == a
		}
		if change {
			if pc < jmptarget {
				setreg = -1
			} else {
				setreg = pc
			}
		}
	}
	return setreg
}
