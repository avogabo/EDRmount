async function saveDownloadProvider() {
  const status = document.getElementById('dlStatus');
  status.textContent = 'Saving download provider...';
  try {
    const cfg = await apiGet('/api/v1/config');
    cfg.download = cfg.download || {};
    cfg.download.enabled = document.getElementById('dl_enabled').checked;
    cfg.download.host = document.getElementById('dl_host').value.trim();
    cfg.download.port = Number(document.getElementById('dl_port').value || 563);
    cfg.download.ssl = document.getElementById('dl_ssl').checked;
    cfg.download.user = document.getElementById('dl_user').value;
    const pass = document.getElementById('dl_pass').value;
    if (pass && pass.trim() !== '') cfg.download.pass = pass;
    cfg.download.connections = Number(document.getElementById('dl_connections').value || 20);
    cfg.download.prefetch_segments = Number(document.getElementById('dl_prefetch').value || 50);

    const out = await apiPutJson('/api/v1/config', cfg);
    document.getElementById('config').value = fmtJSON(out);
    status.textContent = 'Saved.';
  } catch (e) {
    status.textContent = 'Error: ' + String(e);
  }
}

async function testDownloadProvider() {
  const status = document.getElementById('dlStatus');
  status.textContent = 'Testing connectivity...';
  try {
    const req = {
      host: document.getElementById('dl_host').value.trim(),
      port: Number(document.getElementById('dl_port').value || 563),
      ssl: document.getElementById('dl_ssl').checked
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
