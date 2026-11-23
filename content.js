// 확장 영역(isolated world): page.js가 보낸 정보를 background로 전달
(() => {
  if (window.__lpNowPlayingContent) return;
  window.__lpNowPlayingContent = true;

  let alive = true;
  const setInterval_ = (ms) => document.dispatchEvent(new CustomEvent('lp-now-playing-config', { detail: String(ms) }));
  try {
    chrome.runtime.onMessage.addListener((msg) => {
      if (msg && msg.type === 'lp-config' && msg.interval) setInterval_(msg.interval);
      if (msg && msg.type === 'lp-cmd' && msg.cmd) {
        document.dispatchEvent(new CustomEvent('lp-now-playing-cmd', { detail: JSON.stringify(msg.cmd) }));
      }
    });
  } catch {}
  document.addEventListener('lp-now-playing', (e) => {
    if (!alive) return;
    let state;
    try { state = JSON.parse(e.detail); } catch { return; }
    if (!state.pv) return; // 확장 프로그램 업데이트 전의 예전 스크립트가 보낸 것은 무시
    try {
      chrome.runtime.sendMessage({ type: 'lp-state', state })
        .then((r) => {
          if (r && r.interval) setInterval_(r.interval);
        })
        .catch(() => {});
    } catch {
      // 확장 프로그램이 새로고침/업데이트되면 이 탭의 스크립트는 더 이상 쓸 수 없음
      alive = false;
    }
  });
})();
