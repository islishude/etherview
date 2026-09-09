// Offline diagnostic extraction only; executes a trusted soljson artifact in Node.
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const crypto = require('node:crypto');
const [artifact, out] = process.argv.slice(2);
const source = fs.readFileSync(artifact, 'utf8');
const context = { require, module: {exports:{}}, exports:{}, __dirname:path.dirname(path.resolve(artifact)), __filename:path.resolve(artifact), process, console, Buffer };
vm.runInNewContext(source + '\nmodule.exports.__probeBinary = wasmBinary;', context, {timeout:30000});
fs.mkdirSync(out,{recursive:true});
fs.writeFileSync(path.join(out,'solc.wasm'),context.module.exports.__probeBinary);
const imports = Object.fromEntries([...source.match(/var asmLibraryArg\s*=\s*\{([^]*?)\};/)[1].matchAll(/"([^"]+)":\s*(\w+)/g)].map(m=>[m[1],m[2]]));
const exportsMap = Object.fromEntries([...source.matchAll(/(?:var \w+ = )?Module\["([^"]+)"\]\s*=\s*asm\["([^"]+)"\]/g)].map(m=>[m[1],m[2]]));
fs.writeFileSync(path.join(out,'solc.json'),JSON.stringify({artifactSHA256:crypto.createHash('sha256').update(source).digest('hex'),imports,exports:exportsMap},null,2));
console.log(context.module.exports.cwrap('solidity_version','string',[])());
