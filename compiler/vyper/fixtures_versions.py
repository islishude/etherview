"""Regenerate version fixtures from installed locked native compilers."""
import json,pathlib,subprocess
root=pathlib.Path(__file__).resolve().parents[2]; versions=json.loads((root/'compiler/vyper/versions/index.json').read_text())['versions']; output=root/'internal/verify/testdata/compiler/vyper/versions';output.mkdir(exist_ok=True)
program=r'''
import json,sys,inspect,copy
sys.path.insert(0,sys.argv[1])
import vyper
from adapter import compile_input
from vyper.cli import vyper_json
try:
 from vyper.evm.opcodes import EVM_VERSIONS,DEFAULT_EVM_VERSION
except ImportError:
 from vyper.opcodes import EVM_VERSIONS,DEFAULT_EVM_VERSION
v=tuple(map(int,vyper.__version__.split('.')))
outputs=['abi','metadata','layout','evm.bytecode.object','evm.deployedBytecode.object','evm.methodIdentifiers','userdoc','devdoc']
plain='@external\ndef value() -> uint256:\n    return 42\n'
constructor=('@deploy' if v >= (0,4,0) else '@external')+'\ndef __init__(n: uint256):\n    self.value = n\n'
sources={'plain':plain,'constructor':'value: public(uint256)\n'+constructor,'immutable':'thing: immutable(uint256)\n'+constructor.replace('self.value','thing')+'\n@external\ndef value() -> uint256:\n    return thing\n','invalid':'this is invalid vyper syntax'}
cli=inspect.getsource(vyper_json)
profile={'optimization_modes':(['none','gas','codesize'] if v >= (0,3,10) else ['none','gas']) if 'get("optimize"' in cli else [],'evm_versions':[],'default_evm_version':DEFAULT_EVM_VERSION,'bytecode_metadata':'get("bytecodeMetadata"' in cli,'enable_decimals':'get("enable_decimals"' in cli}
def request(source):return {'language':'Vyper','sources':{'A.vy':{'content':source}},'settings':{'search_paths':['.'],'outputSelection':{'A.vy':outputs}}}
for evm in EVM_VERSIONS:
 r=request(plain);r['settings']['evmVersion']=evm;o=compile_input(r)
 if not any(e.get('severity')=='error' for e in o.get('errors',[])):profile['evm_versions'].append(evm)
sources['interface']='import Foo as Foo\n@external\ndef read(target: address) -> uint256:\n    return '+('staticcall ' if v >= (0,4,0) else '')+'Foo(target).value()\n'
sources['invalid_import']='import Missing as Missing\n'+plain
for mode in profile['optimization_modes']: sources['optimize_'+mode]=plain
if profile['bytecode_metadata']:sources['no_metadata']=plain
fixtures={}
for name,source in sources.items():
 r=request(source)
 if name=='interface':r['interfaces']={'Foo.json':{'abi':[{'name':'value','type':'function','inputs':[],'outputs':[{'name':'','type':'uint256'}],'stateMutability':'view','constant':True,'payable':False}]}}
 if name.startswith('optimize_'):r['settings']['optimize']=name.removeprefix('optimize_')
 if name=='no_metadata':r['settings']['bytecodeMetadata']=False
 r=json.loads(json.dumps(r,sort_keys=True))
 o=compile_input(r)
 if name=='immutable' and v < (0,3,1):continue
 if not name.startswith('invalid'): assert not any(e.get('severity')=='error' for e in o.get('errors',[])),o
 changed=copy.deepcopy(r);changed['sources']['A.vy']['content']+=' '
 second=compile_input(changed)
 # Verify adapter bytecodes against direct official compile_json outputs.
 native=copy.deepcopy(r);native['settings'].pop('search_paths');native['settings']['outputSelection']['A.vy']=[x for x in outputs if x in vyper_json.TRANSLATE_MAP]
 if v<(0,3,10) and 'optimize' in native['settings']:native['settings']['optimize']=native['settings']['optimize']=='gas'
 native_output=vyper_json.compile_json(native,vyper_json.exc_handler_to_dict)
 if o.get('contracts'):
  assert o['contracts']['A.vy']['A']['evm']==native_output['contracts']['A.vy']['A']['evm']
  assert o['contracts']['A.vy']['A']['abi']==native_output['contracts']['A.vy']['A']['abi']
 fixtures[name]={'input':r,'output':o,'modified':changed,'modified_output':second}
print(json.dumps({'profile':profile,'fixtures':fixtures},default=str))
'''
profiles={}
for item in versions:
 version=item['version'];python=root/'.local/vyper-builds'/version/'build-venv/bin/python'
 r=subprocess.run([str(python),'-c',program,str(root/'compiler/vyper')],capture_output=True,text=True)
 if r.returncode:raise RuntimeError(version+' official fixture generation failed: '+r.stderr[-1000:])
 d=json.loads(r.stdout);profiles[version]=d['profile'];vdir=output/version;vdir.mkdir(exist_ok=True)
 for name,f in d['fixtures'].items():
  for suffix,value in f.items():(vdir/(name+'.'+suffix+'.json')).write_text(json.dumps(value,sort_keys=True)+'\n')
 print(version,list(d['fixtures']),d['profile'],flush=True)
(root/'internal/verify/vyper_capabilities.json').write_text(json.dumps(profiles,indent=2)+'\n')
