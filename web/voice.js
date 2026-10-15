// Голосовая связь между игроками комнаты.
//
// Звук идёт напрямую между браузерами (WebRTC, библиотека simple-peer из
// web/vendor). Сервер участвует только как «телефонистка»: перекладывает
// сигналы соединения через тот же поток SSE, которым ходит состояние партии.
// Поэтому ни отдельного порта, ни медиасервера на VM не нужно.
//
// Как собирается связь:
// - звонит младшее место, старшее отвечает — иначе оба пришлют по offer'у;
// - кандидаты ICE уходят по мере сбора (trickle): offer летит сразу, а не
//   после полного сбора, который на каждой стороне длится секунды, а за VPN
//   или при недоступном STUN упирается в пятисекундный таймаут;
// - у каждой попытки свой номер сеанса (sid). Запоздавшие сигналы прошлых
//   попыток отбрасываются, а offer с новым sid заменяет прежнее соединение —
//   иначе новая попытка натыкалась на остатки старой и залипала;
// - нет ответа на offer или связь не собралась, оборвалась — звонящий
//   пробует снова сам, не дожидаясь следующего снимка партии.

const SPEAK_THRESHOLD = 12;     // громкость, с которой считаем, что человек говорит
const ANSWER_TIMEOUT = 5000;    // столько звонящий ждёт ответа на offer
const CONNECT_TIMEOUT = 10000;  // а столько — соединения; на повторах ждём дольше
const DROP_GRACE = 5000;        // короткий обрыв (смена Wi-Fi) WebRTC переживает сам
const RETRY_BASE = 1000;        // пауза перед повтором: 1, 2, 4, 8… секунд
const RETRY_MAX = 15000;
const HELLO_FRESH = 4000;       // свежую попытку не перезапускаем: её offer ещё в пути
const REASSERT_GAP = 3000;      // не чаще этого напоминаем серверу, что мы в звонке
const AUTOPLAY_BLOCKED = 'Браузер заблокировал звук — нажмите на страницу';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const newSid = () => Math.random().toString(36).slice(2, 10);

