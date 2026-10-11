// Native Linux evidence only; never changes production limits or release records.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync, renameSync } from 'node:fs';
import { arch, cpus, platform, totalmem } from 'node:os';
import { resolve } from 'node:path';
const [command, phase] = process.argv.slice(2);
const root = resolve('.local/vyper-measure');
const architecture = arch() === 'x64' ? 'amd64' : arch();
const run = (exe, args) => execFileSync(exe, args, { encoding: 'utf8' }).trim();
const json = path => JSON.parse(readFileSync(path, 'utf8'));
const save = (name, value) => writeFileSync(`${root}/${name}.json`, JSON.stringify(value, null, 2) + '\n', {flag:'wx'});
assert.equal(platform(), 'linux');
mkdirSync(root, {recursive:true});
if (command === 'start' || command === 'end') {
  assert(['serial', 'parallel'].includes(phase));
  const slice = `vypermeasure${phase}.slice`;
  const cgroup = `/sys/fs/cgroup/${slice}`;
  if (command === 'start') {
    assert.equal(run('docker', ['info', '--format', '{{.CgroupDriver}}']), 'systemd');
    assert.equal(run('docker', ['info', '--format', '{{.CgroupVersion}}']), '2');
    run('sudo', ['systemd-run', '--unit', `vypermeasure${phase}`, '--slice', slice,
      '--property=MemoryAccounting=yes', '--property=CPUAccounting=yes', '/usr/bin/sleep', 'infinity']);
    const wrapper = `${root}/docker-${phase}`;
    writeFileSync(wrapper, `#!/bin/sh\nif [ "$1" = run ]; then\n shift\n exec docker run --cgroup-parent=${slice} "$@"\nfi\nexec docker "$@"\n`, {mode:0o755, flag:'wx'});
    save(`${phase}-start`, {monotonic_ns:process.hrtime.bigint().toString(),
      cpu:readFileSync(`${cgroup}/cpu.stat`, 'utf8'), memory_peak_bytes:Number(readFileSync(`${cgroup}/memory.peak`))});
  } else {
    const start = json(`${root}/${phase}-start.json`);
    const record = {phase, architecture, elapsed_seconds:Number(process.hrtime.bigint()-BigInt(start.monotonic_ns))/1e9,
      memory_peak_bytes:Number(readFileSync(`${cgroup}/memory.peak`)),
      memory_events:readFileSync(`${cgroup}/memory.events`, 'utf8'),
      cpu_stat:readFileSync(`${cgroup}/cpu.stat`, 'utf8'), cpu_stat_before:start.cpu,
      image_id:run('docker', ['image', 'inspect', '--format', '{{.Id}}', 'etherview:local']),
      runner_cpus:cpus().length, runner_memory_bytes:totalmem(),
      commit:process.env.GITHUB_SHA, run_id:process.env.GITHUB_RUN_ID};
    const evidence = json(`.local/vyper-releases/matrix-${architecture}.json`);
    assert.equal(evidence.native_linux, true);
    assert.equal(evidence.image_id, record.image_id);
    assert.equal(Object.keys(evidence.matrix).length, 26);
    assert.equal(Object.values(evidence.matrix).reduce((n,v)=>n+v.cases,0),362);
    assert.match(record.memory_events, /^oom_kill 0$/m);
    save(phase, record);
    renameSync(`.local/vyper-releases/matrix-${architecture}.json`, `${root}/${phase}-matrix.json`);
    run('sudo', ['systemctl', 'stop', `vypermeasure${phase}.service`, slice]);
  }
} else {
  assert.equal(command, 'compare');
  assert.deepEqual(json(`${root}/serial-matrix.json`),json(`${root}/parallel-matrix.json`));
  const serial = json(`${root}/serial.json`), parallel = json(`${root}/parallel.json`);
  assert.equal(serial.image_id, parallel.image_id);
  save('comparison', {serial,parallel,speedup:serial.elapsed_seconds/parallel.elapsed_seconds,
    memory_scope:'Kernel cgroup v2 hierarchical memory.peak: compiler containers plus idle slice keeper; excludes Docker daemon and host CLI. Includes charged page cache. Each mode runs once, serial first; warm-cache/order effects remain.'});
  console.log(readFileSync(`${root}/comparison.json`,'utf8'));
}
