// 모든 YouTube / YouTube Music 탭의 상태를 모아 LP Now Playing 프로그램(127.0.0.1)으로 전송
const DEFAULT_PORT = 47800;
const YTM = ['https://music.youtube.com/*', 'https://www.youtube.com/*'];

// 프로그램이 꺼져 있으면 쉬기: 10초에 한 번만 연결 확인, 탭도 10초에 한 번만 읽게 함
const ACTIVE_INTERVAL = 1000;
const IDLE_INTERVAL = 10000;
let active = false;   // 프로그램과 연결된 상태인지
let nextTryAt = 0;    // 꺼져 있을 때 다음 연결 시도 시각

const states = new Map(); // tabId -> state
let lastPostAt = 0;
let pending = null;
let status = { ok: false, lastOkAt: 0, error: '아직 전송 전', current: '' };

async function getPort() {
  try {
    const { port } = await chrome.storage.local.get('port');
    const p = parseInt(port, 10);
    return p >= 1024 && p <= 65535 ? p : DEFAULT_PORT;
  } catch { return DEFAULT_PORT; }
}

// 연결 상태가 바뀌면 열린 YouTube Music 탭들에 읽는 간격을 바로 알려 줌
async function setActive(on) {
  if (on === active) return;
  active = on;
  if (on) pollCommands();
  let tabs = [];
  try { tabs = await chrome.tabs.query({ url: YTM }); } catch {}
  for (const t of tabs) {
    chrome.tabs.sendMessage(t.id, { type: 'lp-config', interval: on ? ACTIVE_INTERVAL : IDLE_INTERVAL }).catch(() => {});
  }
}

// 프로그램이 보낸 조작 명령(재생/정지, 이전/다음, 이동)을 기다렸다가 해당 탭에 전달
let polling = false;
async function pollCommands() {
  if (polling) return;
  polling = true;
  while (active) {
    try {
      const port = await getPort();
      const r = await fetch(`http://127.0.0.1:${port}/api/commands?wait=20`, { cache: 'no-store' });
      const j = await r.json();
      for (const c of j.commands || []) {
        chrome.tabs.sendMessage(c.tabId, { type: 'lp-cmd', cmd: c }).catch(() => {});
      }
    } catch {
      await new Promise((res) => setTimeout(res, 1000));
    }
  }
  polling = false;
}

async function post() {
  pending = null;
  lastPostAt = Date.now();
  let tabs = [];
  try { tabs = await chrome.tabs.query({ url: YTM }); } catch {}
  const alive = new Set(tabs.map((t) => t.id));
  for (const id of [...states.keys()]) if (!alive.has(id)) states.delete(id);

  const list = tabs
    .filter((t) => states.has(t.id))
    .map((t) => ({ ...states.get(t.id), tabId: t.id, windowId: t.windowId, index: t.index, audible: !!t.audible }));

  const port = await getPort();
  try {
    const r = await fetch(`http://127.0.0.1:${port}/api/tabs`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tabs: list, ext: chrome.runtime.getManifest().version })
    });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    const playing = list.find((t) => t.playing) || list[0];
    status = { ok: true, lastOkAt: Date.now(), error: '', current: playing ? `${playing.title} — ${playing.artist}` : '' };
    setActive(true);
  } catch (e) {
    status = { ...status, ok: false, error: '프로그램이 꺼져 있거나 [시작] 전이에요. (10초마다 확인 중)' };
    setActive(false);
    nextTryAt = Date.now() + IDLE_INTERVAL - 500;
  }
}

function schedule() {
  if (pending) return;
  if (!active && Date.now() < nextTryAt) return; // 프로그램이 꺼져 있으면 다음 확인 시각까지 쉼
  const wait = Math.max(0, 400 - (Date.now() - lastPostAt));
  pending = setTimeout(post, wait);
}

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg && msg.type === 'lp-state' && sender.tab) {
    states.set(sender.tab.id, msg.state);
    schedule();
    // 탭에게 읽는 간격을 알려 줌 (연결됨: 1초, 프로그램 꺼짐: 10초)
    sendResponse({ interval: active ? ACTIVE_INTERVAL : IDLE_INTERVAL });
    return;
  }
  if (msg && msg.type === 'lp-status') {
    getPort().then((port) => sendResponse({ ...status, port, tabCount: states.size }));
    return true;
  }
  if (msg && msg.type === 'lp-set-port') {
    chrome.storage.local.set({ port: msg.port }).then(() => { schedule(); sendResponse({ ok: true }); });
    return true;
  }
});

chrome.tabs.onRemoved.addListener((id) => { if (states.delete(id)) schedule(); });
chrome.tabs.onMoved.addListener(() => schedule());
chrome.tabs.onAttached.addListener(() => schedule());

// 설치/업데이트 직후, 이미 열려 있던 YouTube Music 탭에도 스크립트 주입 (새로고침 없이 바로 동작)
async function injectExisting() {
  let tabs = [];
  try { tabs = await chrome.tabs.query({ url: YTM }); } catch {}
  for (const t of tabs) {
    try {
      await chrome.scripting.executeScript({ target: { tabId: t.id }, files: ['page.js'], world: 'MAIN' });
      await chrome.scripting.executeScript({ target: { tabId: t.id }, files: ['content.js'] });
    } catch {}
  }
}
chrome.runtime.onInstalled.addListener(injectExisting);
