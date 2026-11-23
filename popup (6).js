const $ = (id) => document.getElementById(id);

function refresh() {
  chrome.runtime.sendMessage({ type: 'lp-status' }, (s) => {
    if (!s) return;
    const recent = s.ok && Date.now() - s.lastOkAt < 5000;
    $('dot').className = 'dot ' + (recent ? 'ok' : 'bad');
    $('msg').textContent = recent ? '프로그램과 연결됨' : (s.error || '연결 안 됨');
    $('cur').hidden = !s.current;
    $('cur').textContent = s.current ? '♪ ' + s.current : '';
    $('tabs').textContent = s.tabCount ? `YouTube 탭 ${s.tabCount}개 감지` : 'YouTube / YouTube Music 탭이 없어요';
    if (document.activeElement !== $('port')) $('port').value = s.port;
  });
}

$('save').onclick = () => {
  const port = parseInt($('port').value, 10);
  if (!(port >= 1024 && port <= 65535)) return;
  chrome.runtime.sendMessage({ type: 'lp-set-port', port }, refresh);
};

refresh();
setInterval(refresh, 1000);
