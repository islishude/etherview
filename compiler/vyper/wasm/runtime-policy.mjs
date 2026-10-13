// Shared by build tools and bundled into the SEA. No runtime filesystem lookup.
import policy from './runtime-policy.json' with {type:'json'};
export const runtimePolicy = policy;
export const sharedRuntimes = Object.fromEntries(Object.entries(policy).map(([id,p])=>[id,p.shared_sha256]));
export function runtimeFor(version) {
  const matches=Object.entries(policy).filter(([,p])=>p.versions.includes(version));
  if(matches.length!==1) throw new Error('unsupported Vyper runtime selection');
  return {id:matches[0][0],...matches[0][1]};
}
export function runtimeByID(id) {
  if(!Object.hasOwn(policy,id)) throw new Error('invalid Vyper runtime');
  return {id,...policy[id]};
}
export function validateRuntimeSet(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length===Object.keys(sharedRuntimes).length &&
    Object.entries(sharedRuntimes).every(([id,sha])=>Object.hasOwn(value,id) && value[id]===sha);
}