export function createVoice({ post, iceServers, onChange }) {
  const peers = new Map();    // seat -> соединение, см. makePeer
  const attempts = new Map(); // seat -> сколько попыток подряд сорвалось
  const pending = new Map();  // seat -> таймер следующей попытки
  let inCall = new Set();     // места в звонке по последнему снимку
  let local = null;           // поток микрофона
  let mySeat = -1;
  let active = false;
  let joining = false;
  let muted = false;
  let error = '';
  let audioCtx = null;
  let meterTimer = 0;
  let reassertAt = 0;
  let unlockArmed = false;

  const notify = () => onChange(snapshot());

  function snapshot() {
    const parts = {};
    for (const [seat, p] of peers) {
      parts[seat] = { connected: p.connected, speaking: p.speaking };
    }
    return { active, muted, error, peers: parts, supported: isSupported() };
  }

  function isSupported() {
    return Boolean(navigator.mediaDevices && navigator.mediaDevices.getUserMedia && window.SimplePeer);
  }

  async function join(seat) {
    if (active || joining) return active;
    mySeat = seat;
    error = '';
    if (!isSupported()) {
      // getUserMedia работает только в защищённом контексте: https или localhost.
      error = window.isSecureContext
        ? 'Браузер не поддерживает голосовую связь'
        : 'Нужен https: без него браузер не даёт доступ к микрофону';
      notify();
      return false;
    }
    // Звук заводим, пока идёт щелчок по кнопке: AudioContext, созданный
    // позже, браузер вправе оставить на паузе.
    ensureAudio();
    joining = true;
    try {
      local = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
        video: false,
      });
    } catch (e) {
      error = e && e.name === 'NotAllowedError'
        ? 'Доступ к микрофону запрещён'
        : `Микрофон недоступен: ${e.message || e}`;
      notify();
      return false;
    } finally {
      joining = false;
    }
    active = true;
    muted = false;
    startMeters();
    // Младшие места позвонят сами, увидев нас в снимке. Но если сервер уже
    // считал нас в звонке (вкладку перезагрузили, а выход до него не дошёл),
    // снимок для них не изменится — тогда попросим перезвонить.
    const stale = inCall.has(mySeat);
    reassertAt = Date.now();
    try {
      await post('voice', { active: true });
    } catch {
      reassertAt = 0; // напомним серверу на следующем снимке
    }
    if (stale) for (const s of inCall) if (s < mySeat) hello(s, null);
    notify();
    return true;
  }

  async function leave() {
    active = false;
    for (const seat of [...peers.keys()]) dropPeer(seat);
    for (const seat of [...pending.keys()]) cancelRetry(seat);
    attempts.clear();
    if (local) {
      local.getTracks().forEach((t) => t.stop());
      local = null;
    }
    stopMeters();
    error = '';
    const sent = post('voice', { active: false });
    notify();
    try { await sent; } catch { /* из звонка выходим и без ответа сервера */ }
  }

  function toggleMute() {
    if (!local) return;
    muted = !muted;
    local.getAudioTracks().forEach((t) => { t.enabled = !muted; });
    notify();
  }

  // syncPeers вызывается на каждый снимок: сервер присылает список мест,
  // которые сейчас в звонке.
  function syncPeers(voiceSeats, seat) {
    mySeat = seat;
    inCall = new Set(voiceSeats || []);
    const want = new Set();
    if (active && mySeat >= 0) {
      for (const s of inCall) if (s !== mySeat) want.add(s);
    }
    for (const s of [...peers.keys()]) if (!want.has(s)) dropPeer(s);
    for (const s of [...pending.keys()]) if (!want.has(s)) cancelRetry(s);
    for (const s of [...attempts.keys()]) if (!want.has(s)) attempts.delete(s);
    if (!active || mySeat < 0) return;

    if (!inCall.has(mySeat)) {
      // Мы в звонке, а сервер нас не видит: поток SSE оборвался, и сервер
      // вычеркнул нас вместе со старой подпиской. Новых соединений не
      // начинаем, пока он не вернёт нас в список.
      reassert();
      return;
    }
    for (const other of want) {
      if (mySeat < other && !peers.has(other) && !pending.has(other)) makePeer(other, true);
    }
  }

  function handleSignal(envelope) {
    const from = envelope.from;
    const data = envelope.data || {};
    if (!active || from === mySeat) return; // нас нет в звонке — сигнал не наш
    if (data.hello) {
      onHello(from, data.sid);
      return;
    }
    const signals = Array.isArray(data.signals) ? data.signals : [];
    let entry = peers.get(from);
    if (mySeat < from) {
      // Звоним мы: принимаем только ответы на текущую попытку.
      if (!entry || entry.sid !== data.sid) return;
      if (signals.some((s) => s && s.type === 'answer')) {
        entry.answered = true;
        clearTimeout(entry.waitAnswer);
      } else if (!entry.answered && signals.some((s) => s && s.candidate)) {
        // Сигналы попытки идут строго по очереди, answer — первым. Пришли
        // кандидаты без него — значит, он потерялся. Ждать бесполезно, а
        // звук тем временем шёл бы только в одну сторону.
        fail(entry, 'answer потерялся');
        return;
      }
    } else if (!entry || entry.sid !== data.sid) {
      // Новая попытка звонящего: прежнее соединение с ним больше не нужно.
      // Хвосты закрытых попыток (кандидаты без offer'а) отбрасываем.
      if (!signals.some((s) => s && s.type === 'offer')) return;
      dropPeer(from);
      entry = makePeer(from, false, data.sid);
      if (!entry) return;
    }
    for (const s of signals) {
      try {
        entry.peer.signal(s);
      } catch (e) {
        fail(entry, e && e.message);
        return;
      }
    }
  }

  // Старшее место просит позвонить ему заново: живого соединения с нами у
  // него нет. sid — наша попытка, которую оно видело последней, или null,
  // если не видело ни одной.
  function onHello(from, sid) {
    if (!(mySeat < from) || !inCall.has(from) || !inCall.has(mySeat)) return;
    const entry = peers.get(from);
    if (entry && entry.sid !== sid && Date.now() - entry.born < HELLO_FRESH) return;
    if (!entry && sid && pending.has(from)) return; // обрыв мы заметили сами, повтор уже назначен
    dropPeer(from);
    if (sid) {
      // У него сорвалась попытка — перезваниваем с обычной паузой: если сбой
      // повторяется, без неё звонки пошли бы по кругу без остановки.
      scheduleRetry(from);
    } else {
      cancelRetry(from);
      makePeer(from, true);
    }
  }

  function makePeer(seat, initiator, sid = newSid()) {
    if (!local || !window.SimplePeer) return null;
    const peer = new window.SimplePeer({
      initiator,
      trickle: true,
      stream: local,
      config: { iceServers, bundlePolicy: 'max-bundle' },
    });

    const entry = {
      seat, sid, initiator, peer, born: Date.now(),
      connected: false, speaking: false, audio: null, source: null, analyser: null,
      outbox: [], sending: false, timer: 0, waitAnswer: 0, answered: false,
    };
    peers.set(seat, entry);
    const current = () => peers.get(seat) === entry;
    if (initiator) {
      // Сторожа ставит звонящий: отвечающий просто ждёт следующего offer'а.
      // На повторах ждём дольше — вдруг сеть просто медленная.
      const patience = 1 + Math.min(attempts.get(seat) || 0, 2);
      arm(entry, CONNECT_TIMEOUT * patience);
      entry.waitAnswer = setTimeout(() => fail(entry, 'нет ответа на offer'), ANSWER_TIMEOUT * patience);
    }

    peer.on('signal', (data) => { if (current()) send(entry, data); });
    // Связь есть, как только ICE нашёл путь; 'connect' simple-peer ждёт ещё
    // и канала данных, который звонку не нужен.
    peer.on('connect', () => up(entry));
    peer.on('iceStateChange', (state) => {
      if (!current()) return;
      if (state === 'connected' || state === 'completed') {
        up(entry);
      } else if (state === 'disconnected' && entry.connected) {
        entry.connected = false;
        arm(entry, DROP_GRACE);
        notify();
      }
    });
    peer.on('stream', (stream) => { if (current()) attachAudio(entry, stream); });
    peer.on('close', () => fail(entry, 'соединение закрыто'));
    peer.on('error', (e) => fail(entry, e && e.message));
    notify();
    return entry;
  }

  function up(entry) {
    if (peers.get(entry.seat) !== entry || entry.connected) return;
    entry.connected = true;
    attempts.delete(entry.seat);
    disarm(entry);
    notify();
  }

  // fail закрывает сорвавшуюся попытку и запускает следующую: звонящий
  // перезванивает сам, отвечающий просит перезвонить.
  function fail(entry, why) {
    if (peers.get(entry.seat) !== entry) return; // уже заменено или закрыто нами
    console.warn('voice: соединение с местом', entry.seat, 'оборвалось:', why);
    dropPeer(entry.seat);
    if (!active || !inCall.has(entry.seat)) return;
    if (entry.initiator) scheduleRetry(entry.seat);
    else hello(entry.seat, entry.sid);
  }

  function scheduleRetry(seat) {
    cancelRetry(seat);
    const n = attempts.get(seat) || 0;
    attempts.set(seat, n + 1);
    pending.set(seat, setTimeout(() => {
      pending.delete(seat);
      if (active && inCall.has(seat) && inCall.has(mySeat) && !peers.has(seat)) makePeer(seat, true);
    }, Math.min(RETRY_BASE * 2 ** n, RETRY_MAX)));
  }

  function cancelRetry(seat) {
    clearTimeout(pending.get(seat));
    pending.delete(seat);
  }

  function arm(entry, ms) {
    clearTimeout(entry.timer);
    entry.timer = setTimeout(() => {
      if (!entry.connected) fail(entry, 'не дождались соединения');
    }, ms);
  }

  function disarm(entry) {
    clearTimeout(entry.timer);
    entry.timer = 0;
  }

  function dropPeer(seat) {
    const entry = peers.get(seat);
    if (!entry) return;
    peers.delete(seat);
    disarm(entry);
    clearTimeout(entry.waitAnswer);
    try { entry.peer.destroy(); } catch { /* уже закрыт */ }
    if (entry.audio) {
      entry.audio.pause();
      entry.audio.srcObject = null;
    }
    if (entry.source) entry.source.disconnect();
    notify();
  }

  function reassert() {
    if (Date.now() - reassertAt < REASSERT_GAP) return;
    reassertAt = Date.now();
    post('voice', { active: true }).catch(() => { /* повторим на следующем снимке */ });
  }

  // --- доставка сигналов ---

  // Сигналы одного соединения уходят по очереди. Пока летит запрос, новые
  // кандидаты копятся и уходят следующим одним пакетом: при trickle их
  // набирается с десяток, а лимит запросов в nginx общий на IP.
  function send(entry, signal) {
    entry.outbox.push(signal);
    if (!entry.sending) flush(entry);
  }

  async function flush(entry) {
    entry.sending = true;
    const alive = () => active && peers.get(entry.seat) === entry;
    while (entry.outbox.length && alive()) {
      const signals = entry.outbox.splice(0);
      await deliver(entry.seat, { sid: entry.sid, signals }, alive);
    }
    entry.sending = false;
  }

  function hello(seat, sid) {
    deliver(seat, { hello: true, sid: sid || null }, () => active);
  }

  // deliver повторяет запрос при сбое: без повтора потерянный offer или
  // answer стоил бы целого CONNECT_TIMEOUT.
  async function deliver(seat, data, alive) {
    for (let i = 0; i < 3; i++) {
      try {
        await post('signal', { to: seat, data });
        return;
      } catch {
        if (i === 2) return;
        await sleep(300 * 2 ** i);
        if (!alive()) return;
      }
    }
  }

  // --- звук ---

  function attachAudio(entry, stream) {
    const audio = entry.audio || new Audio();
    audio.autoplay = true;
    audio.srcObject = stream;
    entry.audio = audio;
    play(audio);
    if (!entry.analyser) {
      const meter = makeAnalyser(stream);
      if (meter) {
        entry.source = meter.source;
        entry.analyser = meter.analyser;
      }
    }
    notify();
  }

  function play(audio) {
    audio.play().catch(() => {
      error = AUTOPLAY_BLOCKED;
      armUnlock();
      notify();
    });
  }

  // Запретил браузер автозапуск — звук включится по первому щелчку или
  // клавише в любом месте страницы.
  function armUnlock() {
    if (unlockArmed) return;
    unlockArmed = true;
    const unlock = () => {
      unlockArmed = false;
      document.removeEventListener('pointerdown', unlock, true);
      document.removeEventListener('keydown', unlock, true);
      ensureAudio();
      if (error === AUTOPLAY_BLOCKED) error = '';
      for (const entry of peers.values()) {
        if (entry.audio && entry.audio.paused) play(entry.audio);
      }
      notify();
    };
    document.addEventListener('pointerdown', unlock, true);
    document.addEventListener('keydown', unlock, true);
  }

  function ensureAudio() {
    try {
      audioCtx = audioCtx || new (window.AudioContext || window.webkitAudioContext)();
      if (audioCtx.state === 'suspended') audioCtx.resume().catch(() => {});
    } catch {
      audioCtx = null;
    }
    return audioCtx;
  }

  // --- индикатор «кто говорит» ---

  function makeAnalyser(stream) {
    const ctx = ensureAudio();
    if (!ctx) return null;
    try {
      const source = ctx.createMediaStreamSource(stream);
      const analyser = ctx.createAnalyser();
      analyser.fftSize = 256;
      source.connect(analyser);
      return { source, analyser };
    } catch {
      return null; // индикатор — украшение, без него звонок работает
    }
  }

  function startMeters() {
    stopMeters();
    const buffer = new Uint8Array(128);
    meterTimer = setInterval(() => {
      let changed = false;
      for (const entry of peers.values()) {
        if (!entry.analyser) continue;
        entry.analyser.getByteFrequencyData(buffer);
        let sum = 0;
        for (const v of buffer) sum += v;
        const speaking = sum / buffer.length > SPEAK_THRESHOLD;
        if (speaking !== entry.speaking) {
          entry.speaking = speaking;
          changed = true;
        }
      }
      if (changed) notify();
    }, 250);
  }

  function stopMeters() {
    if (meterTimer) clearInterval(meterTimer);
    meterTimer = 0;
  }

  return { join, leave, toggleMute, syncPeers, handleSignal, state: snapshot };
}
