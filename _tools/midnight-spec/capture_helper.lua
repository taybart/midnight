-- busted helper: wraps erde.compile to record every source the reference
-- compiler's spec suite compiles, and whether it was accepted.
-- Output dir: $MIDNIGHT_SPEC_CAPTURE_DIR
local out_dir = assert(os.getenv('MIDNIGHT_SPEC_CAPTURE_DIR'), 'MIDNIGHT_SPEC_CAPTURE_DIR not set')
local real_compile = require('erde.compile')
local counter = 0
local seen = {}

package.loaded['erde.compile'] = function(source, options)
  local results = table.pack(pcall(real_compile, source, options))
  local ok, result = results[1], results[2]

  if not seen[source] then
    seen[source] = true
    counter = counter + 1

    local base = ('%s/%04d'):format(out_dir, counter)
    local f = assert(io.open(base .. (ok and '.ok.mn' or '.err.mn'), 'w'))
    f:write(source)
    f:close()

    if not ok then
      local e = assert(io.open(base .. '.err.txt', 'w'))
      e:write(tostring(result))
      e:close()
    end
  end

  if not ok then error(result, 0) end
  return (table.unpack or unpack)(results, 2, results.n)
end

dofile('spec/setup_spec.lua')
