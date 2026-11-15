const descriptions=[[/deepseek/,'추론 · 코딩'],[/minimax/,'코딩 · 긴 문맥'],[/glm-/,'추론 · 코딩'],[/muse/,'범용 · 작성'],[/nemotron-3-ultra/,'추론 · 코딩 · 종합'],[/nemotron-3\.5-super/,'코딩 · 이미지 · 문서'],[/beam/,'코드 검토 · 범용'],[/kilo-auto|openrouter\/free/,'무료 모델 자동 선택'],[/lightning/,'빠른 추론 · 코딩'],[/ernie/,'빠른 대화 · 범용'],[/north-mini/,'코딩 · 코드 완성'],[/dots/,'요약 · 노트 정리'],[/ling-/,'빠른 범용 응답'],[/nano-omni/,'경량 · 멀티모달'],[/lfm-/,'초경량 · 짧은 응답'],[/glyph|pickle|flint/,'실험 모델']];
export const modelStatus=m=>m.reason==='paid_model'?'유료':m.reason==='pricing_unverified'?'가격 미확인':m.reason==='cooldown'||/limit/.test(m.reason||'')?'한도/재시도 대기':m.reason==='missing_key'?'키 필요':m.reason==='confirm_free_account'?'계정 확인':m.ready?'무료':m.reason==='connect_opencode'?'OpenCode 연결 필요':m.reason==='catalog_expired'?'동기화 필요':'연결 전';
export function modelItems(state,selected){
  const preference=state.settings.preferredModels||[];const rank=new Map(preference.map((id,index)=>[id,index]));
  const all=[...(state.catalog||state.models)].filter(m=>m.catalogStatus!=='deprecated');
  all.sort((a,b)=>Number(!a.free)-Number(!b.free)||(rank.get(a.id)??999)-(rank.get(b.id)??999)||a.name.localeCompare(b.name));
  const entries=all.map(m=>({label:m.name.replace(/^[^:]+:\s*/,'').replace(/\s*\(free\)$/i,'')+(selected===m.id?' ✓':''),detail:modelStatus(m)+' · '+(descriptions.find(([pattern])=>pattern.test(m.id))?.[1]||m.provider),value:m.id,disabled:!m.free||!m.ready,disabledReason:m.reason==='paid_model'?'유료 모델입니다. 현재 무료 호출 모드에서는 실행하지 않습니다.':m.reason==='pricing_unverified'?'무료 가격이 확인되지 않은 모델입니다.':m.provider==='opencode'&&!state.bridge?.ready?'OpenCode 연결이 필요합니다. /connect opencode를 실행하세요.':modelStatus(m)+' 상태입니다. 잠시 후 동기화하거나 연결 상태를 확인하세요.',free:m.free}));
  const split=entries.findIndex(m=>!m.free);entries.splice(split<0?entries.length:split,0,{label:'Freeway Auto'+(selected==='free/auto'?' ✓':''),detail:'무료 · 설정한 우선순위로 자동 선택',value:'free/auto',free:true});
  return entries;
}
