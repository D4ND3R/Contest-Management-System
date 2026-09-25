program sol;
var p: PByte; n: PtrUInt;
begin
  n := 1024 * 1024 * 1024;
  GetMem(p, n);
  FillChar(p^, n, 7);
  writeln(p[n - 1]);
end.
