// Фоновая музыка: две бесшовные петли — одна в меню, другая в партии.
//
// Треки синтезирует генератор из tools/music и кладёт в web/audio. Петля там
// склеена так, что конец переходит в начало без щелчка, поэтому играем её
// через Web Audio: у <audio loop> на стыке слышна пауза в десятки миллисекунд,
// и на длинной партии этот щелчок замечают все.
//
// Браузер не даёт звуку начаться до первого касания страницы. Поэтому модуль
// до первого жеста только запоминает, что играть, а контекст поднимает уже в
// обработчике жеста — иначе Safari оставляет контекст навсегда подвешенным.

const KEY = 'siege.music';
const FADE = 2.2;   // секунды на перекрёстное затухание меню ↔ партия
const DUCK = 0.3;   // во столько раз музыка тише, пока идёт звонок
const CURVE = 1.6;  // ползунок на слух линейный, усиление — нет

// Что вообще играет, решает audio/tracks.json, который кладёт сборщик музыки:
// там и длина петли, и сам факт, что для экрана выбран трек. Экран без трека —
// нормальное состояние, а не поломка: сейчас так устроена партия, она идёт в
// тишине. Длину петли держим там же, потому что кодек дописывает в конец
// тишину: без явного loopEnd петля с каждым кругом уезжала бы, а второй копии
// этого числа в коде хватит, чтобы однажды забыть поменять его вместе с
// треком.
const url = (kind) => `audio/${kind}.m4a`;
const TRIM = { menu: 1.0, game: 0.9 }; // под партию музыку убираем ещё немного

