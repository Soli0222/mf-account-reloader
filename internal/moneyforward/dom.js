// This adapter intentionally reads account identity and status only.
(() => {
 const norm = s => (s || '').replace(/\s+/g, ' ').trim();
 const visible = e => !!e && e.getClientRects().length > 0 && getComputedStyle(e).visibility !== 'hidden';
 const enabled = e => visible(e) && !e.disabled && e.getAttribute('aria-disabled') !== 'true' && !e.classList.contains('disabled');
 const label = e => [e.innerText,e.value,e.getAttribute('aria-label'),e.title].map(norm).find(Boolean)||'';
 const controls = root => [...root.querySelectorAll('a,button,input[type="submit"],input[type="button"]')];
 const bulk = () => controls(document).filter(e => visible(e) && label(e).replace(/\s/g,'') === '金融機関からのデータ一括更新');
 const status = s => /^(更新中|取得中)(?:$|[ （(])/.test(s) ? 'updating' : /要認証|認証が必要|再認証/.test(s) ? 'auth_required' : /失敗|エラー/.test(s) ? 'failed' : /停止/.test(s) ? 'suspended' : /^(正常|取得完了|更新完了)(?:$|[ （(])/.test(s) ? 'ok' : 'unknown';
 const reload = row => controls(row).filter(e => visible(e) && !/編集|削除|再連携|認証/.test(label(e)) && (/^(更新|更新する|データ更新|データを更新|個別更新)$/.test(label(e)) || (label(e) === '' && /(?:^|\s)(?:account-refresh|reload-account)(?:\s|$)/.test(e.className))));
 const rows = () => [...document.querySelectorAll('[id="account-table"] tr')].filter(r => r.querySelector('td'));
 const parse = r => {
  const links = [...r.querySelectorAll('a[href],form[action]')].map(e => ({el:e,url:new URL(e.getAttribute('href') || e.getAttribute('action'), location.href)})).filter(x => x.url.origin === location.origin);
  const identities = links.map(x => ({...x,match:x.url.pathname.match(/^\/accounts\/(show(?:_manual)?|edit)\/([^/]+)\/?$/)})).filter(x => x.match);
  const ids = [...new Set(identities.map(x => decodeURIComponent(x.match[2])))];
  const headers=[...r.closest('table').querySelectorAll('tr')].map(tr=>[...tr.querySelectorAll('th')]).find(h=>h.length)||[];
  const field=labels=>{const indices=headers.map((h,i)=>labels.includes(norm(h.textContent))?i:-1).filter(i=>i>=0);return indices.length===1?r.children[indices[0]]:null};
  const nameCell = r.querySelector('.account-name,.service,[data-account-name]')||field(['登録名','金融機関']);
  const show = identities.filter(x => x.match[1].startsWith('show') && x.el.tagName === 'A');
  const name = (show.length === 1 ? show[0].el.textContent : nameCell ? nameCell.innerText : '').trim();
  // The site keeps inactive status templates in the same cell. textContent
  // includes their hidden labels (e.g. 更新中 before the visible 正常).
  const statusCells = [...r.querySelectorAll('.account-status')];
  const headerStatus = field(['更新状態','状態']);
  let cells = statusCells.filter(visible);
  if(statusCells.length===0&&headerStatus&&visible(headerStatus))cells=[headerStatus];
  const st = cells.length === 1 ? norm(cells[0].innerText) : '';
  const manual = identities.some(x => x.match[1] === 'show_manual') || r.dataset.accountType === 'manual';
  const candidates = reload(r);
  return {id:ids.length===1?ids[0]:'', name, status:manual?'manual':status(st), canUpdate:!manual&&ids.length===1&&candidates.length===1&&enabled(candidates[0]), automatic:!manual&&(statusCells.length>0||!!headerStatus), ambiguous:ids.length>1||candidates.length>1};
 };
 const login = () => [...document.querySelectorAll('input[type="password"],input[name="mfid_user[email]"],input[autocomplete="one-time-code"]')].some(visible);
 const snapshot = () => ({structure:document.querySelectorAll('[id="account-table"]').length>0,login:login(),bulkCount:bulk().length,bulkEnabled:bulk().length===1&&enabled(bulk()[0]),accounts:rows().map(parse),receipt:[...document.querySelectorAll('[role="status"],.alert-success,.flash-success,#flash-message')].filter(visible).map(e=>norm(e.innerText)).filter(s=>/更新/.test(s)&&/受け付け|受付|開始/.test(s)).join('|')});
 return {
  snapshot,
  click: a => {
   if(login())return {error:'auth'};
   let c;
   if(a.id===null){c=bulk()}else{const r=rows().filter(r=>parse(r).id===a.id);if(r.length!==1)return {error:'row'};c=reload(r[0]);}
   if(c.length!==1||!enabled(c[0]))return {error:'control'};
   // .click() invokes the real site handler, including anchors and form submits.
   c[0].click();return {clicked:true};
  }
 };
})()
