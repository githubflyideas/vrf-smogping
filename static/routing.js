// Routing edits are deliberately separate from graph refreshes: a refresh must
// never overwrite an unsaved draft. Only switching target loads a fresh draft.
const routingEditor = (() => {
  const el = id => document.getElementById(id);
  const vrf = el('routingVRF'), source = el('routingSource'), save = el('routingSave'), message = el('routingMessage');
  let selected = null, sequence = 0, editable = false, saving = false;
  const lock = value => { vrf.disabled = source.disabled = save.disabled = value; };
  lock(true);
  async function request(url, options) {
    const response = await fetch(url, options);
    if (response.status === 401) { location.href = '/login'; throw new Error('Session expired'); }
    if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    return response.json();
  }
  async function select(name) {
    if (name === selected) return;
    selected = name;
    const seq = ++sequence;
    editable = false; saving = false; lock(true);
    vrf.value = source.value = '';
    el('routingTarget').textContent = name || '';
    message.textContent = name ? 'Loading routing…' : 'Select a target.';
    if (!name) return;
    try {
      const data = await request('/api/routing-options?target=' + encodeURIComponent(name));
      if (seq !== sequence) return;
      const draft = data.saved_target || data.target;
      vrf.value = draft.vrf || ''; source.value = draft.source_address || '';
      editable = data.editable === true; lock(!editable);
      message.textContent = !editable ? 'Routing is read-only: configure login users to edit.' : data.pending_reload ? 'Saved settings shown; reload pending. Active probes still use the previous settings.' : '';
    } catch (error) { if (seq === sequence) message.textContent = error.message; }
  }
  el('routingForm').addEventListener('submit', async event => {
    event.preventDefault();
    if (!editable || saving || !selected) return;
    saving = true; lock(true);
    const seq = ++sequence;
    const body = {name:selected, vrf:vrf.value.trim(), source_address:source.value.trim()};
    message.textContent = 'Saving…';
    try {
      const result = await request('/api/routing-options', {method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body)});
      if (seq !== sequence) return;
      if (result.saved !== true || result.pending_reload !== true) throw new Error('Save was not acknowledged.');
      vrf.value = body.vrf; source.value = body.source_address;
      message.textContent = 'Saved; reload pending. Reopen this page to check active settings. Probe errors are shown separately.';
    } catch (error) { if (seq === sequence) message.textContent = 'Not saved: ' + error.message; }
    finally { if (seq === sequence) { saving = false; lock(!editable); } }
  });
  return {select};
})();
