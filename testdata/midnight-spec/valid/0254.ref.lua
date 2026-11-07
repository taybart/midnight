local
function
a
()
return
1
end
do
function
_G.a
()
return
2
end
end
local
result=
a(
)
_G
.a
=
nil
return
result
-- Compiled with Midnight 1.0.0-1 w/ Lua target jit
-- __MIDNIGHT_COMPILED__