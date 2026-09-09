// Development-only reference: never shipped in the production image.
const fs = require('node:fs');
const wrapper = require('../node_modules/solc/wrapper.js');
const write = process.stdout.write.bind(process.stdout);
let unexpectedStdout = false;
process.stdout.write = () => { unexpectedStdout = true; return true; };
let version = '';
try {
  const compiler = wrapper(require(process.argv[2]));
  version = compiler.version();
  const input = fs.readFileSync(0, 'utf8');
  const output = compiler.compile(input);
  write(JSON.stringify({ version, features: compiler.features, unexpectedStdout, output: JSON.parse(output) }));
} catch (error) {
  write(JSON.stringify({ version, unexpectedStdout, referenceFailure: String(error.message || error) }));
  process.exitCode = 1;
}
