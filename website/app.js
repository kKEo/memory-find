// Interactive "Live demo": reciprocal rank fusion over five retrieval arms.
(function () {
  const ARMS = [
    { id: 'keyword', label: 'Keyword' },
    { id: 'exact', label: 'Exact' },
    { id: 'semantic', label: 'Semantic' },
    { id: 'facts', label: 'Facts' },
    { id: 'graph', label: 'Graph' },
  ];
  const DOCS = [
    { title: 'Replication runbook', uri: 'memo://chunk/212', ranks: { keyword: 3, semantic: 1, graph: 1 } },
    { title: 'Billing Service overview', uri: 'memo://chunk/88', ranks: { keyword: 1, exact: 2, semantic: 4, graph: 3 } },
    { title: 'Postgres failover notes', uri: 'memo://chunk/140', ranks: { semantic: 2, graph: 2 } },
    { title: 'Invoice retry ADR', uri: 'memo://chunk/57', ranks: { keyword: 2, exact: 1 } },
    { title: 'Billing writes to primary (eu-west)', uri: 'memo://fact/19', ranks: { facts: 1, semantic: 5 } },
  ];
  const K = 60;
  const on = Object.fromEntries(ARMS.map(a => [a.id, true]));

  const togglesEl = document.getElementById('arm-toggles');
  const resultsEl = document.getElementById('results');
  const traceEl = document.getElementById('demo-trace');

  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  };

  ARMS.forEach(a => {
    const b = el('button', 'arm-toggle', a.label);
    b.type = 'button';
    b.setAttribute('aria-pressed', 'true');
    b.addEventListener('click', () => {
      on[a.id] = !on[a.id];
      b.setAttribute('aria-pressed', String(on[a.id]));
      render();
    });
    togglesEl.appendChild(b);
  });

  function render() {
    const scored = DOCS.map(d => {
      let score = 0;
      for (const a of ARMS) if (on[a.id] && d.ranks[a.id]) score += 1 / (K + d.ranks[a.id]);
      return { ...d, score };
    }).filter(d => d.score > 0).sort((a, b) => b.score - a.score);
    const max = scored.length ? scored[0].score : 1;
    const active = ARMS.filter(a => on[a.id]).length;
    traceEl.textContent = `${active} of ${ARMS.length} arms · RRF k=${K} · score ×1000`;

    resultsEl.replaceChildren();
    if (!scored.length) {
      resultsEl.appendChild(el('li', 'empty',
        'Zero results — no arm is running. memors-mcp says so instead of returning noise.'));
      return;
    }
    scored.forEach((d, i) => {
      const row = el('li', 'result-row');
      row.style.animationDelay = `${i * 30}ms`;
      row.appendChild(el('span', 'result-pos', String(i + 1).padStart(2, '0')));

      const name = el('div', 'result-name');
      name.append(el('span', null, d.title), el('code', 'addr', d.uri));
      row.appendChild(name);

      const chips = el('div', 'result-chips');
      ARMS.filter(a => d.ranks[a.id]).forEach(a => {
        chips.appendChild(el('span', 'pill ' + (on[a.id] ? 'pill-strong' : 'pill-off'), `${a.id} #${d.ranks[a.id]}`));
      });
      row.appendChild(chips);

      const score = el('div', 'result-score');
      const bar = el('div', 'bar');
      const fill = el('i');
      fill.style.width = Math.round((d.score / max) * 100) + '%';
      bar.appendChild(fill);
      score.append(el('code', null, (d.score * 1000).toFixed(1)), bar);
      row.appendChild(score);

      resultsEl.appendChild(row);
    });
  }
  render();

  // Copy-to-clipboard for the install snippet.
  document.querySelectorAll('[data-copy]').forEach(btn => {
    btn.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(btn.dataset.copy);
        btn.textContent = 'Copied';
      } catch {
        btn.textContent = 'Copy failed';
      }
      setTimeout(() => { btn.textContent = 'Copy'; }, 1600);
    });
  });
})();
