async function apiGet(path) {
  const r = await fetch(path, { headers: { 'Accept': 'application/json' } });
  if (!r.ok) throw new Error(await r.text());
  return await r.json();
}

async function apiPutJson(path, body) {
  const r = await fetch(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
    body: JSON.stringify(body, null, 2)
  });
  if (!r.ok) throw new Error(await r.text());
  return await r.json();
}

function el(tag, attrs = {}, children = []) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') n.className = v;
    else if (k === 'text') n.textContent = v;
    else n.setAttribute(k, v);
  }
  for (const c of children) n.appendChild(c);
  return n;
}

function fmtJSON(obj) {
  return JSON.stringify(obj, null, 2);
}

async function refreshJobs() {
  const box = document.getElementById('jobs');
  box.innerHTML = '';
  const jobs = await apiGet('/api/v1/jobs');

  const table = el('table', { class: 'tbl' });
  const thead = el('thead');
  thead.appendChild(el('tr', {}, [
    el('th', { text: 'id' }),
    el('th', { text: 'type' }),
    el('th', { text: 'state' }),
    el('th', { text: 'created' }),
    el('th', { text: 'actions' }),
  ]));
  table.appendChild(thead);

  const tbody = el('tbody');
  for (const j of jobs) {
    const tr = el('tr');
    tr.appendChild(el('td', { class: 'mono', text: j.id.slice(0, 8) }));
    tr.appendChild(el('td', { text: j.type }));
    tr.appendChild(el('td', { text: j.state }));
    tr.appendChild(el('td', { class: 'mono', text: (j.created_at || '').replace('T', ' ').replace('Z', '') }));

    const btnLogs = el('button', { class: 'btn', text: 'logs' });
    btnLogs.onclick = async () => {
      const out = document.getElementById('logs');
      out.textContent = 'Loading logs...';
      try {
        const resp = await apiGet(`/api/v1/jobs/${j.id}/logs?limit=200`);
        out.textContent = resp.lines.reverse().join('\n');
      } catch (e) {
        out.textContent = String(e);
      }
    };

    const actions = el('td');
    actions.appendChild(btnLogs);
    tr.appendChild(actions);

    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  box.appendChild(table);
}

async function loadConfigEditor() {
  const ta = document.getElementById('config');
  const status = document.getElementById('configStatus');
  const cfg = await apiGet('/api/v1/config');
  ta.value = fmtJSON(cfg);
  status.textContent = '';
}

async function saveConfigEditor() {
  const ta = document.getElementById('config');
  const status = document.getElementById('configStatus');
  status.textContent = 'Saving...';
  try {
    const parsed = JSON.parse(ta.value);
    const out = await apiPutJson('/api/v1/config', parsed);
    ta.value = fmtJSON(out);
    status.textContent = 'Saved.';
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function boot() {
  await loadConfigEditor();
  await refreshJobs();
  setInterval(() => refreshJobs().catch(() => {}), 2000);
}

window.addEventListener('DOMContentLoaded', () => {
  document.getElementById('btnReloadConfig').onclick = () => loadConfigEditor().catch(err => alert(err));
  document.getElementById('btnSaveConfig').onclick = () => saveConfigEditor().catch(err => alert(err));
  document.getElementById('btnRefreshJobs').onclick = () => refreshJobs().catch(err => alert(err));
  boot().catch(err => alert(err));
});
