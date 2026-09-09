"""Fixed Vyper entrypoint for the read-only CPython WASI runtime."""
import json
import sys
import types

sys.path.insert(0, "/runtime/site-packages")
sys.path.insert(0, "/runtime")
import compiler_crypto
crypto = types.ModuleType("Crypto")
hashes = types.ModuleType("Crypto.Hash")
hashes.keccak = compiler_crypto
crypto.Hash = hashes
sys.modules["Crypto"] = crypto
sys.modules["Crypto.Hash"] = hashes
sys.modules["Crypto.Hash.keccak"] = compiler_crypto

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
    if settings.get("optimize") not in ("none", "gas", "codesize"):
        raise ValueError("invalid compiler optimization")
    selected = settings.get("outputSelection")
    if not isinstance(selected, dict) or len(selected) != 1:
        raise ValueError("invalid compiler target")
    target, outputs = next(iter(selected.items()))
    required = ["abi", "metadata", "layout", "evm.bytecode.object", "evm.deployedBytecode.object", "evm.methodIdentifiers", "userdoc", "devdoc"]
    if target not in sources or "content" not in sources[target] or outputs != required:
        raise ValueError("invalid compiler outputs")

def main():
    if sys.version_info[:3] != (3, 13, 15):
        raise ValueError("compiler runtime version mismatch")
    import vyper
    from vyper.cli.vyper_json import compile_json, exc_handler_to_dict
    if vyper.__version__ != "0.4.3":
        raise ValueError("compiler version mismatch")
    if sys.argv[1:] == ["--self-test"]:
        import immutables
        import cbor2
        if immutables.Map.__module__ != "immutables.map" or "_cbor2" in sys.modules:
            raise ValueError("unexpected native extension")
        if compiler_crypto.new(digest_bits=256, data=b"abc").digest().hex() != "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45":
            raise ValueError("compiler hash self-test failed")
        for path in ["/etc/passwd", "/runtime/__write_probe"]:
            try:
                with open(path, "r" if path == "/etc/passwd" else "w") as file:
                    file.read() if path == "/etc/passwd" else file.write("denied")
            except OSError:
                pass
            else:
                raise ValueError("compiler filesystem self-test failed")
        print(json.dumps({"schema":"etherview-wazero-vyper-self-test-v1","version":vyper.__version__,"python":"3.13.15","access_denied":True}))
        return
    if len(sys.argv) != 4 or sys.argv[1] != "--compile":
        raise ValueError("invalid compiler invocation")
    max_input, max_output = int(sys.argv[2]), int(sys.argv[3])
    value = json.loads(read_input(max_input), object_pairs_hook=document)
    validate_input(value)
    output = compile_json(value, exc_handler_to_dict)
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
