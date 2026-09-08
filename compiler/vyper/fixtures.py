"""Regenerate pinned compiler fixtures with the built, guarded helper."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys

OUTPUTS = ["abi", "metadata", "layout", "evm.bytecode.object", "evm.deployedBytecode.object", "evm.methodIdentifiers", "userdoc", "devdoc"]
PLAIN = "@external\ndef value() -> uint256:\n    return 42\n"
IMMUTABLE = "owner: immutable(address)\n@deploy\ndef __init__(who: address):\n    owner = who\n@external\n@view\ndef get_owner() -> address:\n    return owner\n"


def main():
    helper, output = sys.argv[1:]
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    cases = {
        "filename": ({"A.vy": {"content": PLAIN}}, {}, {}),
        "plain": ({"A.vy": {"content": PLAIN}}, {}, {}),
        "immutable": ({"A.vy": {"content": IMMUTABLE}}, {}, {}),
        "no_metadata": ({"A.vy": {"content": PLAIN}}, {}, {"bytecodeMetadata": False}),
        "module": ({
            "A.vy": {"content": "import helper as helper\n@external\n@pure\ndef value() -> uint256:\n    return helper.answer()\n"},
            "helper.vy": {"content": "@internal\n@pure\ndef answer() -> uint256:\n    return 42\n"},
        }, {}, {}),
        "module_immutable": ({
            "A.vy": {"content": "import lib\ninitializes: lib\n@deploy\ndef __init__(who: address):\n    lib.__init__(who)\n@external\n@view\ndef get_owner() -> address:\n    return lib.get_owner()\n"},
            "lib.vy": {"content": IMMUTABLE.replace("@external", "@internal")},
        }, {}, {}),
        "module_type": ({
            "A.vy": {"content": "import lib\ninitializes: lib\n@deploy\ndef __init__(who: address):\n    lib.__init__(who)\n@external\n@view\ndef get_owner() -> address:\n    return lib.get_owner()\n"},
            "lib.vy": {"content": IMMUTABLE.replace("owner:", "type:").replace("owner =", "type =").replace("return owner", "return type").replace("@external", "@internal")},
        }, {}, {}),
        "interface": ({"A.vy": {"content": "import I as I\n@external\n@view\ndef value(a: address) -> uint256:\n    return staticcall I(a).answer()\n"}},
                      {"I.vyi": {"content": "@external\n@view\ndef answer() -> uint256:\n    ...\n"}}, {}),
        "builtin": ({"A.vy": {"content": "from ethereum.ercs import IERC20\n@external\n@view\ndef value(a: address) -> uint256:\n    return staticcall IERC20(a).totalSupply()\n"}}, {}, {}),
        "abi_interface": ({"A.vy": {"content": "import I as I\n@external\n@view\ndef value(a: address) -> uint256:\n    return staticcall I(a).answer()\n"}},
                          {"I.json": {"abi": [{"type": "function", "name": "answer", "stateMutability": "view", "inputs": [], "outputs": [{"name": "", "type": "uint256"}]}]}}, {}),
        "dispatch": ({"A.vy": {"content": "\n".join(f"@external\ndef value{i}() -> uint256:\n    return {i}\n" for i in range(12))}}, {}, {}),
    }
    checksums = []
    for name, (sources, interfaces, settings) in cases.items():
        request = {"language": "Vyper", "sources": sources, "settings": {
            "optimize": "gas", "search_paths": ["."], "outputSelection": {"A.vy": OUTPUTS}, **settings,
        }}
        if name == "filename":
            request["sources"]["A-Token.vy"] = request["sources"].pop("A.vy")
            request["settings"]["outputSelection"] = {"A-Token.vy": OUTPUTS}
        if interfaces:
            request["interfaces"] = interfaces
        for variant in ["input", "modified"]:
            current = copy.deepcopy(request)
            if variant == "modified":
                for section in ["sources", "interfaces"]:
                    for source in current.get(section, {}).values():
                        if "content" in source:
                            source["content"] += " "
            encoded = json.dumps(current, sort_keys=True)
            response = subprocess.run([helper, "--compile", str(5 << 20), str(64 << 20)], input=encoded, env={}, text=True, capture_output=True, timeout=15, check=True)
            data = json.loads(response.stdout)
            if any(e["severity"] == "error" for e in data.get("errors", [])):
                raise RuntimeError(data)
            files = {f"{name}.{variant}.json": encoded + "\n", f"{name}.{variant}.output.json": json.dumps(data, sort_keys=True) + "\n"}
            for filename, content in files.items():
                (output / filename).write_text(content)
                checksums.append(hashlib.sha256(content.encode()).hexdigest() + "  " + filename)
    (output / "SHA256SUMS").write_text("\n".join(sorted(checksums)) + "\n")


if __name__ == "__main__":
    main()
