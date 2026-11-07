local
a=
{
b=
function(self,c)
return
c
+
self
.d
end
,
d=
2
,
}
return
a
:b(
-
1
)
-- Compiled with Midnight 1.0.0-1 w/ Lua target jit
-- __MIDNIGHT_COMPILED__