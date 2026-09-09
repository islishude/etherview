"""Diagnostic Keccak-256 replacement, NOT an approved production dependency.
P30-T100 isolates the remaining Vyper/WASI compatibility from ctypes.
"""
_MASK = (1 << 64) - 1
_RC = (0x1, 0x8082, 0x800000000000808A, 0x8000000080008000, 0x808B,
       0x80000001, 0x8000000080008081, 0x8000000000008009, 0x8A, 0x88,
       0x80008009, 0x8000000A, 0x8000808B, 0x800000000000008B,
       0x8000000000008089, 0x8000000000008003, 0x8000000000008002,
       0x8000000000000080, 0x800A, 0x800000008000000A,
       0x8000000080008081, 0x8000000000008080, 0x80000001, 0x8000000080008008)
_ROT = ((0,36,3,41,18),(1,44,10,45,2),(62,6,43,15,61),(28,55,25,21,56),(27,20,39,8,14))
def _rol(x, n):
    return ((x << n) | (x >> (64-n))) & _MASK

def digest(data):
    data = bytearray(data)
    data.append(1)
    data.extend(b'\0' * ((-len(data)) % 136))
    data[-1] |= 128
    a = [0] * 25
    for offset in range(0,len(data),136):
        for i in range(17):
            a[i] ^= int.from_bytes(data[offset+8*i:offset+8*i+8], 'little')
        for rc in _RC:
            c = [a[x]^a[x+5]^a[x+10]^a[x+15]^a[x+20] for x in range(5)]
            d = [c[(x-1)%5]^_rol(c[(x+1)%5],1) for x in range(5)]
            b = [0]*25
            for x in range(5):
                for y in range(5):
                    b[y+5*((2*x+3*y)%5)] = _rol(a[x+5*y]^d[x],_ROT[x][y])
            for x in range(5):
                for y in range(5):
                    a[x+5*y] = b[x+5*y]^((~b[(x+1)%5+5*y])&b[(x+2)%5+5*y])
            a[0] ^= rc
    return b''.join(v.to_bytes(8,'little') for v in a)[:32]

class _Hash:
    def __init__(self, data): self.data = data
    def digest(self): return digest(self.data)

def new(*, digest_bits, data=b''):
    assert digest_bits == 256
    return _Hash(data)
