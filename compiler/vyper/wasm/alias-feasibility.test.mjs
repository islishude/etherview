import assert from 'node:assert/strict';
import test from 'node:test';
import {selectRuntime,compare} from './alias-feasibility.mjs';
import {versions} from './tooling.mjs';

test('all 26 supported versions route to exactly four modern and 22 legacy runtimes',()=>{
 const groups={pyodide:[], 'pyodide-legacy':[]};
 for(const item of versions()) groups[selectRuntime(item.version)].push(item.version);
 assert.deepEqual(groups.pyodide,['0.4.0','0.4.1','0.4.2','0.4.3']);
 assert.equal(groups['pyodide-legacy'].length,22);
 assert.equal(selectRuntime('0.3.10'),'pyodide-legacy');
});
test('unsupported and malformed versions have no runtime fallback',()=>{
 for(const v of ['0.4.4','0.5.0','0.3.5','latest','0.4.3+fake','../../pyodide']) assert.throws(()=>selectRuntime(v),/unsupported/);
});
test('bytecode and diagnostic changes are rejected',()=>{
 const expected={contracts:{A:{bytecode:'00',abi:[]}}};
 assert(compare(expected,expected,false));
 assert(!compare({contracts:{A:{bytecode:'01',abi:[]}}},expected,false));
 assert(!compare({}, {},false));
 const error={errors:[{type:'SyntaxException',severity:'error'}]};
 assert(compare(error,error,true));
 assert(!compare({errors:[{type:'AttributeError',severity:'error'}]},error,true));
 assert(!compare({errors:[{type:'SyntaxException',severity:'warning'}]},error,true));
});
