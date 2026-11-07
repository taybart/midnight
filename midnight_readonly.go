package lua

// midnight: read-only tables. A frozen table can't be changed (from lua, an
// error; from Go, a panic). Since reading a table never changes it, a frozen
// table (w/ frozen tables in it) can be shared by states running in different
// goroutines, ex. a host's cached data, decoded once.

const errReadOnlyTable = "attempt to modify a read-only table"

// Freeze makes the table read-only (not the tables in it, see FreezeAll). It
// can't have a metatable set afterwards either.
func (tb *LTable) Freeze() { tb.readOnly = true }

// FreezeAll freezes the table and every table in it (keys and values).
func (tb *LTable) FreezeAll() {
	if tb.readOnly {
		return
	}
	tb.readOnly = true
	freeze := func(v LValue) {
		if t, ok := v.(*LTable); ok {
			t.FreezeAll()
		}
	}
	tb.ForEach(func(k, v LValue) {
		freeze(k)
		freeze(v)
	})
}

// IsReadOnly returns whether the table was frozen.
func (tb *LTable) IsReadOnly() bool { return tb.readOnly }

func (tb *LTable) checkWritable() {
	if tb.readOnly {
		panic(errReadOnlyTable)
	}
}
