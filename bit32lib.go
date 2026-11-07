package lua

import (
	"math"
)

// Lua 5.2's bit32 library. Midnight bit operators (`|`, `~`, `&`, `<<`, `>>`) are
// compiled to calls into this library.

// Bit32LibName is the name of the bit32 Library.
const Bit32LibName = "bit32"

func OpenBit32(L *LState) int {
	mod := L.RegisterModule(Bit32LibName, bit32Funcs)
	L.Push(mod)
	return 1
}

var bit32Funcs = map[string]LGFunction{
	"band":   bit32Band,
	"bnot":   bit32Bnot,
	"bor":    bit32Bor,
	"bxor":   bit32Bxor,
	"lshift": bit32Lshift,
	"rshift": bit32Rshift,
}

func bit32Arg(L *LState, n int) uint32 {
	v := math.Mod(math.Floor(float64(L.CheckNumber(n))), 4294967296)
	if v < 0 {
		v += 4294967296
	}
	return uint32(v)
}

func bit32Fold(L *LState, init uint32, op func(a, b uint32) uint32) int {
	result := init
	for i := 1; i <= L.GetTop(); i++ {
		result = op(result, bit32Arg(L, i))
	}
	L.Push(LNumber(result))
	return 1
}

func bit32Band(L *LState) int {
	return bit32Fold(L, math.MaxUint32, func(a, b uint32) uint32 { return a & b })
}

func bit32Bor(L *LState) int {
	return bit32Fold(L, 0, func(a, b uint32) uint32 { return a | b })
}

func bit32Bxor(L *LState) int {
	return bit32Fold(L, 0, func(a, b uint32) uint32 { return a ^ b })
}

func bit32Bnot(L *LState) int {
	L.Push(LNumber(^bit32Arg(L, 1)))
	return 1
}

func bit32Shift(x uint32, disp int) uint32 {
	switch {
	case disp >= 32 || disp <= -32:
		return 0
	case disp >= 0:
		return x << uint(disp)
	default:
		return x >> uint(-disp)
	}
}

func bit32Lshift(L *LState) int {
	L.Push(LNumber(bit32Shift(bit32Arg(L, 1), L.CheckInt(2))))
	return 1
}

func bit32Rshift(L *LState) int {
	L.Push(LNumber(bit32Shift(bit32Arg(L, 1), -L.CheckInt(2))))
	return 1
}
