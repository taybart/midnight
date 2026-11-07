local
a=
{
b=
3
,
}
a['end']
=
function
(self)
return
self
.b
end
return
a
['end'](
a
)
-- Compiled with Midnight 1.0.0-1 w/ Lua target jit
-- __MIDNIGHT_COMPILED__