package lua

import (
	"testing"
)

func TestTableNewLTable(t *testing.T) {
	tbl := newLTable(-1, -2)
	errorIfNotEqual(t, 0, cap(tbl.array))

	tbl = newLTable(10, 9)
	errorIfNotEqual(t, 10, cap(tbl.array))
}

func TestTableLen(t *testing.T) {
	tbl := newLTable(0, 0)
	tbl.RawSetInt(10, LNil)
	tbl.RawSetInt(9, LNumber(10))
	tbl.RawSetInt(8, LNil)
	tbl.RawSetInt(7, LNumber(10))
	errorIfNotEqual(t, 9, tbl.Len())

	tbl = newLTable(0, 0)
	tbl.Append(LTrue)
	tbl.Append(LTrue)
	tbl.Append(LTrue)
	errorIfNotEqual(t, 3, tbl.Len())
}

func TestTableLenType(t *testing.T) {
	L := NewState(Options{})
	err := L.DoString(`
        mt = {
            __index = mt,
            __len = function (self)
                return {hello = "world"}
            end
        }

        v = {}
        v.__index = v

        setmetatable(v, mt)

        assert(#v ~= 0, "#v should return a table reference in this case")

        print(#v)
    `)
	if err != nil {
		t.Error(err)
	}
}

func TestTableAppend(t *testing.T) {
	tbl := newLTable(0, 0)
	tbl.RawSetInt(1, LNumber(1))
	tbl.RawSetInt(2, LNumber(2))
	tbl.RawSetInt(3, LNumber(3))
	errorIfNotEqual(t, 3, tbl.Len())

	tbl.RawSetInt(1, LNil)
	tbl.RawSetInt(2, LNil)
	errorIfNotEqual(t, 3, tbl.Len())

	tbl.Append(LNumber(4))
	errorIfNotEqual(t, 4, tbl.Len())

	tbl.RawSetInt(3, LNil)
	tbl.RawSetInt(4, LNil)
	errorIfNotEqual(t, 0, tbl.Len())

	tbl.Append(LNumber(5))
	errorIfNotEqual(t, 1, tbl.Len())
}

func TestTableInsert(t *testing.T) {
	tbl := newLTable(0, 0)
	tbl.Append(LTrue)
	tbl.Append(LTrue)
	tbl.Append(LTrue)

	tbl.Insert(5, LFalse)
	errorIfNotEqual(t, LFalse, tbl.RawGetInt(5))
	errorIfNotEqual(t, 5, tbl.Len())

	tbl.Insert(-10, LFalse)
	errorIfNotEqual(t, LFalse, tbl.RawGet(LNumber(-10)))
	errorIfNotEqual(t, 5, tbl.Len())

	tbl = newLTable(0, 0)
	tbl.Append(LNumber(1))
	tbl.Append(LNumber(2))
	tbl.Append(LNumber(3))
	tbl.Insert(1, LNumber(10))
	errorIfNotEqual(t, LNumber(10), tbl.RawGetInt(1))
	errorIfNotEqual(t, LNumber(1), tbl.RawGetInt(2))
	errorIfNotEqual(t, LNumber(2), tbl.RawGetInt(3))
	errorIfNotEqual(t, LNumber(3), tbl.RawGetInt(4))
	errorIfNotEqual(t, 4, tbl.Len())

	tbl = newLTable(0, 0)
	tbl.Insert(5, LNumber(10))
	errorIfNotEqual(t, LNumber(10), tbl.RawGetInt(5))

}

func TestTableMaxN(t *testing.T) {
	tbl := newLTable(0, 0)
	tbl.Append(LTrue)
	tbl.Append(LTrue)
	tbl.Append(LTrue)
	errorIfNotEqual(t, 3, tbl.MaxN())

	tbl = newLTable(0, 0)
	errorIfNotEqual(t, 0, tbl.MaxN())

	tbl = newLTable(10, 0)
	errorIfNotEqual(t, 0, tbl.MaxN())
}

