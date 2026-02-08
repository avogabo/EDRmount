function fmtTime(ts) {
  if (!ts) return '';
  return String(ts).replace('T',' ').replace('Z','');
}

async function refreshBackups() {
  const status = document.getElementById('bkStatus');
  const list = document.getElementById('bkList');
  status.textContent = 'Cargando backups...';
  list.innerHTML = '';
  try {
    // Status first: show last/next + warnings
    try {
      const st = await apiGet('/api/v1/backups/status');
      if (st && st.exists === false) {
        status.textContent = `Aviso/Warning: backups.dir no existe: ${st.dir}`;
      } else if (st && st.writable === false) {
        status.textContent = `Aviso/Warning: backups.dir no es escribible (not writable): ${st.dir}`;
      } else {
        const last = st.last_backup ? `${st.last_backup.name} @ ${fmtTime(st.last_backup.time)}` : '(none)';
        const next = st.next_due ? fmtTime(st.next_due) : '(n/a)';
        const dueFlag = st.overdue ? ' [OVERDUE]' : '';
        status.textContent = `OK · Último/Last: ${last} · Próximo/Next: ${next}${dueFlag}`;
      }
    } catch (_) {
      // ignore
    }

    const r = await apiGet('/api/v1/backups');
    const items = r.items || [];

    const table = el('table', { class: 'tbl' });
    const thead = el('thead');
    thead.appendChild(el('tr', {}, [
      el('th', { text: 'archivo/file' }),
      el('th', { text: 'tamaño/size' }),
      el('th', { text: 'fecha/time' }),
      el('th', { text: 'restaurar/restore' }),
    ]));
    table.appendChild(thead);

    const tbody = el('tbody');
    for (const it of items) {
      const tr = el('tr');
      tr.appendChild(el('td', { class: 'mono', text: it.name }));
      tr.appendChild(el('td', { class: 'mono', text: String(it.size || '') }));
      tr.appendChild(el('td', { class: 'mono', text: fmtTime(it.time || '') }));

      const btn = el('button', { class: 'btn', text: 'Restaurar' });
      btn.onclick = async () => {
        if (!confirm(`¿Restaurar backup ${it.name}?\n\nEDRmount se reiniciará.`)) return;
        status.textContent = 'Restaurando (reiniciará)...';
        try {
          await apiPostJson('/api/v1/backups/restore', { name: it.name });
          status.textContent = `Restaurado. Reiniciando...`;
          // La UI caerá; recarga cuando vuelva.
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
    list.appendChild(table);

    status.textContent = `OK (${items.length} backups)`;
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function backupNow() {
  const status = document.getElementById('bkStatus');
  status.textContent = 'Ejecutando backup...';
  try {
    const r = await apiPostJson('/api/v1/backups/run', {});
    status.textContent = 'Backup creado.';
    await refreshBackups();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function saveBackupSettings() {
  const status = document.getElementById('bkStatus');
  status.textContent = 'Guardando ajustes de backups...';
  try {
    const cfg = await apiGet('/api/v1/config');
    cfg.backups = cfg.backups || {};
    cfg.backups.enabled = document.getElementById('bk_enabled').checked;
    cfg.backups.dir = document.getElementById('bk_dir').value.trim() || '/backups';
    cfg.backups.every_mins = Number(document.getElementById('bk_every').value || 0);
    cfg.backups.keep = Number(document.getElementById('bk_keep').value || 30);
    cfg.backups.compress_gz = document.getElementById('bk_gz').checked;
    const out = await apiPutJson('/api/v1/config', cfg);
    status.textContent = 'Guardado.';
    await loadBackupSettings();
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function loadBackupSettings() {
  const cfg = await apiGet('/api/v1/config');
  const b = cfg.backups || {};
  document.getElementById('bk_enabled').checked = !!b.enabled;
  document.getElementById('bk_dir').value = b.dir || '/backups';
  document.getElementById('bk_every').value = (b.every_mins != null) ? b.every_mins : 0;
  document.getElementById('bk_keep').value = (b.keep != null) ? b.keep : 30;
  document.getElementById('bk_gz').checked = (b.compress_gz !== false);

  // Show quick status (writable/mounted-ish)
  const status = document.getElementById('bkStatus');
  try {
    const st = await apiGet('/api/v1/backups/status');
    if (st && st.exists === false) {
      status.textContent = `Aviso/Warning: backups.dir no existe: ${st.dir}`;
    } else if (st && st.writable === false) {
      status.textContent = `Aviso/Warning: backups.dir no es escribible (not writable): ${st.dir}`;
    }
  } catch (_) {
    // ignore
  }
}
