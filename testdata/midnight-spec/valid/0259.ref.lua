local
a=
{
b=
1
,
}
function
a:c
()
return
self
.b
end
return
a
:c(
)
-- Compiled with Midnight 1.0.0-1 w/ Lua target jit
-- __MIDNIGHT_COMPILED__