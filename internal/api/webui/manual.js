function el(tag, attrs = {}, children = []) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') n.className = v;
    else if (k === 'text') n.textContent = v;
    else if (k === 'html') n.innerHTML = v;
    else if (k.startsWith('on') && typeof v === 'function') n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  for (const c of children) n.appendChild(c);
  return n;
}

async function loadImportFilesForManual() {
  const box = document.getElementById('manImportFiles');
  const status = document.getElementById('manStatus');
  const dir = (document.getElementById('man_dir').value || 'root').trim();
  const importId = document.getElementById('man_pick_import').value;
  box.innerHTML = '';

  if (!importId) {
    box.textContent = 'No import selected.';
    return;
  }
  status.textContent = 'Cargando ficheros del import...';
  try {
    const files = await apiGet(`/api/v1/catalog/imports/${importId}/files`);

    const table = el('table', { class: 'tbl' });
    const thead = el('thead');
    thead.appendChild(el('tr', {}, [
      el('th', { text: 'idx' }),
      el('th', { text: 'filename' }),
      el('th', { text: 'bytes' }),
      el('th', { text: 'add to current folder' }),
    ]));
    table.appendChild(thead);

    const tbody = el('tbody');
    for (const f of files) {
      const tr = el('tr');
      tr.appendChild(el('td', { class: 'mono', text: String(f.idx) }));
      tr.appendChild(el('td', { class: 'mono', text: f.filename || '' }));
      tr.appendChild(el('td', { class: 'mono', text: String(f.total_bytes || 0) }));
      const btn = el('button', { class: 'btn', text: 'Add' });
      btn.onclick = async () => {
        status.textContent = 'Adding...';
        try {
          const r = await fetch('/api/v1/manual/items', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
            body: JSON.stringify({
              dir_id: dir,
              import_id: importId,
              file_idx: Number(f.idx || 0),
              label: (f.filename || `idx ${f.idx}`)
            })
          });
          if (!r.ok) throw new Error(await r.text());
          status.textContent = 'Añadido.';
          await refreshManual();
        } catch (e) {
          status.textContent = 'Error: ' + String(e);
        }
      };
      const td = el('td');
      td.appendChild(btn);
      tr.appendChild(td);
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    box.appendChild(table);

    status.textContent = `Loaded ${files.length} files.`;
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

let __manualDirsCache = null;
let __manualDirsCacheTs = 0;

async function loadManualDirsAll(force = false) {
  const now = Date.now();
  if (!force && __manualDirsCache && (now - __manualDirsCacheTs) < 15000) {
    return __manualDirsCache;
  }
  const dirs = await apiGet('/api/v1/manual/dirs/all');
  __manualDirsCache = dirs;
  __manualDirsCacheTs = now;
  return dirs;
}

function buildDirPaths(dirs) {
  const byId = new Map();
  for (const d of dirs) byId.set(d.id, d);

  const memo = new Map();
  const pathOf = (id) => {
    if (id === 'root') return 'root';
    if (memo.has(id)) return memo.get(id);
    const d = byId.get(id);
    if (!d) return id;
    const parent = d.parent_id || 'root';
    const p = parent === 'root' ? `root/${d.name}` : `${pathOf(parent)}/${d.name}`;
    memo.set(id, p);
    return p;
  };

  const out = [];
  for (const d of dirs) {
    out.push({ id: d.id, path: pathOf(d.id) });
  }
  out.sort((a, b) => a.path.localeCompare(b.path));
  return out;
}

async function refreshMovePicker() {
  const sel = document.getElementById('man_item_move');
  const cur = sel.value;
  sel.innerHTML = '';
  sel.appendChild(el('option', { value: '', text: '(keep current)' }));

  // always allow moving to root
  sel.appendChild(el('option', { value: 'root', text: 'root' }));

  try {
    const dirs = await loadManualDirsAll(false);
    const paths = buildDirPaths(dirs);
    for (const p of paths) {
      sel.appendChild(el('option', { value: p.id, text: p.path }));
    }
  } catch (e) {
    // ignore; picker will just have root
  }

  if (cur) sel.value = cur;
}

async function updateManualItem() {
  const status = document.getElementById('manStatus');
  const id = (document.getElementById('man_item_id').value || '').trim();
  const label = (document.getElementById('man_item_label').value || '').trim();
  const dirId = (document.getElementById('man_item_move').value || '').trim();
  if (!id) {
    status.textContent = 'Falta el id del item.';
    return;
  }
  status.textContent = 'Actualizando item...';
  try {
    const body = {};
    if (label) body.label = label;
    if (dirId) body.dir_id = dirId;
    const r = await fetch(`/api/v1/manual/items/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify(body)
    });
    if (!r.ok) throw new Error(await r.text());
    status.textContent = 'Actualizado.';
    // reset move picker to default after a move
    document.getElementById('man_item_move').value = '';
    await refreshManual();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function deleteManualItem() {
  const status = document.getElementById('manStatus');
  const id = (document.getElementById('man_item_id').value || '').trim();
  if (!id) {
    status.textContent = 'Falta el id del item.';
    return;
  }
  status.textContent = 'Borrando item...';
  try {
    const r = await fetch(`/api/v1/manual/items/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      headers: { 'Accept': 'application/json' },
    });
    if (!r.ok) throw new Error(await r.text());
    status.textContent = 'Borrado.';
    document.getElementById('man_item_id').value = '';
    document.getElementById('man_item_label').value = '';
    document.getElementById('man_item_move').value = '';
    await refreshManual();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function renderManualBreadcrumb(dir) {
  const box = document.getElementById('manBreadcrumb');
  const btnUp = document.getElementById('btnManUp');
  box.innerHTML = '';

  const path = await apiGet(`/api/v1/manual/path?dir_id=${encodeURIComponent(dir || 'root')}`);
  // path is an array from root -> current
  const wrap = el('div', { class: 'row' });
  wrap.style.gap = '6px';

  for (let i = 0; i < path.length; i++) {
    const seg = path[i];
    if (i > 0) wrap.appendChild(el('span', { class: 'muted', text: '/' }));

    const name = seg.id === 'root' ? 'root' : (seg.name || seg.id);
    const b = el('button', { class: 'btn', text: name });
    b.style.padding = '4px 8px';
    b.title = seg.id;
    b.onclick = async () => {
      document.getElementById('man_dir').value = seg.id;
      await refreshManual();
    };
    wrap.appendChild(b);
  }
  box.appendChild(wrap);

  // Up button
  if (path.length <= 1) {
    btnUp.disabled = true;
    btnUp.title = '';
    btnUp.onclick = null;
  } else {
    const parent = path[path.length - 2];
    btnUp.disabled = false;
    btnUp.title = `Go to parent (${parent.id})`;
    btnUp.onclick = async () => {
      document.getElementById('man_dir').value = parent.id;
      await refreshManual();
    };
  }
}

async function refreshManual() {
  const status = document.getElementById('manStatus');
  const dir = (document.getElementById('man_dir').value || 'root').trim();
  const boxFolders = document.getElementById('manFolders');
  const boxItems = document.getElementById('manItems');
  boxFolders.innerHTML = '';
  boxItems.innerHTML = '';

  status.textContent = 'Cargando...';
  try {
    await renderManualBreadcrumb(dir);
    await refreshMovePicker();

    const dirs = await apiGet(`/api/v1/manual/dirs?parent_id=${encodeURIComponent(dir)}`);
    const items = await apiGet(`/api/v1/manual/items?dir_id=${encodeURIComponent(dir)}`);

    // folders list (explorer-ish)
    // - single click: if an item is selected, move it into that folder
    // - otherwise: navigate into the folder
    const ul = el('ul');
    async function moveItemToFolder(itemId, folderId, folderName) {
      status.textContent = `Moviendo item a “${folderName}”...`;
      const r = await fetch(`/api/v1/manual/items/${encodeURIComponent(itemId)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
        body: JSON.stringify({ dir_id: folderId })
      });
      if (!r.ok) throw new Error(await r.text());
      status.textContent = 'Movido.';
      document.getElementById('man_item_move').value = '';
      await refreshManual();
    }

    for (const d of dirs) {
      const li = el('li');
      const btn = el('button', { class: 'btn', text: d.name });
      btn.title = d.id;

      // Desktop UX:
      // - click: navigate
      // - drag&drop: move selected/dragged item onto folder
      btn.onclick = async () => {
        document.getElementById('man_dir').value = d.id;
        await refreshManual();
      };

      btn.addEventListener('dragover', (ev) => {
        ev.preventDefault();
        btn.style.outline = '2px solid #6aa9ff';
        btn.style.outlineOffset = '2px';
      });
      btn.addEventListener('dragleave', () => {
        btn.style.outline = '';
        btn.style.outlineOffset = '';
      });
      btn.addEventListener('drop', async (ev) => {
        ev.preventDefault();
        btn.style.outline = '';
        btn.style.outlineOffset = '';

        const draggedId = (ev.dataTransfer && ev.dataTransfer.getData('text/edrmount-item-id')) || '';
        const fallbackSelected = (document.getElementById('man_item_id').value || '').trim();
        const itemId = (draggedId || fallbackSelected).trim();
        if (!itemId) {
          status.textContent = 'No hay ningún item seleccionado para mover.';
          return;
        }
        try {
          await moveItemToFolder(itemId, d.id, d.name);
        } catch (e) {
          status.textContent = 'Error: ' + String(e);
        }
      });

      li.appendChild(btn);
      ul.appendChild(li);
    }
    if (dirs.length === 0) {
      ul.appendChild(el('li', { class: 'muted', text: '(no subfolders)' }));
    }
    boxFolders.appendChild(ul);

    // items table
    const table = el('table', { class: 'tbl' });
    const thead = el('thead');
    thead.appendChild(el('tr', {}, [
      el('th', { text: 'label' }),
      el('th', { text: 'bytes' }),
      el('th', { text: 'source' }),
      el('th', { text: 'id' }),
    ]));
    table.appendChild(thead);
    const tbody = el('tbody');
    for (const it of items) {
      const tr = el('tr');
      tr.draggable = true;
      tr.title = 'Drag onto a folder to move';

      tr.addEventListener('dragstart', (ev) => {
        if (!ev.dataTransfer) return;
        ev.dataTransfer.setData('text/edrmount-item-id', it.id);
        ev.dataTransfer.effectAllowed = 'move';
        // also mark selection
        document.getElementById('man_item_id').value = it.id;
        document.getElementById('man_item_label').value = it.label;
      });

      const tdLabel = el('td', { class: 'mono', text: it.label });
      tdLabel.style.cursor = 'pointer';
      tdLabel.title = it.filename || '';
      tdLabel.onclick = () => {
        document.getElementById('man_item_id').value = it.id;
        document.getElementById('man_item_label').value = it.label;
      };
      tr.appendChild(tdLabel);
      tr.appendChild(el('td', { class: 'mono', text: String(it.bytes || 0) }));
      tr.appendChild(el('td', { class: 'mono', text: `${it.import_id.slice(0,8)} idx=${it.file_idx}` }));
      const tdId = el('td', { class: 'mono', text: it.id });
      tdId.style.cursor = 'pointer';
      tdId.onclick = () => {
        document.getElementById('man_item_id').value = it.id;
        document.getElementById('man_item_label').value = it.label;
      };
      tr.appendChild(tdId);
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    boxItems.appendChild(table);

    // Populate import picker
    const imports = await apiGet('/api/v1/catalog/imports');
    const sel = document.getElementById('man_pick_import');
    const cur = sel.value;
    sel.innerHTML = '';
    for (const imp of imports) {
      const opt = document.createElement('option');
      opt.value = imp.id;
      opt.textContent = `${imp.id.slice(0, 8)} (${imp.files_count} files) ${imp.path}`;
      sel.appendChild(opt);
    }
    if (cur) sel.value = cur;

    status.textContent = `OK (${dirs.length} folders, ${items.length} items)`;
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

function attachManualClickHelpers() {
  // No-op now; we use real tables/buttons.
}
