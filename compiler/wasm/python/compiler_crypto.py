"""Narrow PyCryptodome API adapter for Vyper; all hashing uses the Go host."""
import _etherview_keccak

class _Keccak256:
    def __init__(self, data):
        self._digest = _etherview_keccak.keccak256(data)

    def digest(self):
        return self._digest

def new(*, digest_bits, data=b""):
    if digest_bits != 256:
        raise ValueError("unsupported compiler hash")
    return _Keccak256(data)
