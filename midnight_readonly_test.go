package lua

import (
	"strings"
	"sync"
	"testing"
)

func frozenData(L *LState) *LTable {
	if err := L.DoString(`return { a = 1, list = { 10, 20, 30 }, nested = { deep = { x = 'y' } } }`); err != nil {
		panic(err)
	}
	t := L.Get(-1).(*LTable)
	L.Pop(1)
	t.FreezeAll()
	return t
}

func TestReadOnlyTables(t *testing.T) {
	L := NewState()
	defer L.Close()
	L.SetGlobal("t", frozenData(L))

	writes := []string{
		`t.a = 2`,
		`t.b = 2`,
		`t.nested.deep.x = 'z'`,
		`t.list[4] = 40`,
		`rawset(t, 'b', 2)`,
		`table.insert(t.list, 40)`,
		`table.remove(t.list)`,
		`table.sort(t.list, function(a, b) return a > b end)`,
		`setmetatable(t, {})`,
	}
	for _, code := range writes {
		err := L.DoString(code)
		if err == nil || !strings.Contains(err.Error(), "read-only table") {
			t.Errorf("%s: got %v", code, err)
		}
	}

	reads := `
		local n = 0
		for k, v in pairs(t) do n = n + 1 end
		local sum = 0
		for _, v in ipairs(t.list) do sum = sum + v end
		assert(n == 3 and sum == 60 and #t.list == 3 and t.nested.deep.x == 'y', 'reads')
		assert(t.a == 1, 'unchanged')
		local copy = {}
		for k, v in pairs(t) do copy[k] = v end -- copies are writable
		copy.a = 2`
	if err := L.DoString(reads); err != nil {
		t.Fatal(err)
	}
}

// A frozen table can be read by states in different goroutines.
func TestReadOnlyTablesShared(t *testing.T) {
	owner := NewState()
	defer owner.Close()
	shared := frozenData(owner)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			L := NewState()
			defer L.Close()
			L.SetGlobal("t", shared)
			err := L.DoString(`
				for i = 1, 200 do
					local n = 0
					for k, v in pairs(t) do n = n + 1 end
					for _, v in ipairs(t.list) do n = n + v end
					assert(n == 63 and t.nested.deep.x == 'y')
				end`)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
