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

async function refreshCatalog() {
  const box = document.getElementById('catalog');
  box.innerHTML = '';
  const items = await apiGet('/api/v1/catalog/imports');

  const table = el('table', { class: 'tbl' });
  const thead = el('thead');
  thead.appendChild(el('tr', {}, [
    el('th', { text: 'import id' }),
    el('th', { text: 'nzb path' }),
    el('th', { text: 'files' }),
    el('th', { text: 'bytes' }),
    el('th', { text: 'time' }),
    el('th', { text: 'actions' }),
  ]));
  table.appendChild(thead);

  const tbody = el('tbody');
  for (const it of items) {
    const tr = el('tr');
    tr.appendChild(el('td', { class: 'mono', text: it.id.slice(0, 8) }));
    tr.appendChild(el('td', { class: 'mono', text: it.path }));
    tr.appendChild(el('td', { text: String(it.files_count) }));
    tr.appendChild(el('td', { class: 'mono', text: String(it.total_bytes) }));
    tr.appendChild(el('td', { class: 'mono', text: (it.imported_at || '').replace('T', ' ').replace('Z', '') }));

    const btnFiles = el('button', { class: 'btn', text: 'files' });
    btnFiles.onclick = async () => {
      const out = document.getElementById('catalogFiles');
      out.textContent = 'Loading files...';
      try {
        const files = await apiGet(`/api/v1/catalog/imports/${it.id}/files`);
        out.textContent = files.map(f => {
          return `${f.idx}: bytes=${f.total_bytes} segs=${f.segments_count}\n${f.subject}`;
        }).join('\n\n');
      } catch (e) {
        out.textContent = String(e);
      }
    };

    const actions = el('td');
    actions.appendChild(btnFiles);
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

  // Fill provider form (ngpost)
  const n = (cfg.ngpost || {});
  document.getElementById('ng_enabled').checked = !!n.enabled;
  document.getElementById('ng_host').value = n.host || '';
  document.getElementById('ng_port').value = n.port || 563;
  document.getElementById('ng_ssl').checked = (n.ssl !== false);
  document.getElementById('ng_user').value = n.user || '';
  document.getElementById('ng_pass').value = (n.pass && n.pass !== '***') ? n.pass : '';
  document.getElementById('ng_groups').value = n.groups || '';
  document.getElementById('ng_connections').value = n.connections || 20;
  document.getElementById('ng_threads').value = n.threads || 2;
  document.getElementById('ng_output_dir').value = n.output_dir || '/host/inbox/nzb';
  document.getElementById('ng_tmp_dir').value = n.tmp_dir || '';
  document.getElementById('ng_obfuscate').checked = !!n.obfuscate;
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
    await loadConfigEditor();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function saveProviderForm() {
  const status = document.getElementById('provStatus');
  status.textContent = 'Saving provider settings...';
  try {
    const cfg = await apiGet('/api/v1/config');
    cfg.ngpost = cfg.ngpost || {};
    cfg.ngpost.enabled = document.getElementById('ng_enabled').checked;
    cfg.ngpost.host = document.getElementById('ng_host').value.trim();
    cfg.ngpost.port = Number(document.getElementById('ng_port').value || 563);
    cfg.ngpost.ssl = document.getElementById('ng_ssl').checked;
    cfg.ngpost.user = document.getElementById('ng_user').value;
    // Only overwrite pass if user typed one
    const pass = document.getElementById('ng_pass').value;
    if (pass && pass.trim() !== '') cfg.ngpost.pass = pass;
    cfg.ngpost.groups = document.getElementById('ng_groups').value.trim();
    cfg.ngpost.connections = Number(document.getElementById('ng_connections').value || 20);
    cfg.ngpost.threads = Number(document.getElementById('ng_threads').value || 2);
    cfg.ngpost.output_dir = document.getElementById('ng_output_dir').value.trim();
    cfg.ngpost.tmp_dir = document.getElementById('ng_tmp_dir').value.trim();
    cfg.ngpost.obfuscate = document.getElementById('ng_obfuscate').checked;

    const out = await apiPutJson('/api/v1/config', cfg);
    document.getElementById('config').value = fmtJSON(out);
    status.textContent = 'Saved provider settings.';
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function testProvider() {
  const status = document.getElementById('provStatus');
  status.textContent = 'Testing connectivity...';
  try {
    const req = {
      host: document.getElementById('ng_host').value.trim(),
      port: Number(document.getElementById('ng_port').value || 563),
      ssl: document.getElementById('ng_ssl').checked
    };
    const r = await fetch('/api/v1/provider/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify(req)
    });
    const data = await r.json();
    status.textContent = (data.ok ? 'OK' : 'FAIL') + ` (${data.latency_ms} ms): ${data.message}`;
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

function shouldAutoRefresh() {
  const cb = document.getElementById('autoRefresh');
  if (!cb) return true;
  if (!cb.checked) return false;

  // If the user is typing/focused in a field, don’t fight them.
  const a = document.activeElement;
  if (!a) return true;
  const tag = (a.tagName || '').toLowerCase();
  if (tag === 'input' || tag === 'textarea' || tag === 'select') return false;
  // If focus is inside logs panes, pause refresh too.
  const ids = new Set(['logs', 'catalogFiles']);
  if (a.id && ids.has(a.id)) return false;
  return true;
}

async function boot() {
  await loadConfigEditor();
  await refreshJobs();
  await refreshCatalog();
  // Auto-refresh, but slow enough to not fight the user.
  setInterval(() => {
    if (shouldAutoRefresh()) refreshJobs().catch(() => {});
  }, 8000);
  setInterval(() => {
    if (shouldAutoRefresh()) refreshCatalog().catch(() => {});
  }, 15000);
}

window.addEventListener('DOMContentLoaded', () => {
  document.getElementById('btnReloadConfig').onclick = () => loadConfigEditor().catch(err => alert(err));
  document.getElementById('btnSaveConfig').onclick = () => saveConfigEditor().catch(err => alert(err));
  document.getElementById('btnRefreshJobs').onclick = () => refreshJobs().catch(err => alert(err));
  document.getElementById('btnRefreshCatalog').onclick = () => refreshCatalog().catch(err => alert(err));
  document.getElementById('btnSaveProvider').onclick = () => saveProviderForm().catch(err => alert(err));
  document.getElementById('btnTestProvider').onclick = () => testProvider().catch(err => alert(err));
  boot().catch(err => alert(err));
});
