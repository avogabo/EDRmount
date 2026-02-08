async function refreshUploadPanels() {
  const activeBox = document.getElementById('upActive');
  const recentBox = document.getElementById('upRecent');
  const stA = document.getElementById('upActiveStatus');
  const stR = document.getElementById('upRecentStatus');

  stA.textContent = 'Cargando...';
  stR.textContent = '';

  const resp = await apiGet('/api/v1/uploads/summary');
  const items = (resp.items || []);

  const act = items.filter(x => x.state === 'queued' || x.state === 'running');

  // Recent: dedupe by filename so repeated tests like "progress test" don't show twice.
  const recDedup = [];
  const seen = new Set();
  for (const it of items) {
    if (!(it.state === 'done' || it.state === 'failed')) continue;
    const key = ((it.path || '').split('/').slice(-1)[0] || it.id);
    if (seen.has(key)) continue;
    seen.add(key);
    recDedup.push(it);
    if (recDedup.length >= 20) break;
  }
  const rec = recDedup;

  const renderRow = (it) => {
    const row = el('div', { class: 'listRow' });
    const pct = (it.progress != null && it.progress > 0) ? `${it.progress}%` : '';
    const phase = (it.phase || '').trim();

    row.appendChild(el('div', { class: 'name' }, [
      el('div', { class: 'icon', text: it.state === 'failed' ? 'X' : (it.state === 'done' ? 'OK' : '…') }),
      el('div', { class: 'mono', text: (it.path || '').split('/').slice(-1)[0] || it.id.slice(0,8) })
    ]));

    const mid = el('div', { class: 'mono muted', text: `${pct} ${phase}`.trim() || it.state });
    row.appendChild(mid);

    const btn = el('button', { class: 'btn', text: 'logs' });
    btn.onclick = async () => {
      const lines = await apiGet(`/api/v1/jobs/${it.id}/logs?limit=200`);
      alert((lines.lines || []).slice().reverse().join('\n'));
    };
    row.appendChild(btn);
    return row;
  };

  activeBox.innerHTML = '';
  recentBox.innerHTML = '';

  for (const it of act) activeBox.appendChild(renderRow(it));
  if (act.length === 0) activeBox.appendChild(el('div', { class: 'muted', text: '(sin procesos activos)' }));

  for (const it of rec) recentBox.appendChild(renderRow(it));
  if (rec.length === 0) recentBox.appendChild(el('div', { class: 'muted', text: '(sin histórico todavía)' }));

  stA.textContent = `Activos: ${act.length}`;
  stR.textContent = `Últimas: ${rec.length}`;
}