func TestTableRemove(t *testing.T) {
	tbl := newLTable(0, 0)
	errorIfNotEqual(t, LNil, tbl.Remove(10))
	tbl.Append(LTrue)
	errorIfNotEqual(t, LNil, tbl.Remove(10))

	tbl.Append(LFalse)
	tbl.Append(LTrue)
	errorIfNotEqual(t, LFalse, tbl.Remove(2))
	errorIfNotEqual(t, 2, tbl.MaxN())
	tbl.Append(LFalse)
	errorIfNotEqual(t, LFalse, tbl.Remove(-1))
	errorIfNotEqual(t, 2, tbl.MaxN())

}

func TestTableRawSetInt(t *testing.T) {
	tbl := newLTable(0, 0)
	tbl.RawSetInt(MaxArrayIndex+1, LTrue)
	errorIfNotEqual(t, 0, tbl.MaxN())
	errorIfNotEqual(t, LTrue, tbl.RawGet(LNumber(MaxArrayIndex+1)))

	tbl.RawSetInt(1, LTrue)
	tbl.RawSetInt(3, LTrue)
	errorIfNotEqual(t, 3, tbl.MaxN())
	errorIfNotEqual(t, LTrue, tbl.RawGetInt(1))
	errorIfNotEqual(t, LNil, tbl.RawGetInt(2))
	errorIfNotEqual(t, LTrue, tbl.RawGetInt(3))
	tbl.RawSetInt(2, LTrue)
	errorIfNotEqual(t, LTrue, tbl.RawGetInt(1))
	errorIfNotEqual(t, LTrue, tbl.RawGetInt(2))
	errorIfNotEqual(t, LTrue, tbl.RawGetInt(3))
}

func TestTableRawSetH(t *testing.T) {
	tbl := newLTable(0, 0)
	tbl.RawSetH(LString("key"), LTrue)
	tbl.RawSetH(LString("key"), LNil)
	_, found := tbl.dict[LString("key")]
	errorIfNotEqual(t, false, found)

	tbl.RawSetH(LTrue, LTrue)
	tbl.RawSetH(LTrue, LNil)
	_, foundb := tbl.dict[LTrue]
	errorIfNotEqual(t, false, foundb)
}

func TestTableRawGetH(t *testing.T) {
	tbl := newLTable(0, 0)
	errorIfNotEqual(t, LNil, tbl.RawGetH(LNumber(1)))
	errorIfNotEqual(t, LNil, tbl.RawGetH(LString("key0")))
	tbl.RawSetH(LString("key0"), LTrue)
	tbl.RawSetH(LString("key1"), LFalse)
	tbl.RawSetH(LNumber(1), LTrue)
	errorIfNotEqual(t, LTrue, tbl.RawGetH(LString("key0")))
	errorIfNotEqual(t, LTrue, tbl.RawGetH(LNumber(1)))
	errorIfNotEqual(t, LNil, tbl.RawGetH(LString("notexist")))
	errorIfNotEqual(t, LNil, tbl.RawGetH(LTrue))
}

func TestTableForEach(t *testing.T) {
	tbl := newLTable(0, 0)
	tbl.Append(LNumber(1))
	tbl.Append(LNumber(2))
	tbl.Append(LNumber(3))
	tbl.Append(LNil)
	tbl.Append(LNumber(5))

	tbl.RawSetH(LString("a"), LString("a"))
	tbl.RawSetH(LString("b"), LString("b"))
	tbl.RawSetH(LString("c"), LString("c"))

	tbl.RawSetH(LTrue, LString("true"))
	tbl.RawSetH(LFalse, LString("false"))

	tbl.ForEach(func(key, value LValue) {
		switch k := key.(type) {
		case LBool:
			switch bool(k) {
			case true:
				errorIfNotEqual(t, LString("true"), value)
			case false:
				errorIfNotEqual(t, LString("false"), value)
			default:
				t.Fail()
			}
		case LNumber:
			switch int(k) {
			case 1:
				errorIfNotEqual(t, LNumber(1), value)
			case 2:
				errorIfNotEqual(t, LNumber(2), value)
			case 3:
				errorIfNotEqual(t, LNumber(3), value)
			case 4:
				errorIfNotEqual(t, LNumber(5), value)
			default:
				t.Fail()
			}
		case LString:
			switch string(k) {
			case "a":
				errorIfNotEqual(t, LString("a"), value)
			case "b":
				errorIfNotEqual(t, LString("b"), value)
			case "c":
				errorIfNotEqual(t, LString("c"), value)
			default:
				t.Fail()
			}
		}
	})
}