export function createMusic({ onChange } = {}) {
  let ctx = null;
  let master = null;
  let cur = null;        // {kind, src, g} — что звучит сейчас
  let want = null;       // что должно звучать: экран мог смениться на загрузке
  let vol = stored();
  let ducked = false;
  let failed = false;
  let sleep = 0;
  const buffers = new Map();
  const loading = new Map();
  let loops = null; // audio/tracks.json: длина петли каждого трека

  function stored() {
    try {
      const v = parseFloat(localStorage.getItem(KEY));
      return Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : 0.6;
    } catch { return 0.6; }
  }

  function remember() {
    try { localStorage.setItem(KEY, String(vol)); } catch { /* приватный режим */ }
  }

  function level() {
    return Math.pow(vol, CURVE) * (ducked ? DUCK : 1);
  }

  function changed() { if (onChange) onChange(); }

  function ensure() {
    if (ctx || failed) return ctx;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) { failed = true; return null; }
    ctx = new AC();
    master = ctx.createGain();
    master.gain.value = level();
    master.connect(ctx.destination);
    return ctx;
  }

  async function meta() {
    if (loops) return loops;
    try {
      const res = await fetch('audio/tracks.json');
      loops = res.ok ? await res.json() : {};
    } catch { loops = {}; }
    changed(); // стало известно, какие экраны вообще с музыкой
    return loops;
  }

  // Возвращает {buf, loop}: длину петли берём из tracks.json, а если его нет —
  // играем файл целиком, с лишней тишиной кодека на стыке.
  async function buffer(kind) {
    if (buffers.has(kind)) return buffers.get(kind);
    if (loading.has(kind)) return loading.get(kind);
    const p = (async () => {
      const [res, m] = await Promise.all([fetch(url(kind)), meta()]);
      if (!res.ok) throw new Error(`${res.status}`);
      const buf = await ctx.decodeAudioData(await res.arrayBuffer());
      const declared = m[kind] && m[kind].loop;
      const track = { buf, loop: declared > 0 ? Math.min(declared, buf.duration) : buf.duration };
      buffers.set(kind, track);
      return track;
    })();
    loading.set(kind, p);
    try { return await p; } finally { loading.delete(kind); }
  }

  function fade(node, now, to) {
    const g = node.g.gain;
    g.cancelScheduledValues(now);
    g.setValueAtTime(Math.max(g.value, 0.0001), now);
    g.exponentialRampToValueAtTime(Math.max(to, 0.0001), now + FADE);
  }

  function stop(node) {
    if (!node) return;
    const now = ctx.currentTime;
    fade(node, now, 0.0001);
    try { node.src.stop(now + FADE + 0.1); } catch { /* уже остановлен */ }
  }

  function silence() {
    if (!cur) return;
    stop(cur);
    cur = null;
    changed();
  }

  async function play(kind) {
    want = kind;
    if (failed) return;
    if (vol <= 0) { silence(); return; }
    if (!ctx) return;                      // жеста ещё не было: заиграет в unlock
    if (ctx.state === 'suspended') { try { await ctx.resume(); } catch { /* позже */ } }
    if (cur && cur.kind === kind) return;
    const m = await meta();
    if (want !== kind) return;
    if (!m[kind]) { silence(); return; }   // для этого экрана трек не выбран
    let track;
    try { track = await buffer(kind); } catch { failed = true; changed(); return; }
    // Пока грузили, могли уйти на другой экран или выключить музыку.
    if (want !== kind || vol <= 0 || (cur && cur.kind === kind)) return;

    const now = ctx.currentTime;
    const g = ctx.createGain();
    g.gain.setValueAtTime(0.0001, now);
    g.gain.exponentialRampToValueAtTime(TRIM[kind] || 1, now + FADE);
    const src = ctx.createBufferSource();
    src.buffer = track.buf;
    src.loop = true;
    src.loopStart = 0;
    src.loopEnd = track.loop;
    src.connect(g);
    g.connect(master);
    src.start(now);
    stop(cur);
    cur = { kind, src, g };
    changed();
    // Второй трек подгружаем заранее: переход на поле не должен ждать сети.
    const other = kind === 'menu' ? 'game' : 'menu';
    if (m[other]) {
      setTimeout(() => { if (ctx && !buffers.has(other)) buffer(other).catch(() => {}); }, 5000);
    }
  }

  function setVolume(v) {
    const was = vol;
    vol = Math.min(1, Math.max(0, v));
    remember();
    if (ctx) master.gain.setTargetAtTime(level(), ctx.currentTime, 0.1);
    if (vol <= 0) silence();
    else if (was <= 0) { ensure(); play(want || 'menu'); }
    changed();
  }

  function duck(on) {
    ducked = !!on;
    if (ctx) master.gain.setTargetAtTime(level(), ctx.currentTime, 0.2);
  }

  // Вкладку свернули — гасим и усыпляем контекст: пустая вкладка не должна
  // греть процессор и играть в фоне.
  document.addEventListener('visibilitychange', () => {
    if (!ctx) return;
    clearTimeout(sleep);
    if (document.hidden) {
      master.gain.setTargetAtTime(0.0001, ctx.currentTime, 0.2);
      sleep = setTimeout(() => { if (document.hidden) ctx.suspend().catch(() => {}); }, 1500);
    } else {
      ctx.resume().catch(() => {});
      master.gain.setTargetAtTime(level(), ctx.currentTime, 0.3);
    }
  });

  const unlock = () => {
    if (failed || ctx) return;
    if (!ensure()) return;
    ctx.resume().catch(() => {});
    if (want) play(want);
    changed();
  };
  for (const ev of ['pointerdown', 'keydown', 'touchstart']) {
    document.addEventListener(ev, unlock, { once: true, capture: true });
  }

  // Пока tracks.json не пришёл, считаем, что трек есть: иначе пульт успеет
  // мигнуть и спрятаться. Файл маленький и едет вместе со страницей.
  const has = (kind) => (loops ? !!loops[kind] : true);

  const state = () => ({
    volume: vol, on: vol > 0, ready: !!ctx && !!cur, failed,
    kind: cur ? cur.kind : null,
  });

  meta(); // спрашиваем сразу: пульт должен знать, где музыка есть, а где нет

  return { play, setVolume, duck, state, has };
}
