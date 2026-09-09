"""Run official Vyper with an explicitly reported diagnostic hash replacement."""
import sys
import json
import keccak_probe
sys.modules['Crypto.Hash.keccak'] = keccak_probe
import vyper
from vyper.cli.vyper_json import compile_json, exc_handler_to_dict
print('Vyper '+vyper.__version__+'; diagnostic pure-Python Keccak replacement',file=sys.stderr)
result = compile_json(json.load(sys.stdin), exc_handler=exc_handler_to_dict)
print(json.dumps(result, default=str))
