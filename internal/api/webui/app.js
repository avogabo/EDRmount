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

async function apiPostJson(path, body) {
  const r = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
    body: JSON.stringify(body || {})
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
      out.textContent = 'Cargando logs...';
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
      out.textContent = 'Cargando ficheros...';
      try {
        const files = await apiGet(`/api/v1/catalog/imports/${it.id}/files`);
        out.textContent = files.map(f => {
          return `${f.idx}: bytes=${f.total_bytes} segs=${f.segments_count}\n${f.subject}`;
        }).join('\n\n');
      } catch (e) {
        out.textContent = String(e);
      }
    };

    const btnRaw = el('button', { class: 'btn', text: 'raw' });
    btnRaw.onclick = async () => {
      const out = document.getElementById('rawItems');
      out.textContent = 'Cargando vista raw...';
      try {
        const items = await apiGet(`/api/v1/raw/imports/${it.id}`);
        out.textContent = items.map(x => {
          return `${x.filename}  bytes=${x.bytes} segs=${x.segments}\n${x.path}`;
        }).join('\n\n');
      } catch (e) {
        out.textContent = String(e);
      }
    };

    const actions = el('td');
    actions.appendChild(btnFiles);
    actions.appendChild(btnRaw);
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

  // Fill upload provider form
  const up = (cfg.upload || {});
  document.getElementById('upload_provider').value = (up.provider || 'ngpost');

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

  // Fill download provider form
  const d = (cfg.download || {});
  document.getElementById('dl_enabled').checked = !!d.enabled;
  document.getElementById('dl_host').value = d.host || '';
  document.getElementById('dl_port').value = d.port || 563;
  document.getElementById('dl_ssl').checked = (d.ssl !== false);
  document.getElementById('dl_user').value = d.user || '';
  document.getElementById('dl_pass').value = (d.pass && d.pass !== '***') ? d.pass : '';
  document.getElementById('dl_connections').value = d.connections || 20;
  document.getElementById('dl_prefetch').value = (d.prefetch_segments != null) ? d.prefetch_segments : 50;

  // Fill runner form
  const r = (cfg.runner || {});
  document.getElementById('run_enabled').checked = (r.enabled !== false);
  document.getElementById('run_mode').value = r.mode || 'stub';

  // Fill library + metadata form
  const l = (cfg.library || {});
  document.getElementById('lib_enabled').checked = (l.enabled !== false);
  document.getElementById('lib_upper').checked = !!l.uppercase_folders;
  document.getElementById('lib_movies_root').value = l.movies_root || 'Peliculas';
  document.getElementById('lib_series_root').value = l.series_root || 'SERIES';
  document.getElementById('lib_emision').value = l.emision_folder || 'Emision';
  document.getElementById('lib_finalizadas').value = l.finalizadas_folder || 'Finalizadas';
  document.getElementById('lib_movie_dir').value = l.movie_dir_template || '';
  document.getElementById('lib_movie_file').value = l.movie_file_template || '';
  document.getElementById('lib_series_dir').value = l.series_dir_template || '';
  document.getElementById('lib_season_dir').value = l.season_folder_template || '';
  document.getElementById('lib_series_file').value = l.series_file_template || '';

  const tm = ((cfg.metadata || {}).tmdb || {});
  document.getElementById('tmdb_enabled').checked = !!tm.enabled;
  document.getElementById('tmdb_lang').value = tm.language || 'es-ES';
  document.getElementById('tmdb_key').value = '';
}

