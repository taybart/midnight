local
a=
function(...)
local b = { ... }
return
b
end
return
a(
5
,
6
)
-- Compiled with Midnight 1.0.0-1 w/ Lua target jit
-- __MIDNIGHT_COMPILED__