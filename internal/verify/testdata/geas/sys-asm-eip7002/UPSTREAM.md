# ethereum/sys-asm EIP-7002 fixture

- Repository: https://github.com/ethereum/sys-asm
- Commit: `83f9801245ff56878a450b5625801101b9a225a1`
- Compiler: `github.com/fjl/geas` v0.3.3
- Contract: EIP-7002 withdrawals system contract
- License: MIT (see the upstream repository)

The sources are copied without modification; the hexadecimal bytecode payloads
retain the exact upstream content with a conventional trailing newline. Tests
compile both entrypoints from the inline virtual source bundle and compare them
with the pinned upstream bytecode. No network access is used.
