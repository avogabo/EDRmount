async function loadImportFilesForManual() {
  const out = document.getElementById('manImportFiles');
  const status = document.getElementById('manStatus');
  const dir = (document.getElementById('man_dir').value || 'root').trim();
  const importId = document.getElementById('man_pick_import').value;
  if (!importId) {
    out.textContent = 'No import selected.';
    return;
  }
  status.textContent = 'Loading import files...';
  try {
    const files = await apiGet(`/api/v1/catalog/imports/${importId}/files`);
    const lines = [];
    lines.push(`IMPORT ${importId}`);
    lines.push('');
    lines.push('Click a file_idx row to add it to current folder.');
    lines.push('');
    for (const f of files) {
      const fn = f.filename || '';
      lines.push(`${f.idx}\t${fn}\tbytes=${f.total_bytes}`);
    }
    out.textContent = lines.join('\n');

    // Also auto-fill manual add fields with this import
    document.getElementById('man_imp').value = importId;
    document.getElementById('man_idx').value = 0;
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

function attachManualClickHelpers() {
  // Click helper: if user clicks a line containing "id=..." copy into item id.
  const out = document.getElementById('manOut');
  out.addEventListener('click', () => {
    const sel = window.getSelection();
    const t = (sel && sel.toString ? sel.toString() : '').trim();
    // Try parse id=... from selection
    const m = t.match(/id=([a-f0-9\-]{8,})/i);
    if (m) {
      document.getElementById('man_item_id').value = m[1];
      return;
    }
  });

  // Click helper: select a line with idx\t...
  const imp = document.getElementById('manImportFiles');
  imp.addEventListener('click', () => {
    const sel = window.getSelection();
    const t = (sel && sel.toString ? sel.toString() : '').trim();
    const m = t.match(/^([0-9]+)\t/);
    if (m) {
      document.getElementById('man_idx').value = Number(m[1]);
    }
  });
}
