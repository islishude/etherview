"""Bounded inline-source validation shared by all WASM compiler versions."""
import json

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


def compile_request(raw):
    from adapter import compile_input
    value = json.loads(raw, object_pairs_hook=document)
    validate_input(value)
    return json.dumps(compile_input(value), default=str)