func TestTableConcat(t *testing.T) {
	L := NewState()
	defer L.Close()
	err := L.DoString(`
		assert(table.concat({}) == "")
		assert(table.concat({"a", "b", "c"}) == "abc")
		assert(table.concat({"a", "b", "c"}, ", ") == "a, b, c")
		assert(table.concat({1, 2.5, "x"}, "-") == "1-2.5-x")
		assert(table.concat({"a", "b", "c", "d"}, "", 2, 3) == "bc")
		assert(table.concat({"a", "b"}, ",", 3) == "")
		assert(table.concat({"a", "b"}, ",", 2, 1) == "")

		-- more pieces than the registry holds (it used to overflow)
		local big = {}
		for i = 1, 100000 do big[i] = "x" end
		local s = table.concat(big)
		assert(#s == 100000)
		assert(#table.concat(big, ",") == 199999)

		local ok, msg = pcall(table.concat, {"a", {}, "c"})
		assert(not ok and msg:find("invalid value %(table%) at index 2"))
	`)
	if err != nil {
		t.Fatal(err)
	}
}

// pairs iterates hash keys in the order they were added (rest's json.encode
// relies on it), w/ deleted keys keeping their place (see indexKeys).
func TestTableKeyOrder(t *testing.T) {
	L := NewState()
	defer L.Close()
	err := L.DoString(`
		local function keys(t)
			local out = {}
			for k in pairs(t) do out[#out + 1] = tostring(k) end
			return table.concat(out, ',')
		end

		local t = {}
		t.c = 1; t.a = 2; t.b = 3
		assert(keys(t) == 'c,a,b', keys(t))

		-- re-setting a key doesn't move it
		t.a = 20
		assert(keys(t) == 'c,a,b', keys(t))

		-- a deleted key keeps its place when it's added back
		t.a = nil
		assert(keys(t) == 'c,b', keys(t))
		t.d = 4; t.a = 5
		assert(keys(t) == 'c,a,b,d', keys(t))

		-- deleting before any iteration
		local u = {}
		u.x = 1; u.y = 2; u.x = nil; u.z = 3; u.x = 4
		assert(keys(u) == 'x,y,z', keys(u))

		-- non-string keys, and the array part first
		local k1, k2 = {}, {}
		local m = { 'one', 'two' }
		m[k1] = 1; m.s = 2; m[2.5] = 3; m[k2] = 4
		local seen = {}
		for k, v in pairs(m) do seen[#seen + 1] = type(k) .. ':' .. tostring(v) end
		assert(table.concat(seen, ',') == 'number:one,number:two,table:1,string:2,number:3,table:4', table.concat(seen, ','))

		-- assigning existing fields while iterating (allowed in Lua)
		local w = { a = 1, b = 2, c = 3 }
		local n = 0
		for k, v in pairs(w) do w[k] = v * 10; n = n + 1 end
		assert(n == 3 and w.a == 10 and w.b == 20 and w.c == 30)

		-- clearing fields while iterating (also allowed)
		local cnt = 0
		for k in pairs(w) do w[k] = nil; cnt = cnt + 1 end
		assert(cnt == 3 and next(w) == nil)

		-- big tables still work
		local big = {}
		for i = 1, 1000 do big['k' .. i] = i end
		local total = 0
		for _, v in pairs(big) do total = total + v end
		assert(total == 500500)
	`)
	if err != nil {
		t.Fatal(err)
	}
}
