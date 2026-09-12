"""Pinned trusted compiler protocol; contract source is data, never Python code."""
import json
import os
from pathlib import Path
import resource
import socket
import subprocess
import sys

SCHEMA = "etherview-vyper-runtime-v2"


def invocation():
    args = sys.argv[1:]
    if args == ["--self-test"]:
        return args[0], 0, 0
    if len(args) != 3 or args[0] != "--compile":
        raise ValueError("invalid compiler invocation")
    bounds = []
    for raw in args[1:]:
        if not raw.isascii() or not raw.isdecimal() or len(raw) > 20:
            raise ValueError("invalid compiler limit")
        value = int(raw)
        if value <= 0 or value >= sys.maxsize or str(value) != raw:
            raise ValueError("invalid compiler limit")
        bounds.append(value)
    return args[0], bounds[0], bounds[1]


def read_input(max_input):
    chunks = []
    remaining = max_input + 1
    while remaining:
        chunk = sys.stdin.buffer.read(min(64 << 10, remaining))
        if not chunk:
            break
        chunks.append(chunk)
        remaining -= len(chunk)
    raw = b"".join(chunks)
    if len(raw) > max_input:
        raise ValueError("compiler input exceeds limit")
    return raw


def limits():
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_NOFILE, (64, 64))
    if sys.platform == "linux":
        resource.setrlimit(resource.RLIMIT_AS, (512 << 20, 512 << 20))


def guard(root, manifest):
    allowed = {str(root / item["path"]) for item in manifest["files"]}

    def audit(event, args):
        if event == "open":
            path, mode, flags = args
            if isinstance(path, int):
                if path != 0:
                    raise PermissionError("compiler access denied")
                return
            if flags & (os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC | os.O_APPEND):
                raise PermissionError("compiler access denied")
            if str(Path(os.fsdecode(path)).resolve()) not in allowed:
                raise PermissionError("compiler access denied")
        if event == "ctypes.dlsym":
            library, symbol = args
            path = Path(getattr(library, "_name", "")).resolve()
            if str(path) not in allowed or path.name != "_keccak.abi3.so" or symbol not in {
                "keccak_init", "keccak_destroy", "keccak_absorb", "keccak_squeeze",
                "keccak_digest", "keccak_copy", "keccak_reset",
            }:
                raise PermissionError("compiler access denied")
        if event.startswith(("socket.", "subprocess.")) or event in {
            "ctypes.dlopen",
            "os.system", "os.exec", "os.fork", "os.forkpty", "os.posix_spawn",
            "os.remove", "os.rename", "os.mkdir", "os.rmdir", "os.chmod",
            "os.chown", "os.link", "os.symlink", "os.truncate",
        }:
            raise PermissionError("compiler access denied")

    sys.addaudithook(audit)


def canonical_path(name):
    return (isinstance(name, str) and 0 < len(name) <= 384 and
            not name.startswith("/") and "\\" not in name and
            all(part not in ("", ".", "..") for part in name.split("/")) and
            not any(ord(c) < 32 or ord(c) == 127 for c in name))


def document(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result


def validate_input(value):
    if not isinstance(value, dict) or set(value) - {"language", "sources", "interfaces", "settings"}:
        raise ValueError("invalid compiler input")
    if value["language"] != "Vyper":
        raise ValueError("invalid compiler language")
    sources = value["sources"]
    if not isinstance(sources, dict) or not 1 <= len(sources) <= 1024:
        raise ValueError("invalid compiler sources")
    interfaces = value.get("interfaces", {})
    if not isinstance(interfaces, dict) or len(sources) + len(interfaces) > 1024 or sources.keys() & interfaces.keys():
        raise ValueError("invalid compiler interfaces")
    for name, entry in {**sources, **interfaces}.items():
        if not canonical_path(name) or not isinstance(entry, dict):
            raise ValueError("invalid compiler source")
        if set(entry) == {"content"} and isinstance(entry["content"], str):
            continue
        if name in interfaces and set(entry) == {"abi"} and isinstance(entry["abi"], list):
            continue
        raise ValueError("invalid compiler source")
    settings = value["settings"]
    if not isinstance(settings, dict) or set(settings) - {
        "evmVersion", "optimize", "bytecodeMetadata", "enable_decimals", "outputSelection", "search_paths"
    }:
        raise ValueError("unsupported compiler settings")
    if settings.get("search_paths") != ["."]:
        raise ValueError("invalid compiler search paths")
    if "optimize" in settings and settings["optimize"] not in ("none", "gas", "codesize", True, False):
        raise ValueError("invalid compiler optimization")
    selected = settings.get("outputSelection")
    if not isinstance(selected, dict) or len(selected) != 1:
        raise ValueError("invalid compiler target")
    target, outputs = next(iter(selected.items()))
    required = ["abi", "metadata", "layout", "evm.bytecode.object", "evm.deployedBytecode.object", "evm.methodIdentifiers", "userdoc", "devdoc"]
    if target not in sources or "content" not in sources[target] or outputs != required:
        raise ValueError("invalid compiler outputs")


def denied(operation):
    try:
        operation()
    except PermissionError:
        return True
    return False


def main():
    mode, max_input, max_output = invocation()
    if not getattr(sys, "frozen", False):
        raise ValueError("invalid compiler runtime")
    limits()
    import vyper
    from adapter import compile_input
    # Initialize the trusted native hash implementation before denying dlopen.
    from vyper.utils import keccak256
    keccak256(b"self-test")
    root = Path(sys.executable).resolve().parent
    manifest = json.loads((root / "runtime-manifest.json").read_text())
    if manifest["schema"] not in (SCHEMA, "etherview-vyper-runtime-v3") or manifest["vyper"] != vyper.__version__ or manifest["python"] != ".".join(map(str, sys.version_info[:3])):
        raise ValueError("invalid compiler manifest")
    guard(root, manifest)
    if mode == "--self-test":
        if sys.platform == "linux":
            try:
                bytearray(513 << 20)
            except MemoryError:
                pass
            else:
                raise ValueError("compiler memory limit self-test failed")
        checks = [
            denied(lambda: open("/etc/passwd", "rb")),
            denied(lambda: open("forbidden-write", "w")),
            denied(lambda: socket.socket()),
            denied(lambda: subprocess.run([sys.executable, "--self-test"])),
        ]
        if not all(checks):
            raise ValueError("compiler access self-test failed")
        print(json.dumps({"schema": manifest["schema"], "version": vyper.__version__, "python": manifest["python"], "access_denied": True, "limits": sys.platform == "linux"}))
        return
    raw = read_input(max_input)
    value = json.loads(raw, object_pairs_hook=document)
    validate_input(value)
    output = compile_input(value)
    # System exceptions and absolute runtime paths are never compiler diagnostics.
    if any(error.get("component") == "vyper" for error in output.get("errors", [])):
        raise ValueError("compiler runtime failed")
    encoded = json.dumps(output, default=str).encode()
    if len(encoded) > max_output:
        raise ValueError("compiler output exceeds limit")
    sys.stdout.buffer.write(encoded)


try:
    main()
except BaseException:
    sys.stderr.write("compiler runtime failed\n")
    sys.exit(1)
