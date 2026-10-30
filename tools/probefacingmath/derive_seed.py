import pathlib,struct,decimal,math,sys
D=decimal.Decimal;decimal.getcontext().prec=100
bits=lambda x:struct.unpack('Q',struct.pack('d',x))[0]
val=lambda x:struct.unpack('d',struct.pack('Q',x))[0]
rows=[x.split('\t') for x in pathlib.Path(sys.argv[1]).read_text().splitlines() if x.startswith('inv\t')]
target=float.fromhex(next(a[2] for a in rows if float.fromhex(a[1])==1));t=D.from_float(target)
for guess in [D('.96'),D('1.04')]:
 y=guess
 for _ in range(50):y-=(y*(D('1.5')-D('.5')*y*y)-t)/(D('1.5')-D('1.5')*y*y)
 k0=bits(float(y))+(bits(1.)>>1)
 for k in range(k0-128,k0+129):
  count=0
  for a in rows:
   x=float.fromhex(a[1]);want=float.fromhex(a[2]);seed=val(k-(bits(x)>>1));actual=seed*(1.5-(.5*x)*seed*seed)
   if bits(actual)==bits(want):count+=1
  if count==len(rows):print('inverse-root seed constant from public observations',hex(k),'matches',count)
