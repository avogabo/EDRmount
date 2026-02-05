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
  status.textContent = 'Loading import files...';
  try {
    const files = await apiGet(`/api/v1/catalog/imports/${importId}/files`);

    const table = el('table', { class: 'tbl' });
    const thead = el('thead');
    thead.appendChild(el('tr', {}, [
      el('th', { text: 'idx' }),
      el('th', { text: 'filename' }),
      el('th', { text: 'bytes' }),
      el('th', { text: 'add' }),
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
        document.getElementById('man_imp').value = importId;
        document.getElementById('man_idx').value = f.idx;
        document.getElementById('man_label').value = f.filename || `idx ${f.idx}`;
        await addManualItem();
      };
      const td = el('td');
      td.appendChild(btn);
      tr.appendChild(td);
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    box.appendChild(table);

    status.textContent = `Loaded ${files.length} files. Folder=${dir}`;
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function updateManualItem() {
  const status = document.getElementById('manStatus');
  const id = (document.getElementById('man_item_id').value || '').trim();
  const label = (document.getElementById('man_item_label').value || '').trim();
  const dirId = (document.getElementById('man_item_move').value || '').trim();
  if (!id) {
    status.textContent = 'Need item id.';
    return;
  }
  status.textContent = 'Updating item...';
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
    status.textContent = 'Updated.';
    await refreshManual();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function deleteManualItem() {
  const status = document.getElementById('manStatus');
  const id = (document.getElementById('man_item_id').value || '').trim();
  if (!id) {
    status.textContent = 'Need item id.';
    return;
  }
  status.textContent = 'Deleting item...';
  try {
    const r = await fetch(`/api/v1/manual/items/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      headers: { 'Accept': 'application/json' },
    });
    if (!r.ok) throw new Error(await r.text());
    status.textContent = 'Deleted.';
    document.getElementById('man_item_id').value = '';
    document.getElementById('man_item_label').value = '';
    document.getElementById('man_item_move').value = '';
    await refreshManual();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function refreshManual() {
  const status = document.getElementById('manStatus');
  const dir = (document.getElementById('man_dir').value || 'root').trim();
  const boxFolders = document.getElementById('manFolders');
  const boxItems = document.getElementById('manItems');
  boxFolders.innerHTML = '';
  boxItems.innerHTML = '';

  status.textContent = 'Loading...';
  try {
    const dirs = await apiGet(`/api/v1/manual/dirs?parent_id=${encodeURIComponent(dir)}`);
    const items = await apiGet(`/api/v1/manual/items?dir_id=${encodeURIComponent(dir)}`);

    // folders list
    const ul = el('ul');
    if (dir !== 'root') {
      const liUp = el('li');
      const btnUp = el('button', { class: 'btn', text: '⬅ Up (set parent id manually for now)' });
      btnUp.disabled = true;
      liUp.appendChild(btnUp);
      ul.appendChild(liUp);
    }
    for (const d of dirs) {
      const li = el('li');
      const btn = el('button', { class: 'btn', text: d.name });
      btn.onclick = async () => {
        document.getElementById('man_dir').value = d.id;
        await refreshManual();
      };
      const meta = el('span', { class: 'muted', text: `  id=${d.id}` });
      li.appendChild(btn);
      li.appendChild(meta);
      ul.appendChild(li);
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
      const tdLabel = el('td', { class: 'mono', text: it.label });
      tdLabel.style.cursor = 'pointer';
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
