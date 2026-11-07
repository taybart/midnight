-- Compiles captured snippets w/ the reference compiler into the testdata dir.
-- usage: lua compile.lua <capture dir> <testdata dir>
--
-- Valid snippets are compiled for LuaJIT (which, like gopher-lua, is Lua 5.1 w/
-- goto) w/ bit operators as `bit32` calls, which matches what midnight does.
local compile = require('erde.compile')
local capture_dir, testdata_dir = arg[1], arg[2]

local function read(path)
  local f = assert(io.open(path))
  local content = f:read('*a')
  f:close()
  return content
end

local function write(path, content)
  local f = assert(io.open(path, 'w'))
  f:write(content)
  f:close()
end

local valid, invalid, skipped = 0, 0, 0

for path in io.popen('ls ' .. capture_dir .. '/*.mn'):lines() do
  local id, status = path:match('(%d+)%.(%a+)%.mn$')
  local source = read(path)

  if status == 'ok' then
    local ok, lua = pcall(compile, source, { lua_target = 'jit', bitexpr = 'bit32', alias = 'snippet' })
    if ok then
      write(('%s/valid/%s.mn'):format(testdata_dir, id), source)
      write(('%s/valid/%s.ref.lua'):format(testdata_dir, id), lua)
      valid = valid + 1
    else
      skipped = skipped + 1 -- valid for other targets, but not LuaJIT
    end
  else
    local reason = read(capture_dir .. '/' .. id .. '.err.txt')
    -- Lua target restrictions don't apply to midnight
    if reason:find('compatib') then
      skipped = skipped + 1
    else
      write(('%s/invalid/%s.mn'):format(testdata_dir, id), source)
      write(('%s/invalid/%s.txt'):format(testdata_dir, id), reason)
      invalid = invalid + 1
    end
  end
end

print(('valid: %d, invalid: %d, skipped: %d'):format(valid, invalid, skipped))
