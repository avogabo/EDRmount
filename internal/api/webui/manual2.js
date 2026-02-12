// Manual library UI for the new layout (DB-backed)

async function refreshManual() {
  setStatus('manStatus', 'Cargando...');

  // Breadcrumb
  const crumbsBox = document.getElementById('manCrumbs');
  crumbsBox.innerHTML = '';
  const path = await apiGet(`/api/v1/manual/path?dir_id=${encodeURIComponent(manualDirId)}`);
  let lastId = 'root';
  for (let i = 0; i < path.length; i++) {
    const d = path[i];
    const id = d.id;
    const b = el('button', { class: 'crumb', type: 'button', text: d.name });
    b.onclick = () => {
      manualDirId = id;
      refreshManual().catch(err => setStatus('manStatus', String(err)));
    };
    crumbsBox.appendChild(b);
    if (i !== path.length - 1) crumbsBox.appendChild(el('span', { class: 'crumbSep', text: '›' }));
    lastId = id;
  }

  // Load dirs + items
  const dirs = await apiGet(`/api/v1/manual/dirs?parent_id=${encodeURIComponent(manualDirId)}`);
  const items = await apiGet(`/api/v1/manual/items?dir_id=${encodeURIComponent(manualDirId)}`);

  // Optional controls (move/edit) exist only on some UI layouts.
  // If they are missing, we keep Manual as a simple browser (no crash).
  const moveSel = document.getElementById('manMoveTo');
  if (moveSel) {
    // Load all dirs once (for move-to)
    manDirsAll = await apiGet('/api/v1/manual/dirs/all');
    moveSel.innerHTML = '';
    moveSel.appendChild(el('option', { value: '', text: '(mantener / keep current)' }));
    for (const d of manDirsAll) {
      const opt = document.createElement('option');
      opt.value = d.id;
      // show a simple path hint
      opt.textContent = d.name + `  (${d.id.slice(0,6)})`;
      moveSel.appendChild(opt);
    }
  }

  // Render list (folders first, then items)
  const list = document.getElementById('manList');
  list.innerHTML = '';

  for (const d of dirs) {
    const row = el('div', { class: 'listRow' });
    row.appendChild(el('div', { class: 'name' }, [
      el('div', { class: 'icon', text: 'DIR' }),
      el('div', { text: d.name })
    ]));
    row.appendChild(el('div', { class: 'mono muted', text: 'folder' }));
    row.appendChild(el('div', { class: 'mono muted', text: d.id.slice(0,8) }));
    row.onclick = () => {
      manualDirId = d.id;
      refreshManual().catch(err => setStatus('manStatus', String(err)));
    };
    list.appendChild(row);
  }

  for (const it of items) {
    const row = el('div', { class: 'listRow' });
    row.appendChild(el('div', { class: 'name' }, [
      el('div', { class: 'icon', text: 'IT' }),
      el('div', { class: 'mono', text: it.label || it.filename || '(item)' })
    ]));
    row.appendChild(el('div', { class: 'mono muted', text: 'item' }));
    row.appendChild(el('div', { class: 'mono muted', text: `${(it.bytes||0)} bytes` }));

    row.onclick = () => {
      manSelectedItemId = it.id;
      const sel = document.getElementById('manSel');
      if (sel) sel.textContent = `id=${it.id.slice(0,8)} import=${(it.import_id||'').slice(0,8)} idx=${it.file_idx}`;
      const edit = document.getElementById('manEditLabel');
      if (edit) edit.value = it.label || '';
      const mv = document.getElementById('manMoveTo');
      if (mv) mv.value = '';
      setStatus('manActionStatus', '');
    };

    list.appendChild(row);
  }

  setStatus('manStatus', `OK (${dirs.length} folders, ${items.length} items)`);
}

async function createManualFolder2() {
  const name = (document.getElementById('manNewFolder').value || '').trim();
  if (!name) return;
  document.getElementById('manCreateStatus').textContent = 'Creando...';
  try {
    await apiPostJson('/api/v1/manual/dirs', { parent_id: manualDirId, name });
    document.getElementById('manNewFolder').value = '';
    document.getElementById('manCreateStatus').textContent = 'Creado.';
    await refreshManual();
  } catch (e) {
    document.getElementById('manCreateStatus').textContent = 'Error: ' + String(e);
  }
}

async function saveManualItem2() {
  if (!manSelectedItemId) {
    setStatus('manActionStatus', 'Selecciona un item primero.');
    return;
  }
  setStatus('manActionStatus', 'Guardando...');
  try {
    const label = (document.getElementById('manEditLabel').value || '').trim();
    const moveTo = (document.getElementById('manMoveTo').value || '').trim();
    const body = {};
    if (label) body.label = label;
    if (moveTo) body.dir_id = moveTo;
    await apiPutJson(`/api/v1/manual/items/${encodeURIComponent(manSelectedItemId)}`, body);
    setStatus('manActionStatus', 'Guardado (saved).');
    await refreshManual();
  } catch (e) {
    setStatus('manActionStatus', 'Error: ' + String(e));
  }
}

async function deleteManualItem2() {
  if (!manSelectedItemId) {
    setStatus('manActionStatus', 'Selecciona un item primero.');
    return;
  }
  if (!confirm('¿Borrar item? (Delete item)')) return;
  setStatus('manActionStatus', 'Borrando...');
  try {
    const r = await fetch(`/api/v1/manual/items/${encodeURIComponent(manSelectedItemId)}`, { method: 'DELETE' });
    if (!r.ok) throw new Error(await r.text());
    manSelectedItemId = '';
    document.getElementById('manSel').textContent = '(ninguno seleccionado)';
    document.getElementById('manEditLabel').value = '';
    document.getElementById('manMoveTo').value = '';
    setStatus('manActionStatus', 'Borrado (deleted).');
    await refreshManual();
  } catch (e) {
    setStatus('manActionStatus', 'Error: ' + String(e));
  }
}
