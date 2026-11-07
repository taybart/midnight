local
a=
{
b=
1
,
}
a
.c
=
function(self,d)
return
self
.b
+
d
end
return
a
:c(
1
)
-- Compiled with Midnight 1.0.0-1 w/ Lua target jit
-- __MIDNIGHT_COMPILED__