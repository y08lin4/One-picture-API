const body = document.body;
const refreshBtn = document.getElementById('refreshBtn');
const loginBtn = document.getElementById('loginBtn');
const adminBtn = document.getElementById('adminBtn');
const apiPath = window.AppCommon.detectApiPath();

function refreshBackground() {
  window.AppCommon.refreshBackground(body, apiPath);
}
refreshBackground();
refreshBtn.addEventListener('click', refreshBackground);
loginBtn.addEventListener('click', () => {
  location.href = '/public/login.html';
});
adminBtn.addEventListener('click', () => {
  location.href = '/public/admin.html';
});

const keyMap = {
  "redirect_web": "/api/web",
  "redirect_m": "/api/m",
  "json_web": "/api/web/json",
  "json_m": "/api/m/json",
  "upload": "/api/upload"
};

async function loadStats() {
  const res = await fetch('/api/stats?t=' + Date.now());
  if (!res.ok) {
    throw new Error('HTTP ' + res.status);
  }
  const data = await res.json();
  const tbody = document.getElementById('statsBody');
  tbody.replaceChildren();

  Object.keys(data).forEach((key) => {
    const path = keyMap[key] || key;
    const tr = document.createElement('tr');
    [path, data[key].today, data[key].total].forEach((value) => {
      const td = document.createElement('td');
      td.textContent = String(value);
      tr.appendChild(td);
    });
    tbody.appendChild(tr);
  });
}

let statsTimer = null;
let statsIntervalMs = 10000;

function scheduleStats(nextMs = statsIntervalMs) {
  if (statsTimer) {
    clearTimeout(statsTimer);
  }
  statsTimer = setTimeout(async () => {
    if (document.hidden) {
      scheduleStats(30000);
      return;
    }
    try {
      await loadStats();
      statsIntervalMs = 10000;
      scheduleStats(statsIntervalMs);
    } catch {
      statsIntervalMs = Math.min(statsIntervalMs * 2, 60000);
      scheduleStats(statsIntervalMs);
    }
  }, nextMs);
}

loadStats().catch(() => {});
scheduleStats(10000);

document.addEventListener('visibilitychange', () => {
  if (!document.hidden) {
    loadStats().catch(() => {});
    scheduleStats(10000);
  }
});

function updateBeijingTime() {
  const now = new Date();
  const utc = now.getTime() + (now.getTimezoneOffset() * 60000);
  const beijing = new Date(utc + 8 * 3600000);
  const h = String(beijing.getHours()).padStart(2, '0');
  const m = String(beijing.getMinutes()).padStart(2, '0');
  const s = String(beijing.getSeconds()).padStart(2, '0');
  document.getElementById('timeDisplay').textContent = `北京时间：${h}:${m}:${s}`;
}
updateBeijingTime();
setInterval(updateBeijingTime, 1000);
