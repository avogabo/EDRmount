async function apiGet(path) {
  const r = await fetch(path, { headers: { 'Accept': 'application/json' } });
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

async function apiPutJson(path, body) {
  const r = await fetch(path, {
    method: 'PUT',
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

function fmtTime(ts) {
  if (!ts) return '';
  return String(ts).replace('T',' ').replace('Z','');
}

function fmtSize(n) {
  if (n == null || n === '') return '';
  const x = Number(n);
  if (!isFinite(x)) return String(n);
  const units = ['B','KB','MB','GB','TB'];
  let v = x;
  let i = 0;
  while (v >= 1024 && i < units.length-1) { v /= 1024; i++; }
  const s = (i === 0) ? String(Math.round(v)) : v.toFixed(1);
  return s + ' ' + units[i];
}

// Pages
let __uploadTimer = null;
let __settingsLoadedOnce = false;
let __logsLoadedOnce = false;
function showPage(name) {
  for (const id of ['library','upload','import','health','settings','logs']) {
    document.getElementById('page_' + id).classList.toggle('hide', id !== name);
  }
  for (const item of document.querySelectorAll('.navItem')) {
    item.classList.toggle('active', item.dataset.page === name);
  }

  // Only poll upload status while on Upload page.
  if (__uploadTimer) {
    clearInterval(__uploadTimer);
    __uploadTimer = null;
  }
  if (name === 'upload') {
    refreshUploadPanels().catch(() => {});
    __uploadTimer = setInterval(() => refreshUploadPanels().catch(() => {}), 2500);
  }

  if (name === 'settings') {
    if (!__settingsLoadedOnce) {
      __settingsLoadedOnce = true;
      loadUploadSettings().catch(() => {});
    }
  }

  if (name === 'health') {
    refreshHealthScan().catch(() => {});
  }

  if (name === 'logs') {
    if (!__logsLoadedOnce) {
      __logsLoadedOnce = true;
      refreshLogsJobs().catch(() => {});
    }
  }
}

// Library explorer (FUSE)
const AUTO_ROOT = '/mount/library-auto';
const MAN_ROOT = '/mount/library-manual';
let autoPath = AUTO_ROOT;
let manPath = MAN_ROOT;

function renderCrumbs(boxId, path, onPick) {
  const box = document.getElementById(boxId);
  box.innerHTML = '';
  const parts = path.split('/').filter(Boolean);
  let acc = '';
  for (let i = 0; i < parts.length; i++) {
    acc += '/' + parts[i];
    const target = acc; // avoid closure bug
    const b = el('button', { class: 'crumb', type: 'button', text: parts[i] });
    b.onclick = () => onPick(target);
    box.appendChild(b);
    if (i !== parts.length - 1) box.appendChild(el('span', { class: 'crumbSep', text: '›' }));
  }
}

function setStatus(id, t) {
  document.getElementById(id).textContent = t || '';
}

async function refreshList(kind) {
  const isAuto = kind === 'auto';
  const path = isAuto ? autoPath : manPath;
  const root = isAuto ? AUTO_ROOT : MAN_ROOT;
  const crumbsId = isAuto ? 'autoCrumbs' : 'manCrumbs';
  const listId = isAuto ? 'autoList' : 'manList';
  const statusId = isAuto ? 'autoStatus' : 'manStatus';

  setStatus(statusId, 'Cargando...');
  renderCrumbs(crumbsId, path, (picked) => {
    if (isAuto) autoPath = picked; else manPath = picked;
    refreshList(kind).catch(err => setStatus(statusId, String(err)));
  });

  const data = isAuto
    ? await apiGet(`/api/v1/library/auto/list?path=${encodeURIComponent(path)}`)
    : await apiGet(`/api/v1/hostfs/list?path=${encodeURIComponent(path)}`);
  const list = document.getElementById(listId);
  list.innerHTML = '';

  for (const e of (data.entries || [])) {
    const row = el('div', { class: 'listRow' });
    const icon = e.is_dir ? 'DIR' : 'FILE';
    row.appendChild(el('div', { class: 'name' }, [
      el('div', { class: 'icon', text: icon }),
      el('div', { class: e.is_dir ? '' : 'mono', text: e.name })
    ]));
    row.appendChild(el('div', { class: 'mono muted', text: e.is_dir ? '' : fmtSize(e.size) }));
    row.appendChild(el('div', { class: 'mono muted', text: fmtTime(e.mod_time) }));

    // Action cell (auto list)
    if (isAuto) {
      const cell = el('div');
      if (!e.is_dir && e.import_id) {
        const actions = el('button', { class: 'btn', type: 'button', text: '⋮' });
        actions.style.padding = '6px 10px';
        actions.onclick = async (ev) => {
          ev.stopPropagation();
          const choice = prompt('Acción:\n1 = Borrar global (BD)\n2 = Borrado completo (BD+NZB+PAR2)\n\nEscribe 1 o 2');
          if (!choice) return;
          if (String(choice).trim() === '1') {
            const ok = confirm('¿Borrar global?\n\nDesaparece de auto+manual. No borra NZB/PAR2.');
            if (!ok) return;
            await apiPostJson('/api/v1/catalog/imports/delete', { id: e.import_id });
            await refreshList('auto');
            return;
          }
          if (String(choice).trim() === '2') {
            const ok = confirm('⚠ Borrado completo\n\nBD + mover NZB+PAR2 a .trash\n\n¿Continuar?');
            if (!ok) return;
            const typed = prompt('Escribe BORRAR para confirmar');
            if ((typed || '').trim().toUpperCase() !== 'BORRAR') return;
            await apiPostJson('/api/v1/catalog/imports/delete_full', { id: e.import_id });
            await refreshList('auto');
            return;
          }
        };
        cell.appendChild(actions);
      }
      row.appendChild(cell);
    }

    if (e.is_dir) {
      row.onclick = () => {
        if (isAuto) autoPath = e.path; else manPath = e.path;
        refreshList(kind).catch(err => setStatus(statusId, String(err)));
      };
    }

    list.appendChild(row);
  }

  // Guard: if user navigated out of expected root, snap back.
  if (!path.startsWith(root)) {
    if (isAuto) autoPath = root; else manPath = root;
  }

  setStatus(statusId, `OK (${(data.entries || []).length})`);
}

function goUp(kind) {
  if (kind === 'auto') {
    const root = AUTO_ROOT;
    if (autoPath === root) return;
    const p = autoPath.split('/').filter(Boolean);
    p.pop();
    autoPath = '/' + p.join('/');
    if (!autoPath.startsWith(root)) autoPath = root;
    refreshList('auto').catch(err => setStatus('autoStatus', String(err)));
    return;
  }

  // manual (FUSE)
  const root = MAN_ROOT;
  if (manPath === root) return;
  const p = manPath.split('/').filter(Boolean);
  p.pop();
  manPath = '/' + p.join('/');
  if (!manPath.startsWith(root)) manPath = root;
  refreshList('manual').catch(err => setStatus('manStatus', String(err)));
}

function setLibraryTab(which) {
  const autoPane = document.getElementById('autoPane');
  const manualPane = document.getElementById('manualPane');
  document.getElementById('tabAuto').classList.toggle('active', which === 'auto');
  document.getElementById('tabManual').classList.toggle('active', which === 'manual');
  autoPane.classList.toggle('hide', which !== 'auto');
  manualPane.classList.toggle('hide', which !== 'manual');
  if (which === 'auto') refreshList('auto').catch(err => setStatus('autoStatus', String(err)));
  if (which === 'manual') refreshList('manual').catch(err => setStatus('manStatus', String(err)));
}

async function restartNow() {
  const btn = document.getElementById('btnRestartTop');
  const old = btn.textContent;
  btn.textContent = 'Reiniciando...';
  btn.disabled = true;
  try {
    await apiPostJson('/api/v1/restart', {});
  } catch (e) {
    alert(String(e));
  } finally {
    setTimeout(() => { btn.textContent = old; btn.disabled = false; }, 6000);
  }
}

// HEALTH (NZB Repair)
async function refreshHealthScan() {
  const box = document.getElementById('healthList');
  const st = document.getElementById('healthStatus');
  const set = (t) => { if (st) st.textContent = t || ''; };
  if (!box) return;

  set('Cargando… (Loading)');
  box.innerHTML = '';
  try {
    const data = await apiGet('/api/v1/health/scan');
    const entries = (data && data.entries) ? data.entries : [];

    for (const e of entries) {
      const row = el('div', { class: 'listRow' });
      row.style.gridTemplateColumns = '1fr 110px 190px 120px';

      row.appendChild(el('div', { class: 'mono', text: e.path }));
      row.appendChild(el('div', { class: 'mono muted', text: fmtSize(e.size) }));
      row.appendChild(el('div', { class: 'mono muted', text: fmtTime(e.mod_time) }));

      const btn = el('button', { class: 'btn', type: 'button', text: 'Reparar (Repair)' });
      btn.onclick = async (ev) => {
        ev.stopPropagation();
        set('Encolando reparación… (Queueing)');
        try {
          await apiPostJson('/api/v1/jobs/enqueue/health-repair', { path: e.path });
          set('OK: job encolado (queued). Mira Logs para el detalle.');
        } catch (err) {
          set('Error: ' + String(err));
        }
      };
      const cell = el('div');
      cell.appendChild(btn);
      row.appendChild(cell);

      box.appendChild(row);
    }

    set(`OK (${entries.length})`);
  } catch (e) {
    set('Error: ' + String(e));
  }
}

// LOGS (Jobs)
function _safe(s) {
  return (s == null) ? '' : String(s);
}

async function refreshLogsJobs() {
  const box = document.getElementById('logsJobs');
  const st = document.getElementById('logsStatus');
  const set = (t) => { if (st) st.textContent = t || ''; };
  if (!box) return;

  set('Cargando… (Loading)');
  box.innerHTML = '';
  try {
    const jobs = await apiGet('/api/v1/jobs');
    for (const j of jobs) {
      const row = el('div', { class: 'listRow' });
      row.style.gridTemplateColumns = '90px 120px 110px 1fr 110px';

      row.appendChild(el('div', { class: 'mono', text: _safe(j.id).slice(0, 8) }));
      row.appendChild(el('div', { text: _safe(j.type) }));
      row.appendChild(el('div', { text: _safe(j.state) }));
      row.appendChild(el('div', { class: 'mono muted', text: _safe((j.params || {}).path || '') }));

      const btn = el('button', { class: 'btn', type: 'button', text: 'Ver (View)' });
      btn.onclick = async (ev) => {
        ev.stopPropagation();
        await loadJobLogs(j.id);
      };
      const cell = el('div');
      cell.appendChild(btn);
      row.appendChild(cell);

      row.onclick = () => loadJobLogs(j.id).catch(() => {});

      box.appendChild(row);
    }
    set(`OK (${(jobs || []).length})`);
  } catch (e) {
    set('Error: ' + String(e));
  }
}

async function loadJobLogs(jobId) {
  const out = document.getElementById('logsOut');
  const title = document.getElementById('logsTitle');
  const limit = document.getElementById('logsLimit');
  if (!out) return;

  const n = limit ? parseInt(limit.value || '400', 10) : 400;
  const lim = Number.isFinite(n) ? n : 400;
  out.textContent = 'Cargando logs…';
  if (title) title.textContent = `Job ${String(jobId).slice(0, 8)} (limit=${lim})`;

  const resp = await apiGet(`/api/v1/jobs/${jobId}/logs?limit=${encodeURIComponent(lim)}`);
  const lines = (resp && resp.lines) ? resp.lines : [];
  // API returns newest-first; show oldest-first.
  out.textContent = lines.slice().reverse().join('\n');
}

// SETTINGS (Ajustes) - Upload
function _val(id) {
  const n = document.getElementById(id);
  if (!n) return '';
  return String(n.value || '').trim();
}
function _int(id, defV) {
  const s = _val(id);
  const n = parseInt(s, 10);
  return Number.isFinite(n) ? n : defV;
}
function _bool(id) {
  const n = document.getElementById(id);
  return !!(n && n.checked);
}

async function loadUploadSettings() {
  const st = document.getElementById('setStatus');
  const set = (t) => { if (st) st.textContent = t || ''; };
  set('Cargando… (Loading)');
  const cfg = await apiGet('/api/v1/config');

  // Watch (Media uploads)
  document.getElementById('setWatchMediaEnabled').checked = !!(cfg.watch && cfg.watch.media && cfg.watch.media.enabled);
  document.getElementById('setWatchMediaDir').value = (cfg.watch && cfg.watch.media && cfg.watch.media.dir) ? cfg.watch.media.dir : '';
  document.getElementById('setWatchMediaRecursive').checked = !!(cfg.watch && cfg.watch.media && cfg.watch.media.recursive);

  // Watch (NZB import)
  document.getElementById('setWatchNZBEnabled').checked = !!(cfg.watch && cfg.watch.nzb && cfg.watch.nzb.enabled);
  document.getElementById('setWatchNZBDir').value = (cfg.watch && cfg.watch.nzb && cfg.watch.nzb.dir) ? cfg.watch.nzb.dir : '';
  document.getElementById('setWatchNZBRecursive').checked = !!(cfg.watch && cfg.watch.nzb && cfg.watch.nzb.recursive);

  // Provider
  document.getElementById('setUploadProvider').value = (cfg.upload && cfg.upload.provider) ? cfg.upload.provider : 'ngpost';

  // Upload NNTP (NgPost config reused for Nyuu)
  const n = (cfg.ngpost || {});
  document.getElementById('setNntpHost').value = n.host || '';
  document.getElementById('setNntpPort').value = (n.port != null) ? n.port : 563;
  document.getElementById('setNntpSSL').checked = (n.ssl !== false);
  document.getElementById('setNntpUser').value = n.user || '';
  document.getElementById('setNntpPass').value = n.pass || '';
  document.getElementById('setNntpConnections').value = (n.connections != null) ? n.connections : 20;
  document.getElementById('setNntpThreads').value = (n.threads != null) ? n.threads : 2;
  document.getElementById('setNntpGroups').value = n.groups || '';

  // Library-auto templates (display + preview)
  const L = (cfg.library || {});
  // library-auto is treated as always enabled.
  const libEn = document.getElementById('setLibEnabled');
  if (libEn) {
    libEn.checked = true;
    libEn.disabled = true;
  }
  document.getElementById('setLibUpper').checked = !!L.uppercase_folders;

  const setText = (id, t) => {
    const el = document.getElementById(id);
    if (el) el.textContent = String(t || '');
  };
  const setVal = (id, t) => {
    const el = document.getElementById(id);
    if (el) el.value = String(t || '').trim();
  };

  setText('tplMovieDir', L.movie_dir_template || '');
  setText('tplMovieFile', L.movie_file_template || '');
  setText('tplSeriesDir', L.series_dir_template || '');
  setText('tplSeasonDir', L.season_folder_template || '');
  setText('tplSeriesFile', L.series_file_template || '');

  setVal('setLibMovieDirT', L.movie_dir_template || '');
  setVal('setLibMovieFileT', L.movie_file_template || '');
  setVal('setLibSeriesDirT', L.series_dir_template || '');
  setVal('setLibSeasonT', L.season_folder_template || '');
  setVal('setLibSeriesFileT', L.series_file_template || '');

  // Copy buttons
  const bindCopy = (btnId, srcId) => {
    const b = document.getElementById(btnId);
    if (!b) return;
    b.onclick = async () => {
      const src = document.getElementById(srcId);
      const txt = src ? (src.textContent || '') : '';
      try { await navigator.clipboard.writeText(txt); } catch (_) {}
    };
  };
  bindCopy('btnCopyMovieDir', 'tplMovieDir');
  bindCopy('btnCopyMovieFile', 'tplMovieFile');
  bindCopy('btnCopySeriesDir', 'tplSeriesDir');
  bindCopy('btnCopySeasonDir', 'tplSeasonDir');
  bindCopy('btnCopySeriesFile', 'tplSeriesFile');

  // Preview (realistic examples)
  try {
    const prev = await apiGet('/api/v1/library/templates/preview');
    setText('tplPrevMovie', (prev && prev.movie && prev.movie.example_file) ? prev.movie.example_file : '');
    setText('tplPrevSeries', (prev && prev.series && prev.series.example_file) ? prev.series.example_file : '');
  } catch (e) {
    // ignore preview failure
  }

  // Plex (solo library-auto)
  const p = (cfg.plex || {});
  document.getElementById('setPlexEnabled').checked = !!p.enabled;
  document.getElementById('setPlexRefreshOnImport').checked = !!p.refresh_on_import;
  document.getElementById('setPlexBaseURL').value = p.base_url || '';
  document.getElementById('setPlexToken').value = p.token || '';
  document.getElementById('setPlexRoot').value = p.plex_root || '';

  // Health
  const h = (cfg.health || {});
  document.getElementById('setHealthEnabled').checked = !!h.enabled;
  const hs = (h.scan || {});
  const hl = (h.lock || {});
  document.getElementById('setHealthScanEnabled').checked = !!hs.enabled;
  document.getElementById('setHealthAutoRepair').checked = (hs.auto_repair !== false);
  document.getElementById('setHealthMaxDurationMins').value = (hs.max_duration_minutes != null) ? hs.max_duration_minutes : 180;
  document.getElementById('setHealthChunkEveryHours').value = (hs.chunk_every_hours != null) ? hs.chunk_every_hours : 24;
  document.getElementById('setHealthIntervalHours').value = (hs.interval_hours != null) ? hs.interval_hours : 24;
  document.getElementById('setHealthLockTTLHours').value = (hl.lock_ttl_hours != null) ? hl.lock_ttl_hours : 6;

  // Download NNTP
  const d = (cfg.download || {});
  document.getElementById('setDL_ENABLED').checked = !!d.enabled;
  document.getElementById('setDL_HOST').value = d.host || '';
  document.getElementById('setDL_PORT').value = (d.port != null) ? d.port : 563;
  document.getElementById('setDL_SSL').checked = (d.ssl !== false);
  document.getElementById('setDL_USER').value = d.user || '';
  document.getElementById('setDL_PASS').value = d.pass || '';
  document.getElementById('setDL_CONN').value = (d.connections != null) ? d.connections : 20;
  document.getElementById('setDL_PREFETCH').value = (d.prefetch_segments != null) ? d.prefetch_segments : 2;

  // TMDB
  const t = ((cfg.metadata || {}).tmdb || {});
  document.getElementById('setTMDBEnabled').checked = !!t.enabled;
  document.getElementById('setTMDBApiKey').value = t.api_key || '';
  document.getElementById('setTMDBLanguage').value = t.language || 'es-ES';

  set('');
}

async function saveUploadSettings() {
  const st = document.getElementById('setStatus');
  const set = (t) => { if (st) st.textContent = t || ''; };
  const btn = document.getElementById('btnSetSave');
  const old = btn ? btn.textContent : '';
  if (btn) { btn.disabled = true; btn.textContent = 'Guardando…'; }

  try {
    set('Validando…');
    const cfg = await apiGet('/api/v1/config');

    // Watch media
    cfg.watch = cfg.watch || {};
    cfg.watch.media = cfg.watch.media || {};
    cfg.watch.media.enabled = _bool('setWatchMediaEnabled');
    cfg.watch.media.dir = _val('setWatchMediaDir');
    cfg.watch.media.recursive = _bool('setWatchMediaRecursive');

    // Watch NZB
    cfg.watch.nzb = cfg.watch.nzb || {};
    cfg.watch.nzb.enabled = _bool('setWatchNZBEnabled');
    cfg.watch.nzb.dir = _val('setWatchNZBDir');
    cfg.watch.nzb.recursive = _bool('setWatchNZBRecursive');

    // Provider
    cfg.upload = cfg.upload || {};
    cfg.upload.provider = _val('setUploadProvider') || 'ngpost';

    // NNTP upload settings (ngpost section)
    cfg.ngpost = cfg.ngpost || {};
    cfg.ngpost.enabled = true;
    cfg.ngpost.host = _val('setNntpHost');
    cfg.ngpost.port = _int('setNntpPort', 563);
    cfg.ngpost.ssl = _bool('setNntpSSL');
    cfg.ngpost.user = _val('setNntpUser');
    cfg.ngpost.pass = _val('setNntpPass');
    cfg.ngpost.connections = _int('setNntpConnections', 20);
    cfg.ngpost.threads = _int('setNntpThreads', 2);
    cfg.ngpost.groups = _val('setNntpGroups');

    // Library-auto
    cfg.library = cfg.library || {};
    // library-auto always enabled (ignore UI checkbox)
    cfg.library.enabled = true;
    cfg.library.uppercase_folders = _bool('setLibUpper');
    cfg.library.movie_dir_template = _val('setLibMovieDirT');
    cfg.library.movie_file_template = _val('setLibMovieFileT');
    cfg.library.series_dir_template = _val('setLibSeriesDirT');
    cfg.library.season_folder_template = _val('setLibSeasonT');
    cfg.library.series_file_template = _val('setLibSeriesFileT');

    // Plex (solo library-auto)
    cfg.plex = cfg.plex || {};
    cfg.plex.enabled = _bool('setPlexEnabled');
    cfg.plex.refresh_on_import = _bool('setPlexRefreshOnImport');
    cfg.plex.base_url = _val('setPlexBaseURL');
    cfg.plex.token = _val('setPlexToken');
    cfg.plex.plex_root = _val('setPlexRoot');

    // Health
    cfg.health = cfg.health || {};
    cfg.health.enabled = _bool('setHealthEnabled');
    cfg.health.scan = cfg.health.scan || {};
    cfg.health.scan.enabled = _bool('setHealthScanEnabled');
    cfg.health.scan.auto_repair = _bool('setHealthAutoRepair');
    cfg.health.scan.max_duration_minutes = _int('setHealthMaxDurationMins', 180);
    cfg.health.scan.chunk_every_hours = _int('setHealthChunkEveryHours', 24);
    cfg.health.scan.interval_hours = _int('setHealthIntervalHours', 24);
    cfg.health.lock = cfg.health.lock || {};
    cfg.health.lock.lock_ttl_hours = _int('setHealthLockTTLHours', 6);

    // Download NNTP
    cfg.download = cfg.download || {};
    cfg.download.enabled = _bool('setDL_ENABLED');
    cfg.download.host = _val('setDL_HOST');
    cfg.download.port = _int('setDL_PORT', 563);
    cfg.download.ssl = _bool('setDL_SSL');
    cfg.download.user = _val('setDL_USER');
    cfg.download.pass = _val('setDL_PASS');
    cfg.download.connections = _int('setDL_CONN', 20);
    cfg.download.prefetch_segments = _int('setDL_PREFETCH', 2);

    // TMDB
    cfg.metadata = cfg.metadata || {};
    cfg.metadata.tmdb = cfg.metadata.tmdb || {};
    cfg.metadata.tmdb.enabled = _bool('setTMDBEnabled');
    cfg.metadata.tmdb.api_key = _val('setTMDBApiKey');
    cfg.metadata.tmdb.language = _val('setTMDBLanguage') || 'es-ES';

    set('Guardando… (Saving)');
    await apiPutJson('/api/v1/config', cfg);

    set('Aplicado. Reiniciando… (Restarting)');
    await apiPostJson('/api/v1/restart', {});
    set('Reiniciando…');
  } catch (e) {
    set('Error: ' + String(e));
    throw e;
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = old || 'Guardar y reiniciar'; }
  }
}

let reviewSelected = null;

function showReviewBox(show) {
  const box = document.getElementById('reviewBox');
  if (!box) return;
  box.style.display = show ? '' : 'none';
}

function qualityFromGuess(q) {
  const low = String(q || '').toLowerCase();
  if (low.includes('2160') || low.includes('4k')) return '2160';
  return '1080';
}

async function refreshImports() {
  const list = document.getElementById('importsList');
  if (!list) return;
  list.innerHTML = '';
  const items = await apiGet('/api/v1/catalog/imports');
  if (!items || items.length === 0) {
    list.innerHTML = '<div class="muted" style="padding:10px">No hay imports.</div>';
    return;
  }
  for (const it of items) {
    const row = el('div', { class: 'listRow' });
    const name = (it.path || '').split('/').pop() || it.id.slice(0, 8);
    row.appendChild(el('div', { class: 'mono', text: name }));
    row.appendChild(el('div', { class: 'mono muted', text: fmtSize(it.total_bytes || 0) }));
    row.appendChild(el('div', { class: 'mono muted', text: fmtTime(it.imported_at) }));

    const cell = el('div');
    const btnDel = el('button', { class: 'btn danger', text: 'Borrar global' });
    btnDel.onclick = async (ev) => {
      ev.stopPropagation();
      const ok = confirm('¿Eliminar este import de la biblioteca (global)?\n\n- Desaparece de library-auto y library-manual\n- NO borra el NZB ni PAR2 del disco\n\n¿Continuar?');
      if (!ok) return;
      await apiPostJson('/api/v1/catalog/imports/delete', { id: it.id });
      await refreshImports();
      await refreshList('auto');
    };

    const btnFull = el('button', { class: 'btn danger', text: 'Borrado completo' });
    btnFull.onclick = async (ev) => {
      ev.stopPropagation();
      const ok = confirm('⚠ Borrado completo (irreversible)\n\n- Borra de la BD\n- Mueve NZB a /host/inbox/.trash\n- Mueve PAR2 a /host/inbox/.trash\n\n¿Continuar?');
      if (!ok) return;
      const typed = prompt('Escribe BORRAR para confirmar');
      if ((typed || '').trim().toUpperCase() !== 'BORRAR') return;
      await apiPostJson('/api/v1/catalog/imports/delete_full', { id: it.id });
      await refreshImports();
      await refreshList('auto');
    };

    cell.appendChild(btnDel);
    cell.appendChild(btnFull);
    row.appendChild(cell);

    list.appendChild(row);
  }
}

async function refreshReview() {
  try {
    const r = await apiGet('/api/v1/library/review');
    const items = (r.items || []);
    const list = document.getElementById('reviewList');
    list.innerHTML = '';

    // Keep the review box visible so users know where to look,
    // even when there are no pending items.
    showReviewBox(true);
    if (items.length === 0) {
      list.innerHTML = '<div class="muted" style="padding:10px">No hay elementos pendientes de revisión.</div>';
      return;
    }

    for (const it of items) {
      const row = el('div', { class: 'listRow' });
      row.appendChild(el('div', { class: 'name' }, [
        el('div', { class: 'icon', text: '⚠' }),
        el('div', { class: 'mono', text: it.filename || '(file)' })
      ]));
      row.appendChild(el('div', { class: 'mono muted', text: `${it.guess_title || ''} ${it.guess_year || ''} ${it.guess_quality || ''}`.trim() }));

      const btnFix = el('button', { class: 'btn', text: 'Corregir' });
      btnFix.onclick = () => {
        reviewSelected = it;
        document.getElementById('fixSel').textContent = `import=${it.import_id.slice(0,8)} idx=${it.file_idx}`;
        document.getElementById('fixTitle').value = (it.guess_title || '').replace(/\s+\d{4}\b/, '').trim() || '';
        document.getElementById('fixYear').value = (it.guess_year && it.guess_year > 0) ? it.guess_year : '';
        const q = qualityFromGuess(it.guess_quality);
        document.getElementById('fixQuality').value = q;
        document.getElementById('fixApplyAll').checked = false;
        document.getElementById('fixQualityCustomWrap').style.display = 'none';
        document.getElementById('fixQualityCustom').value = '';
        document.getElementById('fixStatus').textContent = '';
      };

      const btnDismiss = el('button', { class: 'btn danger', text: 'Ignorar' });
      btnDismiss.onclick = async () => {
        if (!confirm('¿Ignorar este aviso? (Dismiss)')) return;
        try {
          await apiPostJson('/api/v1/library/review/dismiss', { import_id: it.import_id, file_idx: it.file_idx });
          await refreshReview();
        } catch (e) {
          alert(String(e));
        }
      };

      const td = el('div', {}, []);
      td.appendChild(btnFix);
      td.appendChild(btnDismiss);
      row.appendChild(td);
      list.appendChild(row);
    }
  } catch (e) {
    // hide box on errors, but don’t break UI
    showReviewBox(false);
  }
}

async function applyFix() {
  const st = document.getElementById('fixStatus');
  if (!reviewSelected) {
    st.textContent = 'Selecciona un archivo primero.';
    return;
  }
  const title = (document.getElementById('fixTitle').value || '').trim();
  const year = Number(document.getElementById('fixYear').value || 0);
  const qSel = document.getElementById('fixQuality').value;
  let quality = qSel;
  if (qSel === 'custom') {
    quality = (document.getElementById('fixQualityCustom').value || '').trim();
  }
  if (!title || !quality) {
    st.textContent = 'Title y Quality son obligatorios.';
    return;
  }
  st.textContent = 'Aplicando...';
  try {
    const applyAll = !!document.getElementById('fixApplyAll').checked;
    if (applyAll) {
      await apiPostJson('/api/v1/library/override/import', {
        import_id: reviewSelected.import_id,
        title,
        year: isFinite(year) ? year : 0,
        quality,
        tmdb_id: 0,
      });
      st.textContent = 'OK. Aplicado a todo el import.';
    } else {
      await apiPostJson('/api/v1/library/override', {
        import_id: reviewSelected.import_id,
        file_idx: reviewSelected.file_idx,
        kind: 'movie',
        title,
        year: isFinite(year) ? year : 0,
        quality,
        tmdb_id: 0,
      });
      st.textContent = 'OK. Ya debería aparecer en library-auto.';
    }
    // refresh review + auto list
    reviewSelected = null;
    document.getElementById('fixSel').textContent = '(selecciona un archivo arriba)';
    await refreshReview();
    await refreshList('auto');
  } catch (e) {
    st.textContent = 'Error: ' + String(e);
  }
}

// Import explorer (NZB inbox)
const IMP_ROOT = '/inbox/nzb';
let impPath = IMP_ROOT;
let impSelected = '';

async function refreshImport() {
  const statusId = 'impStatus';
  setStatus(statusId, 'Cargando...');
  renderCrumbs('impCrumbs', impPath, (picked) => {
    impPath = picked;
    refreshImport().catch(err => setStatus(statusId, String(err)));
  });

  const data = await apiGet(`/api/v1/hostfs/list?path=${encodeURIComponent(impPath)}`);
  const list = document.getElementById('impList');
  list.innerHTML = '';

  for (const e of (data.entries || [])) {
    const row = el('div', { class: 'listRow' });
    const icon = e.is_dir ? 'DIR' : 'NZB';
    row.appendChild(el('div', { class: 'name' }, [
      el('div', { class: 'icon', text: icon }),
      el('div', { class: e.is_dir ? '' : 'mono', text: e.name })
    ]));
    row.appendChild(el('div', { class: 'mono muted', text: e.is_dir ? '' : fmtSize(e.size) }));
    row.appendChild(el('div', { class: 'mono muted', text: fmtTime(e.mod_time) }));

    if (e.is_dir) {
      row.onclick = () => {
        impPath = e.path;
        refreshImport().catch(err => setStatus(statusId, String(err)));
      };
    } else {
      row.onclick = () => {
        impSelected = e.path;
        document.getElementById('impSel').textContent = `Seleccionado (Selected): ${e.name}`;
        const btn = document.getElementById('btnImpEnqueue');
        btn.disabled = !e.name.toLowerCase().endsWith('.nzb');
      };
    }

    list.appendChild(row);
  }

  if (!impPath.startsWith(IMP_ROOT)) impPath = IMP_ROOT;
  setStatus(statusId, `OK (${(data.entries || []).length})`);
}

function goUpImport() {
  if (impPath === IMP_ROOT) return;
  const p = impPath.split('/').filter(Boolean);
  p.pop();
  impPath = '/' + p.join('/');
  if (!impPath.startsWith(IMP_ROOT)) impPath = IMP_ROOT;
  refreshImport().catch(err => setStatus('impStatus', String(err)));
}

async function enqueueSelectedImport() {
  if (!impSelected) return;
  const btn = document.getElementById('btnImpEnqueue');
  btn.disabled = true;
  try {
    await apiPostJson('/api/v1/jobs/enqueue/import', { path: impSelected });
    setStatus('impStatus', 'Encolado (Queued)');
  } catch (e) {
    alert(String(e));
  } finally {
    btn.disabled = false;
  }
}

window.addEventListener('DOMContentLoaded', () => {
  // Nav
  for (const item of document.querySelectorAll('.navItem')) {
    item.onclick = () => showPage(item.dataset.page);
  }
  showPage('library');

  // Tabs
  document.getElementById('tabAuto').onclick = () => setLibraryTab('auto');
  document.getElementById('tabManual').onclick = () => setLibraryTab('manual');
  setLibraryTab('auto');

  // Imports UI
  if (document.getElementById('btnImportsRefresh')) {
    document.getElementById('btnImportsRefresh').onclick = () => refreshImports().catch(() => {});
  }

  // Review UI
  document.getElementById('btnReviewRefresh').onclick = () => refreshReview().catch(() => {});
  document.getElementById('btnFixApply').onclick = () => applyFix().catch(() => {});
  document.getElementById('fixQuality').onchange = () => {
    const v = document.getElementById('fixQuality').value;
    document.getElementById('fixQualityCustomWrap').style.display = (v === 'custom') ? '' : 'none';
  };

  // Controls
  document.getElementById('btnAutoRefresh').onclick = () => refreshList('auto').catch(err => setStatus('autoStatus', String(err)));
  document.getElementById('btnAutoUp').onclick = () => goUp('auto');
  document.getElementById('btnManRefresh').onclick = () => refreshList('manual').catch(err => setStatus('manStatus', String(err)));
  document.getElementById('btnManUp').onclick = () => goUp('manual');

  // Import page
  if (document.getElementById('btnImpRefresh')) {
    document.getElementById('btnImpRefresh').onclick = () => refreshImport().catch(err => setStatus('impStatus', String(err)));
    document.getElementById('btnImpUp').onclick = () => goUpImport();
    document.getElementById('btnImpEnqueue').onclick = () => enqueueSelectedImport().catch(err => alert(err));

    // Upload NZB
    const up = document.getElementById('impUpload');
    if (up) {
      up.onchange = async () => {
        const f = up.files && up.files[0];
        if (!f) return;
        const fd = new FormData();
        fd.append('file', f, f.name);
        setStatus('impStatus', 'Subiendo a NZB inbox…');
        const r = await fetch('/api/v1/import/nzb/upload', { method: 'POST', body: fd });
        if (!r.ok) throw new Error(await r.text());
        setStatus('impStatus', 'OK (copiado a inbox)');
        up.value = '';
        await refreshImport();
      };
    }

    // init
    document.getElementById('btnImpEnqueue').disabled = true;
    refreshImport().catch(() => {});
  }

  // Upload media on Upload page
  const upMedia = document.getElementById('upUpload');
  if (upMedia) {
    upMedia.onchange = async () => {
      const st = document.getElementById('upActiveStatus');
      const set = (t) => { if (st) st.textContent = t || ''; };
      try {
        const f = upMedia.files && upMedia.files[0];
        if (!f) return;
        const fd = new FormData();
        fd.append('file', f, f.name);
        set('Subiendo media manual… (Uploading manual)');
        const r = await fetch('/api/v1/upload/media/manual', { method: 'POST', body: fd });
        if (!r.ok) throw new Error(await r.text());
        set('OK: encolado. (Queued)');
        upMedia.value = '';
        // Upload page will pick it up via polling.
      } catch (e) {
        set('Error: ' + String(e));
        throw e;
      }
    };
  }

  document.getElementById('btnRestartTop').onclick = () => restartNow();

  // Settings
  if (document.getElementById('btnSetSave')) {
    document.getElementById('btnSetSave').onclick = () => saveUploadSettings().catch(() => {});
    document.getElementById('btnSetReload').onclick = () => loadUploadSettings().catch(() => {});
  }
  if (document.getElementById('btnDBReset')) {
    document.getElementById('btnDBReset').onclick = async () => {
      const ok = confirm('¿Borrar SOLO la base de datos?\n\n- Se perderán imports/overrides/jobs\n- La configuración NO se borra\n- Reinicia el contenedor\n\n¿Continuar?');
      if (!ok) return;
      const st = document.getElementById('setStatus');
      if (st) st.textContent = 'Reseteando BD… (Resetting DB)';
      await apiPostJson('/api/v1/db/reset', {});
      if (st) st.textContent = 'Reiniciando… (Restarting)';
      await apiPostJson('/api/v1/restart', {});
    };
  }

  // Health
  if (document.getElementById('btnHealthScan')) {
    document.getElementById('btnHealthScan').onclick = () => refreshHealthScan().catch(() => {});
  }

  // Logs
  if (document.getElementById('btnLogsRefresh')) {
    document.getElementById('btnLogsRefresh').onclick = () => refreshLogsJobs().catch(() => {});
  }

  // Load imports + review initially
  refreshImports().catch(() => {});
  refreshReview().catch(() => {});
});