async function saveConfigEditor() {
  const ta = document.getElementById('config');
  const status = document.getElementById('configStatus');
  status.textContent = 'Guardando...';
  try {
    const parsed = JSON.parse(ta.value);
    const out = await apiPutJson('/api/v1/config', parsed);
    ta.value = fmtJSON(out);
    status.textContent = 'Guardado.';
    await loadConfigEditor();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function restartNow() {
  const status = document.getElementById('runStatus');
  status.textContent = 'Reiniciando... (Restarting...)';
  try {
    await apiPostJson('/api/v1/restart', {});
    status.textContent = 'Reinicio solicitado (restart requested). Espera ~5-10s.';
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function saveRunnerSettings() {
  const status = document.getElementById('runStatus');
  status.textContent = 'Guardando ajustes del runner...';
  try {
    const cfg = await apiGet('/api/v1/config');
    const prevEnabled = (cfg.runner || {}).enabled !== false;
    cfg.runner = cfg.runner || {};
    cfg.runner.enabled = document.getElementById('run_enabled').checked;
    cfg.runner.mode = document.getElementById('run_mode').value;
    await apiPutJson('/api/v1/config', cfg);

    const nextEnabled = cfg.runner.enabled;
    if (prevEnabled !== nextEnabled) {
      status.textContent = 'Guardado. Requiere reinicio (restart required).';
    } else {
      status.textContent = 'Guardado.';
    }
    await loadConfigEditor();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function saveLibraryForm() {
  const status = document.getElementById('libStatus');
  status.textContent = 'Guardando biblioteca... (Saving library...)';
  try {
    const cfg = await apiGet('/api/v1/config');
    cfg.library = cfg.library || {};
    cfg.library.enabled = document.getElementById('lib_enabled').checked;
    cfg.library.uppercase_folders = document.getElementById('lib_upper').checked;
    cfg.library.movies_root = document.getElementById('lib_movies_root').value.trim();
    cfg.library.series_root = document.getElementById('lib_series_root').value.trim();
    cfg.library.emision_folder = document.getElementById('lib_emision').value.trim();
    cfg.library.finalizadas_folder = document.getElementById('lib_finalizadas').value.trim();
    cfg.library.movie_dir_template = document.getElementById('lib_movie_dir').value.trim();
    cfg.library.movie_file_template = document.getElementById('lib_movie_file').value.trim();
    cfg.library.series_dir_template = document.getElementById('lib_series_dir').value.trim();
    cfg.library.season_folder_template = document.getElementById('lib_season_dir').value.trim();
    cfg.library.series_file_template = document.getElementById('lib_series_file').value.trim();

    cfg.metadata = cfg.metadata || {};
    cfg.metadata.tmdb = cfg.metadata.tmdb || {};
    cfg.metadata.tmdb.enabled = document.getElementById('tmdb_enabled').checked;
    cfg.metadata.tmdb.language = document.getElementById('tmdb_lang').value.trim();
    const key = document.getElementById('tmdb_key').value;
    if (key && key.trim() !== '') cfg.metadata.tmdb.api_key = key.trim();

    const out = await apiPutJson('/api/v1/config', cfg);
    document.getElementById('config').value = fmtJSON(out);
    status.textContent = 'Guardado (saved).';
    await loadConfigEditor();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function saveProviderForm() {
  const status = document.getElementById('provStatus');
  status.textContent = 'Guardando proveedor... (Saving provider...)';
  try {
    const cfg = await apiGet('/api/v1/config');
    cfg.upload = cfg.upload || {};
    cfg.upload.provider = document.getElementById('upload_provider').value;

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
    status.textContent = 'Guardado (saved).';
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function testProvider() {
  const status = document.getElementById('provStatus');
  status.textContent = 'Probando conectividad... (Testing...)';
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

async function refreshManual() {
  const out = document.getElementById('manOut');
  const status = document.getElementById('manStatus');
  const dir = (document.getElementById('man_dir').value || 'root').trim();
  status.textContent = 'Cargando...';
  try {
    const dirs = await apiGet(`/api/v1/manual/dirs?parent_id=${encodeURIComponent(dir)}`);
    const items = await apiGet(`/api/v1/manual/items?dir_id=${encodeURIComponent(dir)}`);
    const lines = [];
    lines.push(`DIR ${dir}`);
    lines.push('');
    lines.push('Subfolders (click id to navigate):');
    for (const d of dirs) lines.push(`- ${d.name}   id=${d.id}`);
    lines.push('');
    lines.push('Items (click id to edit):');
    for (const it of items) lines.push(`- id=${it.id}  label=${it.label}  import=${it.import_id} idx=${it.file_idx} bytes=${it.bytes}`);
    out.textContent = lines.join('\n');

    // Populate import picker
    const imports = await apiGet('/api/v1/catalog/imports');
    const sel = document.getElementById('man_pick_import');
    sel.innerHTML = '';
    for (const imp of imports) {
      const opt = document.createElement('option');
      opt.value = imp.id;
      opt.textContent = `${imp.id.slice(0,8)}  (${imp.files_count} files)  ${imp.path}`;
      sel.appendChild(opt);
    }

    status.textContent = `OK (${dirs.length} folders, ${items.length} items)`;
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function createManualFolder() {
  const status = document.getElementById('manStatus');
  const dir = (document.getElementById('man_dir').value || 'root').trim();
  const name = (document.getElementById('man_new_folder').value || '').trim();
  status.textContent = 'Creating...';
  try {
    const r = await fetch('/api/v1/manual/dirs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify({ parent_id: dir, name })
    });
    if (!r.ok) throw new Error(await r.text());
    status.textContent = 'Created.';
    document.getElementById('man_new_folder').value = '';
    await refreshManual();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function addManualItem() {
  const status = document.getElementById('manStatus');
  const dir = (document.getElementById('man_dir').value || 'root').trim();
  const importId = (document.getElementById('man_imp').value || '').trim();
  const fileIdx = Number(document.getElementById('man_idx').value || 0);
  const label = (document.getElementById('man_label').value || '').trim();
  status.textContent = 'Adding...';
  try {
    const r = await fetch('/api/v1/manual/items', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify({ dir_id: dir, import_id: importId, file_idx: fileIdx, label })
    });
    if (!r.ok) throw new Error(await r.text());
    status.textContent = 'Added.';
    document.getElementById('man_label').value = '';
    await refreshManual();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function boot() {
  await loadConfigEditor();
  await loadBackupSettings();
  await refreshBackups();
  await refreshJobs();
  await refreshCatalog();
  await refreshManual();
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
  document.getElementById('btnSaveLibrary').onclick = () => saveLibraryForm().catch(err => alert(err));
  document.getElementById('btnTestProvider').onclick = () => testProvider().catch(err => alert(err));
  document.getElementById('btnSaveDownload').onclick = () => saveDownloadProvider().catch(err => alert(err));
  document.getElementById('btnTestDownload').onclick = () => testDownloadProvider().catch(err => alert(err));
  document.getElementById('btnManRefresh').onclick = () => refreshManual().catch(err => alert(err));
  // btnManUp is wired dynamically by renderManualBreadcrumb()
  document.getElementById('btnManNewFolder').onclick = () => createManualFolder().catch(err => alert(err));
  // Legacy btnManAdd removed from UI; add is done from the import file list.
  document.getElementById('btnManLoadImport').onclick = () => loadImportFilesForManual().catch(err => alert(err));
  document.getElementById('btnManUpdateItem').onclick = () => updateManualItem().catch(err => alert(err));
  document.getElementById('btnManDeleteItem').onclick = () => deleteManualItem().catch(err => alert(err));
  document.getElementById('btnBkSave').onclick = () => saveBackupSettings().catch(err => alert(err));
  document.getElementById('btnBkRun').onclick = () => backupNow().catch(err => alert(err));
  document.getElementById('btnBkRefresh').onclick = () => refreshBackups().catch(err => alert(err));
  document.getElementById('btnRunSave').onclick = () => saveRunnerSettings().catch(err => alert(err));
  document.getElementById('btnRestart').onclick = () => restartNow().catch(err => alert(err));
  attachManualClickHelpers();
  boot().catch(err => alert(err));
});
