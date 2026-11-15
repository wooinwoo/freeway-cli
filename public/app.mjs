const $ = (q, root = document) => root.querySelector(q);
const content = $('#content');
const dialog = $('#provider-dialog');
const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
const num = n => new Intl.NumberFormat('ko-KR').format(n || 0);
const short = n => n >= 1000000 ? (n / 1000000).toFixed(1) + 'M' : n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + 'K' : num(n);
const pageNames = { overview: '대시보드', models: '모델 탐색', providers: '공급처 연결', playground: '플레이그라운드', usage: '요청 기록', connect: 'API 연결', research: '조사 후보' };
const reasons = { disabled: '연결 전', missing_key: '키 등록 필요', confirm_free_account: '무료 계정 확인', connect_opencode: '서버 연결 필요', cooldown: '잠시 대기', day_request_limit: '오늘 한도 도달', hour_request_limit: '시간 한도 도달', minute_request_limit: '분당 한도 도달', daily_token_limit: '토큰 상한 도달', catalog_expired: '모델 동기화 필요' };
let state, research, page = 'overview', providerId, toastTimer;
let messages = [], chatController, chatBusy = false;

async function api(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { 'content-type': 'application/json', ...options.headers } });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error?.message || '요청을 처리하지 못했습니다.');
  return data;
}
function toast(message) {
  const el = $('#toast'); el.textContent = message; el.classList.add('visible');
  clearTimeout(toastTimer); toastTimer = setTimeout(() => el.classList.remove('visible'), 5000);
}
async function loadState(renderPage = true) {
  state = await api('/api/state');
  state.models.sort((a,b) => Number(b.ready)-Number(a.ready) ||
    state.providers.find(p=>p.id===a.provider).priority-state.providers.find(p=>p.id===b.provider).priority || a.name.localeCompare(b.name));
  $('#provider-nav-count').textContent = state.providers.filter(p => p.ready).length;
  $('#research-nav-count').textContent = state.researchCount;
  $('#footer-status').textContent = state.models.filter(m => m.ready).length + '개 모델 호출 준비 · 유료 대체 꺼짐';
  if (renderPage && !dialog.open) render();
}
function heading(title, text, actions = '', eyebrow = 'YOUR FREE AI WORKSPACE') {
  return '<section class="page-header"><div><span class="eyebrow">' + eyebrow + '</span><h1>' + title + '</h1><p>' + text + '</p></div><div class="page-actions">' + actions + '</div></section>';
}
const button = (label, action, extra = '', cls = '') => '<button class="button ' + cls + '" data-action="' + action + '" ' + extra + '>' + label + '</button>';
function metrics() {
  const ready = state.models.filter(m => m.ready).length;
  const providers = state.providers.filter(p => p.ready).length;
  const requests = state.providers.reduce((n,p) => n + p.usage.day.requests, 0);
  const success = state.providers.reduce((n,p) => n + p.usage.total.succeeded, 0);
  return '<div class="metrics">' + [
    ['호출 준비 모델', num(ready), '등록된 무료 모델 ' + num(state.models.length) + '개', '▦'],
    ['연결된 공급처', num(providers), 'API + OpenCode bridge', '⌘'],
    ['오늘의 요청', num(requests), '성공 ' + num(success) + '회 · 누적 기준', '↗'],
    ['자동 유료 대체', 'OFF', '무료로 확인된 경로만 사용', '◇']
  ].map(([title,value,caption,icon]) => '<div class="metric"><div class="metric-top">' + title + '<span class="metric-icon">' + icon + '</span></div><div class="metric-value">' + value + '</div><div class="metric-bottom ' + (title === '자동 유료 대체' ? 'green' : '') + '">' + caption + '</div></div>').join('') + '</div>';
}
function modelRows(models) {
  return models.map(m => '<tr><td><div class="model-cell"><span class="model-logo">' + esc(m.provider.slice(0,2).toUpperCase()) + '</span><div><span class="model-name">' + esc(m.name) + '</span><span class="model-sub" title="' + esc(m.id) + '">' + esc(m.id) + '</span></div></div></td><td><span class="badge">' + esc(state.providers.find(p => p.id === m.provider)?.name || m.provider) + '</span></td><td class="mono">' + (m.context ? short(m.context) : '—') + '</td><td><span class="free-price">$0<small>FREE ROUTE</small></span></td><td><span class="status-text ' + (m.ready ? '' : 'off') + '">' + (m.ready ? '<span class="dot"></span>호출 준비' : esc(reasons[m.reason] || m.reason)) + '</span></td><td>' + button('대화 ↗', 'try-model', 'data-model="' + esc(m.id) + '"', 'small') + '</td></tr>').join('');
}
function modelPanel(overview = false) {
  const models = state.models.slice(0, overview ? 6 : 100);
  return '<section class="panel"><div class="panel-heading"><div><h2>' + (overview ? '무료 모델' : '모델 카탈로그') + '<span class="count-pill">' + num(state.models.length) + '</span></h2><p>공급처가 무료로 제공하는 모델과 연결 상태를 확인하세요.</p></div>' + (overview ? '<a href="#models">전체 보기 ↗</a>' : '') + '</div>' + (!overview ? '<div class="toolbar"><div class="search"><span>⌕</span><input id="model-search" aria-label="모델 검색" placeholder="모델 이름이나 ID 검색"></div><select id="model-provider" aria-label="공급처 필터"><option value="">모든 공급처</option>' + state.providers.map(p => '<option value="' + p.id + '">' + esc(p.name) + '</option>').join('') + '</select><select id="model-status" aria-label="연결 상태"><option value="">모든 상태</option><option value="ready">호출 준비</option></select><span class="filter-note"><span class="dot"></span> 무료 경로만 표시</span></div>' : '') + '<div class="table-scroll"><table id="model-table"><thead><tr><th>모델</th><th>공급처</th><th>컨텍스트</th><th>요금 경로</th><th>상태</th><th></th></tr></thead><tbody>' + modelRows(models) + '</tbody></table></div>' + (!models.length ? '<div class="empty"><strong>첫 무료 모델을 연결해보세요</strong><p>공급처를 연결하면 이곳에 실제 모델 목록이 표시됩니다.</p><a class="button soft" href="#providers">공급처 연결 ↗</a></div>' : '') + '<div class="table-bottom"><span id="model-result-count">' + num(models.length) + '개 표시 / ' + num(state.models.length) + '개 등록</span><span>연결 상태는 호출 권한과 계정 한도에 따라 달라질 수 있습니다.</span></div></section>';
}
function logsTable(logs) {
  if (!logs.length) return '<div class="empty"><strong>아직 요청 기록이 없습니다</strong><p>플레이그라운드나 연결한 챗에서 첫 메시지를 보내보세요.</p></div>';
  return '<div class="table-scroll"><table><thead><tr><th>시간</th><th>모델 / 공급처</th><th>상태</th><th>토큰</th><th>응답 시간</th></tr></thead><tbody>' + logs.map(log => '<tr><td class="mono">' + new Date(log.time).toLocaleTimeString('ko-KR', { hour12: false }) + '</td><td><span class="model-name">' + esc(log.model.split('/').slice(1).join('/')) + '</span><span class="model-sub">' + esc(log.provider) + '</span></td><td><span class="badge ' + (log.ok ? 'green' : 'amber') + '">' + esc(log.ok ? '완료' : log.error) + '</span></td><td class="mono">' + (log.tokens === null ? '≈ ' + short(log.estimatedTokens) : short(log.tokens)) + '</td><td class="mono">' + (log.durationMs / 1000).toFixed(1) + 's</td></tr>').join('') + '</tbody></table></div>';
}
function overview() {
  const opencode = !state.bridge.ready && state.bridge.installed;
  return heading('한 주소로, 무료 AI.', '여러 공급처의 무료 모델을 연결하고, 내 챗에서 같은 API로 사용하세요.',
    '<a class="button" href="#connect">⌁ API 연결</a><a class="button primary" href="#playground">플레이그라운드 ↗</a>') + metrics()
    + (opencode ? '<div class="callout"><div class="callout-symbol">↗</div><div><h3>설치된 OpenCode를 발견했습니다.</h3><p>기존 로그인으로 공식 서버에 연결하고, 무료 모델만 가져옵니다.</p></div>' + button('OpenCode 연결하기', 'start-opencode', '', 'soft') + '</div>' : '')
    + modelPanel(true)
    + '<div class="bottom-grid"><section class="panel"><div class="panel-heading"><div><h2>하나의 연결, 여러 경로</h2><p>공급처 한도에 도달하면 다음 무료 경로를 확인합니다.</p></div><span class="badge green">FREE ONLY</span></div><div class="route-flow"><div class="route-node"><strong>내 챗</strong><small>단일 API 키</small></div><span class="route-arrow">→</span><div class="route-node"><strong>Freeway</strong><small>한도 · 우선순위</small></div><span class="route-arrow">→</span><div class="route-node"><strong>무료 모델</strong><small>API · OpenCode</small></div></div></section><section class="panel"><div class="panel-heading"><div><h2>챗에 바로 연결</h2><p>기존 OpenAI 호환 클라이언트의 기본 주소를 바꾸세요.</p></div><a href="#connect">설정 보기 ↗</a></div><div class="quick-code"><pre>base_url = "' + esc(state.baseURL) + '"\nmodel    = "free/auto"</pre></div></section></div>'
    + '<section class="panel"><div class="panel-heading"><h2>최근 요청</h2><a href="#usage">전체 기록 ↗</a></div>' + logsTable(state.logs.slice(0,5)) + '</section>';
}
function providersPage() {
  return heading('공급처를 연결하세요.', '키는 이 컴퓨터의 암호화된 보관소에 저장됩니다. OpenCode는 기존 로그인을 사용합니다.',
    button('↻ 상태 새로고침', 'refresh')) + '<div class="provider-grid">' + state.providers.map(p => {
      const count = state.models.filter(m => m.provider === p.id).length;
      const verified = state.logs.some(log => log.provider === p.id && log.ok);
      return '<section class="provider-card"><div class="provider-card-head"><span class="model-logo">' + esc(p.name.slice(0,2).toUpperCase()) + '</span><h3>' + esc(p.name) + '</h3><span class="badge ' + (verified ? 'green' : p.ready ? 'blue' : '') + '">' + (verified ? '호출 확인' : p.ready ? '호출 준비' : esc(reasons[p.reason] || '연결 전')) + '</span></div><p>' + esc(p.quota) + '</p><div class="provider-card-foot"><span class="tiny">' + num(count) + '개 무료 모델 · 우선순위 ' + p.priority + '</span>' + (p.kind === 'opencode' && !state.bridge.ready ? button('서버 연결', 'start-opencode', '', 'small soft') : button('↻', 'sync-provider', 'data-provider="' + p.id + '" aria-label="' + esc(p.name) + ' 모델 동기화"', 'small')) + button('설정', 'edit-provider', 'data-provider="' + p.id + '"', 'small') + '</div><div class="provider-usage"><span>오늘 요청</span><span>' + num(p.usage.day.requests) + (p.limits.day ? ' / ' + num(p.limits.day) : '') + '</span></div>' + (p.syncError ? '<div class="provider-error">' + esc(p.syncError) + '</div>' : '') + '</section>';
    }).join('') + '</div>';
}
function usagePage() {
  return heading('요청의 흐름을 확인하세요.', '본문과 API 키는 기록하지 않습니다. 공급처, 성공 여부, 토큰, 응답 시간만 남깁니다.', button('↻ 새로고침', 'refresh')) + metrics()
    + '<section class="panel"><div class="panel-heading"><h2>공급처별 오늘 사용량</h2><span class="tiny">UTC 00:00 기준 · 이 게이트웨이에서 관측한 사용량</span></div><div class="table-scroll"><table><thead><tr><th>공급처</th><th>오늘 요청</th><th>확인된 토큰</th><th>추정 토큰</th><th>무료 조건</th></tr></thead><tbody>' + state.providers.map(p => '<tr><td class="model-name">' + esc(p.name) + '</td><td class="mono">' + num(p.usage.day.requests) + (p.limits.day ? ' / ' + num(p.limits.day) : '') + '</td><td class="mono">' + short(p.usage.day.tokens) + '</td><td class="mono">≈ ' + short(p.usage.day.estimatedTokens) + '</td><td>' + esc(p.quota) + '</td></tr>').join('') + '</tbody></table></div></section><section class="panel"><div class="panel-heading"><h2>요청 기록 <span class="count-pill">' + state.logs.length + '</span></h2><span class="tiny">최근 200건</span></div>' + logsTable(state.logs) + '</section>';
}
function playgroundPage() {
  return heading('직접 대화해보세요.', '내 챗과 동일한 API를 사용합니다. 응답 아래에서 실제 선택된 모델을 확인할 수 있습니다.')
    + '<div class="playground-layout"><section class="chat-panel"><div class="chat-toolbar"><select id="chat-model" aria-label="대화 모델"><option value="free/auto">✦ Freeway Auto · 무료 경로 자동 선택</option>' + state.models.filter(m => m.ready).map(m => '<option value="' + esc(m.id) + '">' + esc(m.name) + ' · ' + esc(m.provider) + '</option>').join('') + '</select>' + button('새 대화', 'clear-chat', '', 'small') + '</div><div class="chat-feed" id="chat-feed"></div><form class="chat-composer" id="chat-form"><div class="composer-box"><textarea id="chat-input" aria-label="메시지 입력" placeholder="무엇이든 물어보세요."></textarea><div class="composer-bottom"><span>Enter로 전송 · Shift + Enter로 줄바꿈</span><button class="button primary small" id="send-chat" type="submit">전송 ↑</button></div></div></form></section><aside class="playground-side"><section class="panel"><h3>응답 설정</h3><label for="chat-max-tokens">최대 출력 토큰</label><input id="chat-max-tokens" type="number" min="1" max="32768" value="2048"><label for="chat-temperature">Temperature</label><input id="chat-temperature" type="number" min="0" max="2" step=".1" value="0.7"><p>Auto는 연결된 공급처의 우선순위를 따릅니다. 응답이 시작된 뒤에는 다른 모델의 답변을 섞지 않습니다.</p></section><section class="panel"><span class="badge green">FREE ROUTING</span><p class="side-explainer">공급처의 키와 무료 계정 조건이 설정된 모델만 호출합니다. 실제 남은 한도는 공급처 계정에서도 확인하세요.</p></section></aside></div>';
}
function renderMessages() {
  const feed = $('#chat-feed'); if (!feed) return;
  if (!messages.length) { feed.innerHTML = '<div class="chat-empty"><span class="empty-symbol">↗</span><h3>첫 메시지로 시작하세요.</h3><p>공급처는 여러 개여도, 대화는 한곳에서.<br>연결한 무료 모델로 답변합니다.</p></div>'; return; }
  feed.innerHTML = messages.map((m,i) => '<div class="chat-message ' + esc(m.role) + (m.error ? ' error' : '') + '"><div class="chat-message-label"><span>' + (m.role === 'user' ? 'YOU' : 'FREEWAY') + '</span><span id="message-meta-' + i + '">' + esc(m.model || '') + '</span></div><div class="chat-message-body" id="message-body-' + i + '">' + esc(m.content || (chatBusy && i === messages.length - 1 ? '응답을 기다리는 중…' : '')) + '</div></div>').join('');
  feed.scrollTop = feed.scrollHeight;
}
function integrationPage() {
  return heading('내 챗에 연결하세요.', '기본 주소와 Freeway 키 하나로 연결합니다. 공급처의 키는 챗에 따로 전달하지 않습니다.')
    + '<div class="integration-grid"><div><section class="panel connection-card"><h3>BASE URL</h3><div class="copy-field"><input id="base-url" readonly value="' + esc(state.baseURL) + '" aria-label="Base URL">' + button('복사', 'copy-base', '', 'small') + '</div><h3>FREEWAY API KEY</h3><div class="copy-field"><input id="gateway-key" type="password" readonly value="" aria-label="Freeway API 키" placeholder="불러오는 중…">' + button('보기', 'reveal-key', '', 'small') + button('복사', 'copy-key', '', 'small') + '</div><p class="field-help">내 챗 백엔드의 환경 변수에 저장하세요. 공급처의 원본 키는 이 화면에 표시하지 않습니다.</p></section><section class="panel settings-form"><h3>브라우저에서 직접 연결할 때</h3><label for="allowed-origins">허용할 챗 Origin · 한 줄에 하나</label><textarea id="allowed-origins" placeholder="http://localhost:3000">' + esc(state.settings.allowedOrigins.join('\n')) + '</textarea><p class="field-help">챗의 백엔드에서 호출한다면 추가 설정이 필요 없습니다.</p>' + button('Origin 저장', 'save-origins', '', 'small') + '</section></div><div><div class="code-panel"><div class="code-toolbar"><span>JavaScript · server-side</span>' + button('복사', 'copy-example', '', 'small') + '</div><pre id="integration-code"></pre></div><p class="field-help">특정 모델을 고르려면 <code>free/auto</code> 대신 모델 탐색 화면의 전체 ID를 사용하세요. 특정 모델 요청은 자동으로 다른 모델로 바뀌지 않습니다.</p></div></div>';
}
function codeExample() {
  return 'const response = await fetch(\n  "' + state.baseURL + '/chat/completions",\n  {\n    method: "POST",\n    headers: {\n      "Authorization": "Bearer " + process.env.FREEWAY_API_KEY,\n      "Content-Type": "application/json"\n    },\n    body: JSON.stringify({\n      model: "free/auto",\n      messages: [\n        { role: "user", content: "안녕!" }\n      ],\n      stream: false\n    })\n  }\n);\n\nif (!response.ok) throw new Error(await response.text());\nconsole.log((await response.json()).choices[0].message);';
}
function researchPage() {
  if (!research) {
    api('/api/research').then(data => { research = data; if (page === 'research') render(); }).catch(e => toast(e.message));
    return heading('연결의 가능성을 남겨두세요.', '자체 앱 전용인 후보도 무료 조건과 연결 경로를 따로 확인합니다.') + '<div class="loading">조사 기록을 불러오는 중…</div>';
  }
  return heading('연결의 가능성을 남겨두세요.', '30개 후보의 무료 조건과 확인할 항목입니다. 후보 등록은 연결 완료를 뜻하지 않습니다.')
    + '<div class="toolbar research-tools"><div class="search"><span>⌕</span><input id="research-search" aria-label="조사 후보 검색" placeholder="공급처 이름 검색"></div><select id="research-group" aria-label="조사 분류"><option value="">모든 후보</option><option value="direct_api">직접 API</option><option value="tool_bridge">CLI · 서버 · SDK</option><option value="investigate_or_conditional">조건 · 연결 조사</option></select><span class="tiny">확인일 ' + esc(research.as_of) + '</span></div><div class="research-grid" id="research-grid">' + researchCards(research.candidates) + '</div>';
}
function researchCards(entries) {
  const groups = { direct_api: '직접 API', tool_bridge: 'CLI · 서버 · SDK', investigate_or_conditional: '추가 조사' };
  return entries.map(e => '<article class="research-card"><span class="badge ' + (e.group === 'direct_api' ? 'green' : e.group === 'tool_bridge' ? 'blue' : 'amber') + '">' + groups[e.group] + '</span><h3>' + esc(e.name) + '</h3><p>' + esc(e.free) + '</p><div class="research-route">' + esc(e.integration) + '</div><p>' + esc(e.notes) + '</p><div class="research-sources">' + e.sources.map((url,i) => '<a href="' + esc(url) + '" target="_blank" rel="noopener noreferrer">공식 출처 ' + (i+1) + ' ↗</a>').join('') + '</div></article>').join('');
}
function updateModels() {
  const query = $('#model-search').value.toLowerCase();
  const provider = $('#model-provider').value, ready = $('#model-status').value === 'ready';
  const models = state.models.filter(m => (!provider || m.provider === provider) && (!ready || m.ready) && (m.id + ' ' + m.name).toLowerCase().includes(query));
  $('#model-table tbody').innerHTML = modelRows(models.slice(0,100));
  $('#model-result-count').textContent = num(Math.min(models.length,100)) + '개 표시 / ' + num(models.length) + '개 검색 결과';
}
function render() {
  if (!state) return;
  const titles = { overview: overview, models: () => heading('필요한 모델을 찾아보세요.', '동기화한 모델의 전체 ID를 내 챗에서 그대로 사용할 수 있습니다.', '<a href="#providers" class="button">공급처 관리 ↗</a>') + modelPanel(), providers: providersPage, usage: usagePage, playground: playgroundPage, connect: integrationPage, research: researchPage };
  content.innerHTML = titles[page]();
  document.title = pageNames[page] + ' — Freeway';
  $('#page-crumb').textContent = pageNames[page];
  document.querySelectorAll('nav a').forEach(a => a.classList.toggle('active', a.dataset.page === page));
  if (page === 'models') {
    $('#model-search').addEventListener('input', updateModels);
    $('#model-provider').addEventListener('change', updateModels);
    $('#model-status').addEventListener('change', updateModels);
  }
  if (page === 'playground') {
    renderMessages();
    const model = new URLSearchParams(location.hash.split('?')[1]).get('model');
    if (model && [...$('#chat-model').options].some(o => o.value === model)) $('#chat-model').value = model;
    $('#chat-form').addEventListener('submit', sendChat);
    $('#chat-input').addEventListener('keydown', event => {
      if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); if (!chatBusy) sendChat(event); }
    });
  }
  if (page === 'connect') {
    $('#integration-code').textContent = codeExample();
    api('/api/key').then(data => { if ($('#gateway-key')) $('#gateway-key').value = data.key; }).catch(e => toast(e.message));
  }
  if (page === 'research' && research) {
    const update = () => {
      const query = $('#research-search').value.toLowerCase(), group = $('#research-group').value;
      $('#research-grid').innerHTML = researchCards(research.candidates.filter(e => (!group || e.group === group) && e.name.toLowerCase().includes(query)));
    };
    $('#research-search').addEventListener('input', update); $('#research-group').addEventListener('change', update);
  }
}
function route() {
  if (chatBusy) chatController?.abort();
  const hash = location.hash.slice(1).split('?')[0] || 'overview';
  page = pageNames[hash] ? hash : 'overview';
  $('.sidebar').classList.remove('open'); render();
}
async function busy(el, work) {
  if (el?.disabled) return;
  if (el) el.disabled = true;
  try { await work(); } catch (e) { toast(e.message); }
  finally { if (el?.isConnected) el.disabled = false; }
}
async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast('복사했습니다.'); }
  catch { toast('클립보드에 접근할 수 없습니다. 입력란의 값을 직접 복사하세요.'); }
}
content.addEventListener('click', async event => {
  const el = event.target.closest('[data-action]'); if (!el) return;
  const action = el.dataset.action;
  if (action === 'refresh') return busy(el, () => loadState());
  if (action === 'start-opencode') return busy(el, async () => {
    toast('OpenCode 공식 서버를 연결하는 중입니다…');
    const result = await api('/api/opencode/start', { method: 'POST', body: '{}' });
    await loadState(); toast(result.count + '개 무료 모델을 가져왔습니다.');
  });
  if (action === 'sync-provider') return busy(el, async () => {
    const result = await api('/api/providers/' + el.dataset.provider + '/sync', { method: 'POST', body: '{}' });
    await loadState(); toast(result.count + '개 무료 모델을 동기화했습니다.');
  });
  if (action === 'edit-provider') return openProvider(el.dataset.provider);
  if (action === 'try-model') {
    const model=state.models.find(m=>m.id===el.dataset.model);
    if (!model?.ready) { toast('이 모델의 공급처를 먼저 연결하세요.'); return openProvider(model.provider); }
    location.hash = 'playground?model=' + encodeURIComponent(el.dataset.model); return;
  }
  if (action === 'clear-chat' && !chatBusy) { messages = []; renderMessages(); return; }
  if (action === 'copy-base') return copy(state.baseURL);
  if (action === 'copy-key') return copy($('#gateway-key').value);
  if (action === 'copy-example') return copy(codeExample());
  if (action === 'reveal-key') { const input = $('#gateway-key'); input.type = input.type === 'password' ? 'text' : 'password'; el.textContent = input.type === 'password' ? '보기' : '숨기기'; return; }
  if (action === 'save-origins') return busy(el, async () => {
    const origins = $('#allowed-origins').value.split('\n').map(x => x.trim()).filter(Boolean);
    await api('/api/settings', { method: 'PUT', body: JSON.stringify({ allowedOrigins: origins }) });
    await loadState(false); toast('허용 Origin을 저장했습니다.');
  });
});
function openProvider(id) {
  const p = state.providers.find(p => p.id === id); providerId = id;
  $('#provider-dialog-title').textContent = p.name;
  $('#provider-description').textContent = p.quota;
  $('#provider-key-group').hidden = p.kind === 'opencode';
  $('#provider-key').value = '';
  $('#provider-key-status').textContent = p.hasKey ? '저장된 키가 있습니다. 비워두면 그대로 사용합니다.' : p.keyOptional ? '키 없이 연결 가능한 무료 모델은 먼저 동기화할 수 있습니다.' : '공급처에서 발급한 키를 입력하세요.';
  $('#provider-priority').value = p.priority;
  $('#provider-day-limit').value = p.limits.day || '';
  $('#provider-enabled').checked = p.enabled;
  $('#free-confirm-group').hidden = !p.needsConfirmation;
  $('#provider-free-confirm').checked = p.freeConfirmed;
  $('#provider-form-error').textContent = '';
  $('#provider-docs').href = p.url; dialog.showModal();
}
$('#close-dialog').addEventListener('click', () => dialog.close());
dialog.addEventListener('click', event => { if (event.target === dialog) { const r = dialog.getBoundingClientRect(); if (event.clientX < r.left || event.clientX > r.right || event.clientY < r.top || event.clientY > r.bottom) dialog.close(); } });
$('#provider-form').addEventListener('submit', async event => {
  event.preventDefault();
  const submit = event.submitter; submit.disabled = true;
  try {
    const patch = { enabled: $('#provider-enabled').checked, priority: Number($('#provider-priority').value), freeConfirmed: $('#provider-free-confirm').checked };
    if ($('#provider-key').value.trim()) patch.apiKey = $('#provider-key').value.trim();
    if ($('#provider-day-limit').value) patch.limits = { day: Number($('#provider-day-limit').value) };
    await api('/api/providers/' + providerId, { method: 'PUT', body: JSON.stringify(patch) });
    if (patch.enabled && providerId !== 'opencode') {
      try { await api('/api/providers/' + providerId + '/sync', { method: 'POST', body: '{}' }); }
      catch (e) { toast('설정은 저장했습니다. ' + e.message); dialog.close(); await loadState(); return; }
    }
    dialog.close(); await loadState(); toast('공급처 설정을 저장했습니다.');
  } catch (e) { $('#provider-form-error').textContent = e.message; }
  finally { submit.disabled = false; }
});
async function sendChat(event) {
  event.preventDefault();
  if (chatBusy) { chatController?.abort(); return; }
  const input = $('#chat-input'), text = input.value.trim(); if (!text) return;
  const model = $('#chat-model').value;
  const maxTokens = Number($('#chat-max-tokens')?.value || 2048), temperature = Number($('#chat-temperature')?.value || .7);
  input.value = ''; chatBusy = true; chatController = new AbortController();
  messages.push({ role: 'user', content: text });
  const payload = messages.filter(m => !m.error && m.content).map(m => ({ role: m.role, content: m.content }));
  messages.push({ role: 'assistant', content: '', model: '연결 중…' });
  const assistant = messages.at(-1), index = messages.length - 1;
  renderMessages();
  const send = $('#send-chat'); send.textContent = '중지 ■';
  $('#chat-model').disabled = true;
  try {
    const response = await fetch('/v1/chat/completions', { method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ model, messages: payload, max_tokens: maxTokens, temperature, stream: true }), signal: chatController.signal });
    if (!response.ok) { const data = await response.json(); throw new Error(data.error?.message || '대화 요청 실패'); }
    const reader = response.body.getReader(), decoder = new TextDecoder();
    let buffer = '', ended = false;
    while (!ended) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let pos;
      while ((pos = buffer.indexOf('\n\n')) >= 0) {
        const block = buffer.slice(0,pos); buffer = buffer.slice(pos+2);
        const raw = block.split('\n').filter(l => l.startsWith('data:')).map(l => l.slice(5).trimStart()).join('\n');
        if (!raw) continue;
        if (raw === '[DONE]') { ended = true; break; }
        const chunk = JSON.parse(raw);
        if (chunk.error) throw new Error(chunk.error.message);
        assistant.model = chunk.model || assistant.model;
        const delta = chunk.choices?.[0]?.delta?.content || '';
        assistant.content += delta;
        const element = $('#message-body-' + index);
        if (element) { element.textContent = assistant.content || '응답을 기다리는 중…'; $('#message-meta-' + index).textContent = assistant.model; $('#chat-feed').scrollTop = $('#chat-feed').scrollHeight; }
      }
    }
    if (!ended) throw new Error('연결이 완료 전에 종료되었습니다.');
  } catch (e) {
    assistant.error = true;
    const message = e.name === 'AbortError' ? '응답을 중지했습니다.' : e.message;
    assistant.content = assistant.content ? assistant.content + '\n\n[' + message + ']' : message;
    assistant.model = e.name === 'AbortError' ? '중지됨' : '요청 실패';
  } finally {
    chatBusy = false; chatController = null;
    if (page === 'playground') { renderMessages(); $('#send-chat').textContent = '전송 ↑'; $('#chat-model').disabled = false; }
    await loadState(false).catch(() => {});
  }
}
$('#mobile-menu').addEventListener('click', () => $('.sidebar').classList.toggle('open'));
window.addEventListener('hashchange', route);
try { await loadState(false); route(); }
catch (e) { content.innerHTML = '<div class="panel error-panel"><h2>게이트웨이에 연결하지 못했습니다.</h2><p>' + esc(e.message) + '</p><button class="button" id="reload-page">다시 연결</button></div>'; $('#reload-page').addEventListener('click', () => location.reload()); }
setInterval(() => {
  if (document.hidden || chatBusy || dialog.open || ['connect','playground','research'].includes(page) || document.activeElement?.matches('input,textarea,select')) return;
  loadState().catch(() => {});
}, 15000);
