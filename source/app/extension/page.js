// YouTube Music / YouTube 페이지 안(MAIN world)에서 실행: 플레이어 정보를 읽어 content.js로 전달
(() => {
  // 확장 프로그램을 업데이트하면 이미 열린 탭의 예전 스크립트를 멈추고 새 스크립트로 교체
  const PV = '2.1.4';
  if (window.__lpNowPlayingPage === PV) return;
  try { if (typeof window.__lpNowPlayingStop === 'function') window.__lpNowPlayingStop(); } catch {}
  window.__lpNowPlayingPage = PV;
  const offs = [];
  const on = (target, ev, fn, opt) => { target.addEventListener(ev, fn, opt); offs.push(() => target.removeEventListener(ev, fn, opt)); };
  let lastAck = 0; // 마지막으로 실행한 조작 명령 번호 (프로그램이 "실행됨" 확인용)

  const IS_YTM = location.hostname === 'music.youtube.com';
  const SOURCE = IS_YTM ? 'ytmusic' : 'youtube';
  const $ = (sel) => document.querySelector(sel);
  const txt = (sel) => ($(sel)?.textContent || '').trim();

  function biggestArtwork(list) {
    if (!list || !list.length) return '';
    let best = list[0], bestArea = 0;
    for (const a of list) {
      const m = /(\d+)x(\d+)/.exec(a.sizes || '');
      const area = m ? (+m[1]) * (+m[2]) : 0;
      if (area >= bestArea) { best = a; bestArea = area; }
    }
    return best.src || '';
  }
  // 작은 썸네일 URL을 큰 사이즈로 (googleusercontent: =w60-h60 → =w544-h544)
  const upsize = (u) => (u || '').replace(/=w\d+-h\d+/, '=w544-h544');

  // "1:23" / "1:02:03" → 초
  const toSec = (t) => (t || '').trim().split(':').reduce((acc, x) => acc * 60 + (parseInt(x, 10) || 0), 0);

  // 화면에 보이는 재생 시간 (곡이 바뀌어도 이전 곡 시간이 이어지지 않는 "현재 곡 기준" 값)
  function shownTime() {
    if (IS_YTM) {
      const m = /([\d:]+)\s*\/\s*([\d:]+)/.exec(txt('ytmusic-player-bar .time-info'));
      if (m) return [toSec(m[1]), toSec(m[2])];
      const el = $('ytmusic-player-bar #progress-bar');
      if (el) {
        const v = parseFloat(el.getAttribute('value') ?? el.getAttribute('aria-valuenow'));
        const max = parseFloat(el.getAttribute('max') ?? el.getAttribute('aria-valuemax'));
        if (isFinite(v) && max > 0) return [v, max];
      }
    } else {
      const cur = txt('#movie_player .ytp-time-current'), dur = txt('#movie_player .ytp-time-duration');
      if (cur && dur) return [toSec(cur), toSec(dur)];
    }
    return [NaN, NaN];
  }

  let lastVid = null, vidOffset = 0;

  // 재생 위치/길이 + 플레이어 시간과 화면 시간의 차이(offset)
  //  - YouTube: 플레이어가 알려 주는 정확한 값 (화면 시간 글자는 컨트롤이 숨겨지면 갱신이 멈춤)
  //  - YouTube Music: 화면 시간이 기준. 플레이어 값이 화면 시간과 맞으면 더 정확한 플레이어 값 사용
  //    (곡이 바뀌어도 플레이어 시간이 이전 곡에서 이어지는 경우가 있어서, 그 차이를 offset으로 기억해 이동할 때 보정)
  function timing(player, video, videoId) {
    const [shownPos, shownDur] = shownTime();
    let apiPos = NaN, apiDur = NaN;
    try { apiPos = player.getCurrentTime(); apiDur = player.getDuration(); } catch {}
    const apiOK = isFinite(apiPos) && apiDur > 0;
    let position = NaN, duration = NaN, offset = 0;
    if (!IS_YTM && apiOK) { position = apiPos; duration = apiDur; }
    else if (IS_YTM && apiOK && shownDur > 0 && Math.abs(apiPos - shownPos) < 2.5 && Math.abs(apiDur - shownDur) < 2.5) { position = apiPos; duration = apiDur; }
    else if (shownDur > 0) { position = shownPos; duration = shownDur; if (apiOK) offset = apiPos - shownPos; }
    else if (apiOK) { position = apiPos; duration = apiDur; }
    if (!(duration > 0) || !isFinite(position)) {
      const raw = video ? (video.currentTime || 0) : 0;
      if (videoId !== lastVid) { lastVid = videoId; vidOffset = raw > 3 ? raw : 0; }
      position = Math.max(0, raw - vidOffset);
      duration = video && isFinite(video.duration) ? video.duration - vidOffset : 0;
    }
    if (duration > 0 && position > duration) position = duration;
    return { position: Math.max(0, position || 0), duration: Math.max(0, duration || 0), offset };
  }

  // 일반 YouTube: 영상 시청 화면(/watch)이나 미니플레이어일 때만 (홈 화면 미리보기 재생은 무시)
  function youtubeHasVideo() {
    if (location.pathname.startsWith('/watch')) return true;
    const mini = $('ytd-miniplayer');
    return !!(mini && (mini.hasAttribute('active') || mini.hasAttribute('enabled')) && mini.offsetParent !== null);
  }

  // ── 이 탭에서 본 영상 기억 (재생목록 없는 YouTube에서 "이전 곡"용) ──
  const HKEY = '__lpNowPlayingHistory';
  const loadHist = () => { try { return JSON.parse(sessionStorage.getItem(HKEY) || '[]'); } catch { return []; } };
  const saveHist = (h) => { try { sessionStorage.setItem(HKEY, JSON.stringify(h.slice(-50))); } catch {} };
  function rememberVideo(id) {
    if (!id) return;
    const h = loadHist();
    if (h[h.length - 1] !== id) { h.push(id); saveHist(h); }
  }

  // ── 조작 명령 실행 ──
  const player = () => document.getElementById('movie_player');
  const click = (sel) => { const b = $(sel); if (b && !b.disabled) { b.click(); return true; } return false; };
  const vidOf = (p) => { try { return p.getVideoData().video_id || ''; } catch { return ''; } };
  function curTiming() { const p = player(); return timing(p, p?.querySelector('video'), vidOf(p)); }
  function curTime() { return curTiming().position; }
  function isPlaying() {
    try { const s = player().getPlayerState(); if (s === 1 || s === 3) return true; if (s === 2 || s === 0 || s === 5) return false; } catch {}
    const v = player()?.querySelector('video');
    return !!v && !v.paused;
  }
  function seekTo(sec) {
    sec = Math.max(0, sec + curTiming().offset); // YouTube Music에서 플레이어 시간이 어긋나 있으면 보정
    try { player().seekTo(sec, true); return; } catch {}
    const v = player()?.querySelector('video'); if (v) v.currentTime = Math.max(0, sec);
  }
  function hasPlaylist() {
    try { return !!new URL(location.href).searchParams.get('list'); } catch { return false; }
  }
  function goPrev() {
    // 음악 앱처럼: 3초 넘게 재생했으면 처음으로
    if (curTime() > 3) { seekTo(0); return; }
    if (IS_YTM) { if (!click('ytmusic-player-bar .previous-button')) { try { player().previousVideo(); } catch { seekTo(0); } } return; }
    if (hasPlaylist()) { if (!click('#movie_player .ytp-prev-button')) { try { player().previousVideo(); } catch { seekTo(0); } } return; }
    // 재생목록 없음: 이 탭에서 전에 본 영상으로, 첫 영상이면 처음으로 되감기 (유튜브 홈으로 나가지 않음)
    const h = loadHist();
    let cur = ''; try { cur = player().getVideoData().video_id; } catch {}
    while (h.length && h[h.length - 1] === cur) h.pop();
    const prev = h[h.length - 1];
    if (prev) { saveHist(h); location.assign('https://www.youtube.com/watch?v=' + encodeURIComponent(prev)); }
    else seekTo(0);
  }
  function goNext() {
    if (IS_YTM) { if (!click('ytmusic-player-bar .next-button')) { try { player().nextVideo(); } catch {} } return; }
    if (!click('#movie_player .ytp-next-button')) { try { player().nextVideo(); } catch {} }
  }
  function setPlaying(on) {
    try { on ? player().playVideo() : player().pauseVideo(); return; } catch {}
    if (IS_YTM) click('ytmusic-player-bar #play-pause-button'); else click('#movie_player .ytp-play-button');
  }
  on(document, 'lp-now-playing-cmd', (e) => {
    let c; try { c = JSON.parse(e.detail); } catch { return; }
    if (!c || !player()) return;
    if (c.id && c.id <= lastAck) return; // 같은 명령 두 번 실행 방지
    lastAck = c.id || lastAck;
    switch (c.type) {
      case 'toggle': setPlaying(!isPlaying()); break;
      case 'play': setPlaying(true); break;
      case 'pause': setPlaying(false); break;
      case 'next': goNext(); break;
      case 'prev': goPrev(); break;
      case 'seek': if (isFinite(c.value)) seekTo(c.value); break;
    }
    setTimeout(emit, 200);
    setTimeout(emit, 700);
  });

  function read() {
    const empty = { pv: PV, ack: lastAck, source: SOURCE, title: '', artist: '', album: '', artwork: '', videoId: '', url: '', playing: false, isAd: false, position: 0, duration: 0, ts: Date.now() };
    if (!IS_YTM && !youtubeHasVideo()) return empty;

    const player = document.getElementById('movie_player');
    if (!player) return empty;
    const video = player.querySelector('video') || $('video');
    const md = navigator.mediaSession && navigator.mediaSession.metadata;

    let vd = {};
    try { vd = (player.getVideoData && player.getVideoData()) || {}; } catch {}
    let ps = -1;
    try { ps = player.getPlayerState ? player.getPlayerState() : -1; } catch {}

    let title, artist, album = '', artwork;
    if (IS_YTM) {
      title = (md && md.title) || txt('ytmusic-player-bar .title') || vd.title || '';
      artist = (md && md.artist) || (txt('ytmusic-player-bar .byline').split('•')[0] || '').trim() || vd.author || '';
      album = (md && md.album) || '';
      artwork = biggestArtwork(md && md.artwork) || upsize($('ytmusic-player-bar img.image, ytmusic-player-bar .thumbnail img')?.src);
    } else {
      // 영상 제목과 채널 이름 (곡/아티스트 정리는 프로그램이 함)
      title = vd.title || (md && md.title) || txt('#title h1') || '';
      artist = vd.author || (md && md.artist) || txt('#owner #channel-name a') || '';
      artwork = biggestArtwork(md && md.artwork);
    }

    let videoId = vd.video_id || '';
    if (!videoId) { try { videoId = new URL(location.href).searchParams.get('v') || ''; } catch {} }
    if (!IS_YTM) rememberVideo(videoId);

    let playing;
    if (ps === 1 || ps === 3) playing = true;
    else if (ps === 0 || ps === 2 || ps === 5) playing = false;
    else playing = !!video && !video.paused && !video.ended;

    const isAd = player.classList.contains('ad-showing') || player.classList.contains('ad-interrupting');

    // 재생 위치/길이: 1) 화면에 보이는 시간 2) 플레이어 API 3) video 요소(곡 시작 시점 보정)
    const { position, duration } = timing(player, video, videoId);

    return {
      pv: PV, ack: lastAck,
      source: SOURCE, title, artist, album, artwork, videoId,
      url: videoId ? (IS_YTM ? 'https://music.youtube.com/watch?v=' : 'https://www.youtube.com/watch?v=') + videoId : '',
      playing, isAd,
      position, duration,
      ts: Date.now()
    };
  }

  function emit() {
    try {
      document.dispatchEvent(new CustomEvent('lp-now-playing', { detail: JSON.stringify(read()) }));
    } catch {}
  }

  // 읽는 간격: 프로그램과 연결되면 1초, 꺼져 있으면 10초 (background가 알려 줌)
  let interval = 1000;
  let timer = setInterval(emit, interval);
  window.__lpNowPlayingStop = () => { clearInterval(timer); offs.forEach((f) => f()); };
  on(document, 'lp-now-playing-config', (e) => {
    const v = parseInt(e.detail, 10);
    if (v >= 500 && v <= 60000 && v !== interval) {
      interval = v;
      clearInterval(timer);
      timer = setInterval(emit, interval);
    }
  });
  for (const ev of ['play', 'pause', 'ended', 'loadedmetadata', 'seeked']) {
    on(document, ev, () => setTimeout(emit, 150), true);
  }
  on(window, 'yt-navigate-finish', () => setTimeout(emit, 300)); // 유튜브 페이지 이동
  emit();
})();
