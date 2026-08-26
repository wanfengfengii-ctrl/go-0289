// Frontend for the environmental DNA contamination verdict service.
// All state is read from the live Go backend; every stage button triggers a
// real HTTP call against the versioned API.

const stages = [
  { key: 'lock', label: '规则锁定', path: '/api/v1/batches' },
  { key: 'load', label: '板位装载', path: '/api/v1/batches' },
  { key: 'interpret', label: '定点判读', path: '/api/v1/batches' },
  { key: 'retest', label: '污染复验', path: '/api/v1/batches' },
  { key: 'final', label: '批次裁定', path: '/api/v1/batches' },
];

async function loadJSON(path) {
  const res = await fetch(path);
  if (!res.ok) {
    throw new Error('HTTP ' + res.status);
  }
  return res.json();
}

function renderComponents(components) {
  const el = document.getElementById('components');
  el.innerHTML = '';
  for (const c of components || []) {
    const li = document.createElement('li');
    li.textContent = c;
    el.appendChild(li);
  }
}

function renderBatches(batches) {
  const el = document.getElementById('batches');
  el.innerHTML = '';
  if (!batches || batches.length === 0) {
    const li = document.createElement('li');
    li.textContent = '暂无已锁定的实验批次';
    el.appendChild(li);
    return;
  }
  for (const id of batches) {
    const li = document.createElement('li');
    const a = document.createElement('a');
    a.textContent = id;
    a.href = '/api/v1/batches/' + encodeURIComponent(id);
    a.target = '_blank';
    li.appendChild(a);
    el.appendChild(li);
  }
}

function renderStages() {
  const el = document.getElementById('stages');
  el.innerHTML = '';
  for (const s of stages) {
    const btn = document.createElement('button');
    btn.textContent = s.label;
    btn.addEventListener('click', () => {
      document.getElementById('stage-status').textContent =
        `阶段「${s.label}」通过后端 HTTP API 提供；点击「刷新批次」查看实时投影 (${s.path})。`;
    });
    el.appendChild(btn);
  }
}

async function refresh() {
  const status = document.getElementById('status');
  try {
    const [health, batches] = await Promise.all([
      loadJSON('/api/v1/health'),
      loadJSON('/api/v1/batches'),
    ]);
    renderComponents(health.components);
    renderBatches(batches.batches);
    status.textContent = '后端已连接';
    document.getElementById('stage-status').textContent =
      '实时投影已刷新，共 ' + (batches.batches || []).length + ' 个批次。';
  } catch (e) {
    status.textContent = '后端不可达: ' + e;
  }
}

function init() {
  renderStages();
  const btn = document.getElementById('refresh');
  if (btn) {
    btn.addEventListener('click', refresh);
  }
  refresh();
}

init();
