// Package luaconv converts between Go and lua values (as midnight states see
// them): plain values, json, and response cookies.
package luaconv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	lua "github.com/taybart/midnight"
)

func ToGo(value lua.LValue) any {
	switch v := value.(type) {
	case lua.LString:
		return string(v)
	case lua.LNumber:
		return float64(v)
	case lua.LBool:
		return bool(v)
	case *lua.LTable:
		return tableToGo(v)
	case *lua.LNilType:
		return nil
	default:
		return lua.LVAsString(v)
	}
}

func tableToGo(table *lua.LTable) any {
	maxn := table.MaxN()
	count := 0
	isSeq := true
	table.ForEach(func(key, _ lua.LValue) {
		count++
		if num, ok := key.(lua.LNumber); ok {
			idx := int(num)
			if float64(idx) != float64(num) || idx < 1 || idx > maxn {
				isSeq = false
			}
		} else {
			isSeq = false
		}
	})
	if isSeq && count == maxn {
		arr := make([]any, maxn)
		for i := 1; i <= maxn; i++ {
			arr[i-1] = ToGo(table.RawGetInt(i))
		}
		return arr
	}
	return TableToMap(table)
}

func TableToMap(table *lua.LTable) map[string]any {
	result := make(map[string]any)

	table.ForEach(func(key, value lua.LValue) {
		keyStr := lua.LVAsString(key)
		result[keyStr] = ToGo(value)
	})

	return result
}

func FromGo(state *lua.LState, value any) lua.LValue {
	switch v := value.(type) {
	case string:
		return lua.LString(v)
	case float64:
		return lua.LNumber(v)
	case int:
		return lua.LNumber(v)
	case bool:
		return lua.LBool(v)
	case []any:
		// sized exactly: NewTable preallocates room for 32 items and 32
		// keys (~2KB), a lot for a decoded json object with a few fields
		tbl := state.CreateTable(len(v), 0)
		for i, item := range v {
			tbl.RawSetInt(i+1, FromGo(state, item))
		}
		return tbl
	case map[string]any:
		return MapToTable(state, v)
	case nil:
		return lua.LNil
	default:
		return lua.LString(fmt.Sprint(v))
	}
}

func MapToTable(state *lua.LState, data map[string]any) *lua.LTable {
	table := state.CreateTable(0, len(data))

	for key, value := range data {
		lval := FromGo(state, value)
		table.RawSetString(key, lval)
	}

	return table
}

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// jsonObject is a JSON object that keeps its keys' order.
type jsonObject []jsonField

type jsonField struct {
	key   string
	value any
}

func (o jsonObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := marshalJSON(f.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := marshalJSON(f.value)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// EncodeJSON encodes a lua value like json.encode (objects keep their keys'
// order, no HTML escaping).
func EncodeJSON(v lua.LValue) ([]byte, error) {
	goVal, err := luaToJSONValue(v, make(map[*lua.LTable]bool))
	if err != nil {
		return nil, err
	}
	return marshalJSON(goVal)
}

func luaToJSONValue(val lua.LValue, visited map[*lua.LTable]bool) (any, error) {
	switch v := val.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(v), nil
	case lua.LString:
		return string(v), nil
	case lua.LNumber:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("unexpected number value '%v'", v)
		}
		return f, nil
	case *lua.LTable:
		if visited[v] {
			return nil, fmt.Errorf("circular reference")
		}
		visited[v] = true
		defer delete(visited, v)

		maxn := v.MaxN()
		count := 0
		hasNum := false
		hasStr := false
		invalidKey := false
		isSeq := true

		v.ForEach(func(key, _ lua.LValue) {
			count++
			switch k := key.(type) {
			case lua.LNumber:
				hasNum = true
				idx := int(k)
				if float64(idx) != float64(k) || idx < 1 || idx > maxn {
					isSeq = false
				}
			case lua.LString:
				hasStr = true
				isSeq = false
			default:
				invalidKey = true
				isSeq = false
			}
		})

		if invalidKey || (hasNum && hasStr) {
			return nil, fmt.Errorf("invalid table: mixed or invalid key types")
		}
		if hasNum && (!isSeq || count != maxn) {
			return nil, fmt.Errorf("invalid table: sparse or non-sequential array")
		}
		if count == 0 || isSeq {
			arr := make([]any, maxn)
			for i := 1; i <= maxn; i++ {
				elem, err := luaToJSONValue(v.RawGetInt(i), visited)
				if err != nil {
					return nil, err
				}
				arr[i-1] = elem
			}
			return arr, nil
		}

		// (in the order the keys were added, like JSON.stringify)
		obj := make(jsonObject, 0, count)
		for key, value := v.Next(lua.LNil); key != lua.LNil; key, value = v.Next(key) {
			elem, err := luaToJSONValue(value, visited)
			if err != nil {
				return nil, err
			}
			obj = append(obj, jsonField{string(key.(lua.LString)), elem})
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("unexpected type '%s'", val.Type().String())
	}
}

func parseSameSite(s string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "default":
		return http.SameSiteDefaultMode
	case "lax":
		return http.SameSiteLaxMode
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return 0
	}
}

// Cookies converts a Lua cookies table (either map of name -> string/table
// or array of cookie tables) into a slice of *http.Cookie.
func Cookies(tbl *lua.LTable) []*http.Cookie {
	var cookies []*http.Cookie
	tbl.ForEach(func(k, v lua.LValue) {
		switch val := v.(type) {
		case *lua.LTable:
			c := &http.Cookie{}
			if k.Type() == lua.LTString {
				c.Name = k.String()
			}
			val.ForEach(func(ck, cv lua.LValue) {
				switch strings.ToLower(ck.String()) {
				case "name":
					c.Name = cv.String()
				case "value":
					c.Value = cv.String()
				case "path":
					c.Path = cv.String()
				case "domain":
					c.Domain = cv.String()
				case "max_age", "maxage":
					if n, ok := cv.(lua.LNumber); ok {
						c.MaxAge = int(n)
					}
				case "secure":
					c.Secure = lua.LVAsBool(cv)
				case "http_only", "httponly":
					c.HttpOnly = lua.LVAsBool(cv)
				case "same_site", "samesite":
					c.SameSite = parseSameSite(cv.String())
				}
			})
			if c.Name != "" {
				cookies = append(cookies, c)
			}
		case *lua.LNilType:
			// skip nil
		default:
			if k.Type() == lua.LTString {
				cookies = append(cookies, &http.Cookie{
					Name:  k.String(),
					Value: val.String(),
				})
			}
		}
	})
	return cookies
}
