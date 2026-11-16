// Identity is based on upstream IDs, never a fuzzy display-name match.
// Preserve versions, dates, context sizes, distillations and other variants.
const vendors = new Map(Object.entries({ 'z-ai':'zai', 'zai':'zai', 'meta-llama':'meta', meta:'meta', particle:'particle-ai' }));
const bareVendors = [[/^deepseek-/, 'deepseek'], [/^nemotron-/, 'nvidia'], [/^glm-/, 'zai'], [/^minimax-/, 'minimax'], [/^gpt-oss-/, 'openai'], [/^gemini-/, 'google']];
const upstreamSlug = route => (route.upstreamId || route.id.slice((route.provider?.length || route.id.indexOf('/')) + 1)).toLowerCase().replace(/:free$/, '').replace(/-free$/, '');

export function modelIdentity(route, catalog=[]) {
  let slug = upstreamSlug(route);
  let vendor;
  if (slug.includes('/')) {
    const [namespace, ...parts] = slug.split('/');
    if(['kilo-auto','openrouter'].includes(namespace))return route.id;
    vendor = vendors.get(namespace)||namespace; slug = parts.join('/');
  } else vendor = bareVendors.find(([pattern]) => pattern.test(slug))?.[1];
  if(vendor)return 'model/' + vendor + '/' + slug;
  // A bare ID can adopt a manufacturer namespace only when the exact slug
  // has one unambiguous manufacturer in the synchronized catalog.
  if(!/^(free|auto|default|latest|pro|flash|chat|small)$/.test(slug)){
    const matches=new Set(catalog.filter(r=>upstreamSlug(r).includes('/')&&upstreamSlug(r).split('/').slice(1).join('/')===slug).map(r=>modelIdentity(r)));
    if(matches.size===1)return [...matches][0];
  }
  return route.id;
}

export const cleanModelName = name => name.replace(/^[^:]+:\s*/, '').replace(/\s*(\(free\)| free)$/i, '');

export function groupModels(routes) {
  const groups = new Map();
  for (const route of routes) {
    if (route.catalogStatus === 'deprecated') continue;
    const id = modelIdentity(route,routes);
    if (!groups.has(id)) groups.set(id, {id, routes:[]});
    const group = groups.get(id);
    if (!group.routes.some(r => r.id === route.id)) group.routes.push(route);
  }
  return [...groups.values()].map(group => {
    const eligible = group.routes.filter(r => r.free);
    const ready = eligible.filter(r => r.ready);
    const representative = ready[0] || eligible[0] || group.routes[0];
    const contexts = eligible.map(r=>r.context).filter(Number.isFinite);
    return {...group, name:cleanModelName(representative.name || representative.id),
      providers:[...new Set(group.routes.map(r=>r.provider))], free:eligible.length>0, ready:ready.length>0,
      reason:ready.length ? null : representative.reason,
      context:contexts.length ? Math.min(...contexts) : null,
      capabilities:{stream:ready.some(r=>r.capabilities?.stream),tools:ready.some(r=>r.capabilities?.tools)}};
  });
}

export function modelReference(id, routes) {
  if(id==='free/auto'||id.startsWith('model/'))return id;
  const route = routes.find(r=>r.id===id);
  return route ? modelIdentity(route,routes) : modelIdentity({id,provider:id.split('/')[0]},routes);
}

export function stateModelGroups(state) { return state.modelGroups || groupModels(state.catalog || state.models); }

export function rankedGroups(groups, preferred=[]) {
  const routes=groups.flatMap(g=>g.routes), rank=new Map();
  preferred.forEach((id,index)=>{const canonical=modelReference(id,routes);if(!rank.has(canonical))rank.set(canonical,index);});
  return [...groups].sort((a,b)=>Number(!a.free)-Number(!b.free)||(rank.get(a.id)??999)-(rank.get(b.id)??999)||a.name.localeCompare(b.name));
}
