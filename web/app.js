// Клиент «Осады»: рисует присланное сервером состояние и отправляет действия.
// Правила здесь не дублируются — что разрешено, решает сервер, а подсветку
// он же присылает в снимке (moves/targets/zone).

import { clampView, draw, drawItemIcon, drawUnitIcon, layout, pickTile, resize, SIDE_COLORS } from './render.js';
import { createMusic } from './music.js';
import { createVoice } from './voice.js';

const $ = (id) => document.getElementById(id);
const STORE_KEY = 'siege.session';

const app = {
  meta: null,
  snap: null,
  session: null, // {room, token}
  es: null,
  clockSkew: 0,
  ui: { mode: 'select', selectedId: 0, unit: null, ability: null, build: null, court: null, hover: null, inspect: null, moves: [], targets: [], zone: [], segments: [], buildSpots: [], courtSpots: [], healSpots: [], chargeSpots: [], unloadSpots: [], unloadId: 0 },
  view: { scale: 1, ox: 0, oy: 0 }, // зум и панорама: карта больше экрана
  showThreat: false,                // накладка «куда достаёт враг»
  voice: null,     // модуль голосовой связи, поднимается после загрузки meta
  music: null,     // фоновая музыка: живёт отдельно от партии и от сервера
  channel: 'all',  // канал чата: общий или осадный (двое осаждающих)
  siegeSeen: 0,    // сколько осадных сообщений уже показано — для значка
  drop: null,      // открытое окно пульта: 'chat', 'menu' или ничего
  chatSeenMs: null, // время последней реплики, которую видели в открытом чате
};

let canvas, ctx;

// --- сеть ---

async function api(path, body) {
  const res = await fetch(path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return null;
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) throw new Error((data && data.error) || `ошибка ${res.status}`);
  return data;
}

function connect() {
  if (app.es) app.es.close();
  const { room, token } = app.session;
  const es = new EventSource(`/api/rooms/${room}/events?token=${encodeURIComponent(token)}`);
  es.addEventListener('state', (e) => onSnapshot(JSON.parse(e.data)));
  es.addEventListener('signal', (e) => {
    if (app.voice) app.voice.handleSignal(JSON.parse(e.data));
  });
  es.onerror = () => {
    // EventSource переподключается сам; сообщаем только о потере связи.
    if (es.readyState === EventSource.CLOSED) toast('Связь с сервером потеряна, пробуем снова…');
  };
  app.es = es;
}

function saveSession(s) {
  app.session = s;
  try { localStorage.setItem(STORE_KEY, JSON.stringify(s)); } catch { /* приватный режим */ }
  const url = new URL(location.href);
  url.searchParams.set('room', s.room);
  history.replaceState(null, '', url);
}

function loadSession() {
  try { return JSON.parse(localStorage.getItem(STORE_KEY) || 'null'); } catch { return null; }
}

// forgetSession — полный выход: обрываем поток, забываем токен и убираем код
// комнаты из адреса. Без последнего ссылка затаскивала бы обратно в ту же
// партию при каждом следующем заходе.
function forgetSession() {
  if (app.es) { app.es.close(); app.es = null; }
  if (app.voice && app.voice.state().active) app.voice.leave();
  app.session = null;
  app.snap = null;
  app.ui.selectedId = 0;
  app.ui.mode = 'select';
  try { localStorage.removeItem(STORE_KEY); } catch { /* приватный режим */ }
  history.replaceState(null, '', location.pathname);
}

// leaveRoom выпускает игрока из комнаты. Сервер освобождает место, если
// партия ещё не началась; из начатой осады выйти можно, но вернуться — нет,
// поэтому спрашиваем.
async function leaveRoom(started) {
  const msg = started
    ? 'Выйти из партии? Вернуться в неё будет нельзя: место останется за вами, но доступ к нему пропадёт.'
    : 'Выйти из комнаты? Место освободится для другого игрока.';
  if (!confirm(msg)) return;
  const session = app.session;
  // Сессию забываем в любом случае: даже если сервер недоступен, застревать
  // в комнате на своей же вкладке игрок не должен.
  forgetSession();
  show('screen-home');
  homeError('');
  if (!session) return;
  try {
    await api(`/api/rooms/${session.room}/leave`, { token: session.token });
  } catch (e) {
    toast(`Сервер не подтвердил выход: ${e.message}`);
  }
}

// --- экраны ---

function show(screen) {
  for (const id of ['screen-home', 'screen-lobby', 'screen-game']) $(id).hidden = id !== screen;
  const game = screen === 'screen-game';
  $('music-pill').hidden = game; // в партии громкость живёт в меню «⋯»
  if (app.music) app.music.play(game ? 'game' : 'menu');
}

// --- музыка ---

let lastMusicVol = 0.6; // куда вернуть громкость, если её выключили кнопкой

function paintMusic() {
  if (!app.music) return;
  const st = app.music.state();
  if (st.volume > 0) lastMusicVol = st.volume;
  // Экран, для которого трек не выбран, пульта не показывает: ползунок,
  // который сейчас ничем не управляет, только сбивает с толку.
  $('music-pill').classList.toggle('gone', st.failed || !app.music.has('menu'));
  $('menu-music').hidden = st.failed || !app.music.has('game');
  for (const id of ['btn-music-home', 'btn-music']) {
    const b = $(id);
    if (!b) continue;
    b.classList.toggle('off', !st.on);
    b.setAttribute('aria-pressed', String(st.on));
    b.title = st.on ? 'Выключить музыку' : 'Включить музыку';
    const ico = b.querySelector('.ico');
    if (ico) ico.className = st.on ? 'ico music' : 'ico mute';
  }
  $('music-label').textContent = st.on ? 'Музыка' : 'Музыка выключена';
  for (const id of ['music-vol-home', 'music-vol']) {
    const r = $(id);
    if (!r) continue;
    // Ползунок, который сейчас тянут, не трогаем: он сам источник значения.
    if (document.activeElement !== r) r.value = String(Math.round(st.volume * 100));
    r.style.setProperty('--fill', `${r.value}%`); // залитая часть дорожки
  }
}

function toggleMusic() {
  const st = app.music.state();
  app.music.setVolume(st.volume > 0 ? 0 : (lastMusicVol || 0.6));
}

function homeError(msg) {
  const el = $('home-error');
  el.textContent = msg;
  el.hidden = !msg;
}

let toastTimer = 0;
function toast(msg) {
  const el = $('toast');
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 3200);
}

// --- вход в игру ---

async function createHotseat() {
  const bots = ['hs1-who', 'hs2-who', 'hs3-who'].map((id) => $(id).value);
  // Боту имя не обязательно: сервер даст ему имя одного из домов лора.
  const names = ['hs1', 'hs2', 'hs3'].map((id, i) => $(id).value.trim() || (bots[i] ? '' : `Игрок ${i + 1}`));
  try {
    const r = await api('/api/rooms', { mode: 'hotseat', names, bots, map: $('hs-map').value });
    saveSession({ room: r.room, token: r.token });
    connect();
  } catch (e) { homeError(e.message); }
}

// createSolo — партия против двух ботов на одном экране. Человек садится
// на второе место и осаждает: начинать с наступления понятнее, чем с
// обороны. Держать замок против двух ботов можно с панели общего экрана.
async function createSolo() {
  const level = $('solo-level').value;
  saveLevel(level);
  const name = $('solo-name').value.trim() || 'Игрок';
  try {
    const r = await api('/api/rooms', {
      mode: 'hotseat', names: ['', name, ''], bots: [level, '', level], map: $('solo-map').value,
    });
    saveSession({ room: r.room, token: r.token });
    connect();
  } catch (e) { homeError(e.message); }
}

async function createOnline() {
  try {
    const r = await api('/api/rooms', { mode: 'online', name: $('net-name').value.trim() });
    saveSession({ room: r.room, token: r.token });
    connect();
  } catch (e) { homeError(e.message); }
}

async function joinOnline() {
  const code = $('net-code').value.trim().toUpperCase();
  if (!code) return homeError('Введите код комнаты');
  try {
    const r = await api(`/api/rooms/${code}/join`, { name: $('net-name').value.trim() });
    saveSession({ room: r.room, token: r.token });
    connect();
  } catch (e) { homeError(e.message); }
}

// --- приём состояния ---

// useBoard — подмешать в мету геометрию той карты, на которой идёт партия.
// Карт две, и всё остальное в клиенте — отрисовка, подсветки, карточка
// клетки — по-прежнему читает поля меты, не зная, что их стало по набору на
// карту.
function useBoard(snap) {
  const raw = app.rawMeta || app.meta;
  if (!raw || !raw.boards) return;
  app.rawMeta = raw;
  const key = (snap.game && snap.game.map) || snap.map || 'classic';
  if (app.boardKey === key) return;
  const board = raw.boards[key] || raw.boards.classic;
  app.boardKey = key;
  app.meta = Object.assign({}, raw, board);
}

function onSnapshot(snap) {
  app.snap = snap;
  app.clockSkew = snap.nowMs - Date.now();
  useBoard(snap);

  if (!snap.game) {
    show('screen-lobby');
    renderLobby(snap);
    return;
  }
  show('screen-game');
  syncUI(snap);
  renderHUD(snap);
  if (app.voice) app.voice.syncPeers(snap.voice, snap.you ? snap.you.seat : -1);
  render();
}

// syncUI сбрасывает выбор, если он больше не актуален (чужой ход, юнит погиб).
function syncUI(snap) {
  const g = snap.game;
  const units = g.units || [];
  const mine = units.find((u) => u.id === app.ui.selectedId && u.mine);
  if (!g.you.myTurn) {
    app.ui.mode = 'select';
    app.ui.selectedId = 0;
    app.ui.moves = app.ui.targets = app.ui.buildSpots = app.ui.healSpots = app.ui.chargeSpots = app.ui.unloadSpots = [];
    app.ui.unloadId = 0;
    return;
  }
  if (!mine) {
    app.ui.selectedId = 0;
    app.ui.moves = app.ui.targets = app.ui.buildSpots = app.ui.healSpots = app.ui.chargeSpots = app.ui.unloadSpots = [];
    app.ui.unloadId = 0;
    if (['build', 'heal', 'charge', 'unload'].includes(app.ui.mode)) app.ui.mode = 'select';
  } else {
    app.ui.moves = mine.moves || [];
    app.ui.targets = mine.targets || [];
    if (app.ui.mode === 'build') {
      app.ui.buildSpots = buildSpotsFor(mine, app.ui.build);
      if (!app.ui.buildSpots.length) app.ui.mode = 'select';
    }
    app.ui.healSpots = mine.healAt || [];
    if (app.ui.mode === 'heal' && !app.ui.healSpots.length) app.ui.mode = 'select';
    app.ui.chargeSpots = mine.chargeAt || [];
    if (app.ui.mode === 'charge' && !app.ui.chargeSpots.length) app.ui.mode = 'select';
    app.ui.unloadSpots = [...(mine.unloadAt || []), ...(mine.exitAt || [])];
    const aboard = (mine.cargo || []).some((c) => c.id === app.ui.unloadId);
    if (app.ui.mode === 'unload' && (!aboard || !app.ui.unloadSpots.length)) {
      app.ui.mode = 'select';
      app.ui.unloadId = 0;
    }
  }
  // Умения и стройка во дворе есть только у замка. В горячем кресле роль
  // меняется в том же окне, и без сброса осаждающий получал подсветку чужих
  // умений — и кнопку, которую сервер всё равно отобьёт.
  if (g.you.side !== 'defender') {
    if (app.ui.mode === 'ability' || app.ui.mode === 'construct') app.ui.mode = 'select';
    app.ui.ability = null;
    app.ui.court = null;
    app.ui.segments = [];
  }
  app.ui.zone = deployZone(g);
  app.ui.courtSpots = g.you.court || [];
  if (app.ui.mode === 'construct' && (!app.ui.court || !app.ui.courtSpots.length)) app.ui.mode = 'select';
  if (app.ui.mode === 'bombard' && !(mine && mine.area)) app.ui.mode = 'select';
  if (app.ui.mode === 'strike' && !mine) app.ui.mode = 'select';
  if (app.ui.mode === 'deploy' && !app.ui.unit) app.ui.mode = 'select';
  if (app.ui.mode === 'ability' && !app.ui.ability) app.ui.mode = 'select';
  if (app.ui.mode === 'build' && !app.ui.build) app.ui.mode = 'select';
}

// buildSpotsFor — какие клетки подсвечивать под выбранную стройку. Сервер
// присылает по списку на каждый вид работ: где ставить, что чинить, какой
// пролом заколачивать, какую клетку рва засыпать.
function buildSpotsFor(unit, build) {
  if (!unit) return [];
  switch (build) {
    case 'repair': return unit.fixAt || [];
    case 'outpost': return unit.outpostAt || [];
    case 'watchpost': return unit.watchAt || [];
    case 'woodgate': return unit.gateAt || [];
    case 'fascine': return unit.fascineAt || [];
    case 'warehouse': return unit.storeAt || [];
    case 'mantlet': return unit.mantletAt || [];
    default: return unit.buildAt || [];
  }
}

// buildMenu — что умеет строить выбранный отряд. Инженеры у сторон разные:
// замок чинит и заколачивает, осада ставит частокол и катит щиты.
function buildMenu(side) {
  return (side === 'defender' ? app.meta.builds : app.meta.sapperBuilds) || [];
}

// deployZone — куда можно выставить выбранный в лавке отряд. Сервер шлёт
// общую зону стороны, но осадную башню собирают только в лагере, и
// подсвечивать аванпост для неё — значит врать игроку.
function deployZone(g) {
  const zone = g.you.zone || [];
  const spec = app.meta.specs.find((s) => s.name === app.ui.unit);
  if (!spec || !spec.campOnly) return zone;
  const camp = g.you.side === 'attackerA' ? 'a' : 'b';
  return zone.filter((p) => app.meta.tiles[p.y][p.x] === camp);
}

function renderLobby(snap) {
  $('lobby-code').textContent = snap.room;
  const list = $('lobby-players');
  // Список перестраиваем, только когда в нём что-то поменялось: иначе
  // открытый выбор уровня у бота закрывался бы на каждом снимке.
  const sig = JSON.stringify(snap.players || []);
  if (list.dataset.sig !== sig) {
    list.dataset.sig = sig;
    list.innerHTML = '';
    for (const p of snap.players || []) list.appendChild(lobbyRow(p));
  }
  $('lobby-need').textContent = snap.needPlayers > 0
    ? `Ждём ещё ${snap.needPlayers} ${plural(snap.needPlayers, 'игрока', 'игроков', 'игроков')}…`
    : 'Все на месте, начинаем!';
  $('lobby-bot').hidden = !(snap.needPlayers > 0);
}

// lobbyRow — строка игрока в лобби. У бота вместо точки связи — его
// уровень и кнопка «убрать»: пока партия не началась, место можно вернуть.
// Роль видна уже здесь: она закреплена за местом на всю партию.
function lobbyRow(p) {
  const li = document.createElement('li');
  const side = (app.meta.seatSides || [])[p.seat];
  li.className = [side, p.left ? 'left' : ''].filter(Boolean).join(' ');
  const name = document.createElement('span');
  name.textContent = p.name;
  if (SIDE_COLORS[side]) {
    const role = document.createElement('span');
    role.className = 'role-tag';
    role.textContent = SIDE_COLORS[side].name;
    name.appendChild(role);
  }
  li.appendChild(name);
  if (p.bot) {
    const tag = document.createElement('span');
    tag.className = 'bot-tag';
    tag.textContent = 'бот';
    name.appendChild(tag);
    const tools = document.createElement('span');
    tools.className = 'tools';
    const level = document.createElement('select');
    fillLevels(level, { value: p.bot, short: true });
    level.setAttribute('aria-label', `Сложность бота ${p.name}`);
    level.onchange = () => botOp({ op: 'level', seat: p.seat, level: level.value });
    const drop = document.createElement('button');
    drop.className = 'quiet';
    drop.textContent = '×';
    drop.title = 'Убрать бота';
    drop.setAttribute('aria-label', `Убрать бота ${p.name}`);
    drop.onclick = () => botOp({ op: 'remove', seat: p.seat });
    tools.append(level, drop);
    li.appendChild(tools);
    return li;
  }
  const dot = document.createElement('span');
  dot.className = 'dot' + (p.online ? '' : ' off');
  dot.title = p.left ? 'вышел из комнаты' : p.online ? 'на связи' : 'нет связи';
  li.appendChild(dot);
  return li;
}

// --- боты ---
//
// Уровни приходят в мете: сервер держит их описание у себя, чтобы подсказка
// в интерфейсе не расходилась с тем, как бот на самом деле играет.

const LEVEL_KEY = 'siege.botLevel';

function botLevels() { return (app.meta && app.meta.botLevels) || []; }

// fillLevels заполняет выбор уровня. human — первым пунктом «Человек»
// (место на общем экране), short — без звания, для тесных мест.
function fillLevels(select, { human = false, short = false, value = '' } = {}) {
  select.innerHTML = '';
  if (human) select.add(new Option('Человек', ''));
  for (const l of botLevels()) {
    const text = human ? `Бот: ${l.label}` : short ? l.label : `${l.title} — ${l.label}`;
    select.add(new Option(text, l.key));
  }
  select.value = value;
}

function levelInfo(key) { return botLevels().find((l) => l.key === key); }

function savedLevel() {
  try { return localStorage.getItem(LEVEL_KEY) || 'normal'; } catch { return 'normal'; }
}

function saveLevel(level) {
  try { localStorage.setItem(LEVEL_KEY, level); } catch { /* приватный режим */ }
}

// botOf — уровень бота на месте seat; пусто — там человек.
function botOf(snap, seat) {
  const p = (snap.players || []).find((x) => x.seat === seat && x.bot);
  return p ? p.bot : '';
}

async function botOp(body) {
  if (!app.session) return;
  try {
    await api(`/api/rooms/${app.session.room}/bot`, { token: app.session.token, ...body });
  } catch (e) {
    if (app.snap && app.snap.game) toast(e.message); else homeError(e.message);
  }
}

// Карты. Их две, и выбирают её при создании партии: сменить карту посреди
// осады нельзя — это другая партия.
const MAP_HINTS = {
  classic: 'Замок поперёк теснины, лагеря внизу, ров вдоль стены. 28×22.',
  split: 'Замок посреди поля, осаждающие в противоположных углах, река сквозь двор, ' +
    'форпост у истока. Втрое больше: 44×44, кольцо стены в 68 сегментов, четверо ворот.',
};

function fillMaps(select, value) {
  const boards = (app.rawMeta && app.rawMeta.boards) || {};
  select.innerHTML = '';
  for (const key of ['classic', 'split']) {
    if (boards[key]) select.add(new Option(boards[key].name, key));
  }
  select.value = boards[value] ? value : 'classic';
}

function showMapHint(select, hintId) {
  const el = $(hintId);
  if (el) el.textContent = MAP_HINTS[select.value] || '';
}

function savedMap() {
  try { return localStorage.getItem('siege.map') || 'classic'; } catch { return 'classic'; }
}

function saveMap(key) {
  try { localStorage.setItem('siege.map', key); } catch { /* приватный режим */ }
}

// showLevelHint — что ждать от выбранного уровня: под выбором на главном.
function showLevelHint() {
  const l = levelInfo($('solo-level').value);
  $('solo-hint').textContent = l ? `${l.title}. ${l.hint}` : '';
}

// renderMenuBots — боты партии в меню «⋯». Уровень можно сменить посреди
// осады, а место ушедшего или пропавшего со связи игрока — отдать боту:
// иначе партия на троих встала бы на его ходе.
function renderMenuBots(snap) {
  const box = $('menu-bots');
  const rows = [];
  const mySeat = snap.you ? snap.you.seat : -1;
  for (const p of snap.players || []) {
    if (p.bot) rows.push({ free: false, p });
    else if (snap.mode === 'online' && p.seat !== mySeat && (p.left || !p.online)) rows.push({ free: true, p });
  }
  const sig = JSON.stringify(rows.map((r) => [r.free, r.p.seat, r.p.name, r.p.bot || '', Boolean(r.p.left)]));
  if (box.dataset.sig === sig) return;
  box.dataset.sig = sig;
  box.hidden = rows.length === 0;
  box.innerHTML = '';
  if (!rows.length) return;
  const head = document.createElement('h4');
  head.textContent = 'Боты';
  box.appendChild(head);
  for (const { free, p } of rows) {
    const row = document.createElement('div');
    row.className = 'row';
    const label = document.createElement('span');
    const level = document.createElement('select');
    if (!free) {
      label.textContent = p.name;
      fillLevels(level, { value: p.bot, short: true });
      level.setAttribute('aria-label', `Сложность бота ${p.name}`);
      level.onchange = () => botOp({ op: 'level', seat: p.seat, level: level.value });
      row.append(label, level);
    } else {
      label.textContent = `Вместо: ${p.name}`;
      label.title = p.left ? 'Игрок вышел из партии' : 'Игрок не на связи';
      fillLevels(level, { value: savedLevel(), short: true });
      level.setAttribute('aria-label', `Сложность бота вместо ${p.name}`);
      const put = document.createElement('button');
      put.type = 'button';
      put.textContent = 'Посадить';
      put.onclick = () => {
        if (!confirm(`Отдать место «${p.name}» боту? Вернуться в партию этот игрок уже не сможет.`)) return;
        saveLevel(level.value);
        botOp({ op: 'replace', seat: p.seat, level: level.value });
      };
      row.append(label, level, put);
    }
    box.appendChild(row);
  }
}

function renderHUD(snap) {
  const g = snap.game;
  $('hud-day').textContent = `${g.day}/${g.days}`;
  // Семьдесят дней — долгая осада: сколько прошло, видно полоской, без счёта в уме.
  $('hud-day-fill').style.width = `${Math.min(100, (g.day / g.days) * 100)}%`;

  // Король — отдельная строка: терять его нельзя, и следить за ним приходится
  // не так, как за остальной армией.
  const king = $('hud-king');
  if (g.you.king) {
    king.hidden = false;
    const ratio = g.you.king.hp / g.you.king.maxHp;
    king.textContent = `Король ${g.you.king.hp}/${g.you.king.maxHp}`;
    king.classList.toggle('hurt', ratio <= 0.5);
  } else {
    king.hidden = true;
  }
  const role = $('hud-role');
  role.textContent = (SIDE_COLORS[g.you.side] ? SIDE_COLORS[g.you.side].name : '—')
    + (g.you.rally ? ' · ×2 урон' : '');
  role.classList.toggle('rally-on', Boolean(g.you.rally));
  // Ход: в свой — кнопка, в чужой — кто ходит, точкой цвета его стороны.
  const turnSeat = (g.seats || []).find((s) => s.turn);
  $('btn-end').hidden = !g.you.myTurn;
  $('hud-wait').hidden = g.you.myTurn;
  $('hud-turn').textContent = g.turnName + (turnSeat && botOf(snap, turnSeat.seat) ? ' (бот)' : '');
  $('wait-dot').style.background = turnSeat && SIDE_COLORS[turnSeat.side] ? SIDE_COLORS[turnSeat.side].edge : '';

  const seats = $('hud-seats');
  seats.innerHTML = '';
  const hotseat = snap.mode === 'hotseat';
  // На общем экране «вы» — тот, чей ход; но если человек за ним один, а
  // остальные места у ботов, «вы» — это он.
  const humans = (g.seats || []).filter((s) => !botOf(snap, s.seat));
  for (const s of g.seats || []) {
    const side = SIDE_COLORS[s.side].name;
    const level = botOf(snap, s.seat);
    const mine = hotseat ? humans.length === 1 && humans[0].seat === s.seat : s.seat === g.you.seat;
    const tail = level ? ' · бот' : mine ? ' · вы' : '';
    const info = level && levelInfo(level);
    const div = document.createElement('div');
    div.className = `seat ${s.side}${s.turn ? ' active' : ''}${level ? ' bot' : ''}`;
    // Очков на плашке нет: партия — одна осада, и считают их только в конце.
    div.title = `${s.name} — ${side}${info ? `, бот: ${info.title.toLowerCase()} (${info.label})` : ''}`;
    div.innerHTML = `<span class="name">${escapeHTML(s.name)}</span><span class="initial">${escapeHTML(initial(s.name))}</span>`
      + `<span class="role">${side}${tail}</span>`;
    seats.appendChild(div);
  }
  renderMenuBots(snap);

  renderAP(g);
  renderVoice(snap);
  renderWallets(g);
  renderCard(g);
  renderStrip();
  renderChat(snap);
  renderChatButton(snap);
  renderOverlay(snap);
}

// --- кошельки ---
//
// Траты сгруппированы по ресурсу, которым за них платят: припасы, серебро и
// то, что стоит одних только очков действия. Внутри кошелька — по действию:
// нанять, строить, умения. Цена всегда с единицами, а если сейчас нельзя —
// в строке сказано почему: серая кнопка без объяснений заставляла гадать.

const RES_WORDS = {
  supplies: (n) => plural(n, 'припас', 'припаса', 'припасов'),
  silver: () => 'серебра',
  ap: (n) => plural(n, 'очко действия', 'очка действия', 'очков действия'),
};

function renderAP(g) {
  $('hud-ap').textContent = `${g.ap}/${g.apMax}`;
  // В чужой ход полоска показывает очки того, кто сейчас ходит, —
  // приглушаем её, чтобы не путать со своими.
  const strip = $('ap-strip');
  strip.classList.toggle('idle', !g.you.myTurn);
  strip.title = g.you.myTurn
    ? `Почти любое действие стоит очко. Неистраченные в конце хода превращаются в припасы: +${app.meta.apCarry} за очко.`
    : `Сейчас ходит ${g.turnName}: это очки действия текущего хода.`;
  drawPips(g, 0);
}

// drawPips — очки действия кружками. spend — сколько из них уйдёт на
// действие под курсором: это видно до клика, а не после.
function drawPips(g, spend) {
  const box = $('ap-pips');
  box.innerHTML = '';
  for (let i = 0; i < g.apMax; i++) {
    const pip = document.createElement('i');
    const left = i < g.ap;
    pip.className = 'pip' + (left ? ' on' : '') + (left && i >= g.ap - spend ? ' spend' : '');
    box.appendChild(pip);
  }
}

function renderWallets(g) {
  const you = g.you;

  // Припасы: остаток, потолок обоза и доход по статьям. Без статей «+8 в
  // ход» — число с потолка: непонятно, что его кормит и что может отнять.
  // Потолок нужен по той же причине: без него исчезающий излишек выглядит
  // ошибкой сервера.
  const cap = you.supplyCap || app.meta.supplyCap;
  $('hud-cap').textContent = `из ${cap}`;
  $('hud-cap').classList.toggle('full', you.supplies >= cap);
  $('supplies-fill').style.width = `${Math.min(100, Math.round((you.supplies / cap) * 100))}%`;
  $('hud-income').textContent = `+${you.income || 0} в ход`;
  $('income-parts').textContent = (you.incomeParts || []).map((p) => `${p.label} +${p.amount}`).join(' · ');

  // Серебро платит только рудник, и только тому, кто стоит на клетке.
  $('hud-mine').textContent = `рудник ${you.mine}/${app.meta.mineTiles.length}`;
  $('hud-silver-income').textContent = `+${you.silverIncome || 0} в ход`;
  $('elite-hint').textContent = 'Серебро платит только рудник — тому, кто стоит на его клетке.';
  $('elite-hint').hidden = you.mine > 0;
  previewSpend(null);

  const wait = $('wallet-wait');
  wait.hidden = you.myTurn && g.ap > 0;
  // Без очков действия гаснет всё разом — одна строка вместо одинаковой
  // причины на каждой кнопке.
  wait.textContent = you.myTurn
    ? 'Очки действия кончились — остаётся завершить ход.'
    : 'Тратить можно в свой ход.';

  renderHire(g);
  renderCourt(g);
  renderSkills(g);
}

// previewSpend — что останется после действия под курсором: число в шапке
// кошелька и кружки очков. cost = null возвращает всё как есть.
function previewSpend(cost) {
  const g = app.snap && app.snap.game;
  if (!g) return;
  const show = (id, have, spend) => {
    const el = $(id);
    el.textContent = spend > 0 ? `${have} → ${have - spend}` : have;
    el.classList.toggle('preview', spend > 0);
  };
  show('hud-supplies', g.you.supplies, (cost && cost.supplies) || 0);
  show('hud-silver', g.you.silver, (cost && cost.silver) || 0);
  drawPips(g, (cost && cost.ap) || 0);
}

// renderHire — наём в обоих кошельках: обычные отряды за припасы, элита за
// серебро. Счёт наймов и предел армии у них общие, поэтому видны в обоих.
function renderHire(g) {
  const you = g.you;
  const items = you.myTurn ? g.buyable || [] : [];
  const groups = [
    ['grp-hire', 'shop-list', 'hire-limits', items.filter((i) => !i.elite)],
    ['grp-elite', 'elite-list', 'elite-limits', items.filter((i) => i.elite)],
  ];
  for (const [box, listId, limitsId, group] of groups) {
    $(box).hidden = group.length === 0;
    const list = $(listId);
    list.innerHTML = '';
    for (const item of group) list.appendChild(unitItem(item, g));
    const limits = $(limitsId);
    limits.textContent = hireLimits(you);
    limits.classList.toggle('full', you.hires === 0 || (you.armyMax > 0 && you.army >= you.armyMax));
  }
}

function hireLimits(you) {
  const hires = you.hires > 0
    ? `ещё ${you.hires} ${plural(you.hires, 'найм', 'найма', 'наймов')}`
    : 'наём на этот ход исчерпан';
  return you.armyMax ? `${hires} · армия ${you.army}/${you.armyMax}` : hires;
}

function unitItem(item, g) {
  const spec = app.meta.specs.find((s) => s.name === item.unit) || {};
  const ap = item.ap || 1;
  const cost = item.elite ? { silver: item.silver, ap } : { supplies: item.cost, ap };
  return walletItem({
    label: item.label,
    // Для отрядов с пределом (катапульты) показываем, сколько уже на поле.
    meta: item.max ? `на поле ${item.have || 0}/${item.max}` : '',
    sub: specStats(spec),
    more: specTraits(spec),
    cost,
    block: item.affordable ? null : blockFor(item.why, g, cost, {
      max: `больше ${item.max} одновременно не держат`,
    }),
    active: app.ui.mode === 'deploy' && app.ui.unit === item.unit,
    // Иконку рисуем той же функцией, что и фигурку на карте: в лавке и на
    // поле это должен быть один и тот же человек, иначе выбирать приходится
    // по названию, а узнавать — по памяти.
    icon: (canvas) => drawUnitIcon(canvas, item.unit, g.you.side || 'neutral', app.meta),
    pick: () => {
      app.ui.mode = app.ui.unit === item.unit && app.ui.mode === 'deploy' ? 'select' : 'deploy';
      app.ui.unit = app.ui.mode === 'deploy' ? item.unit : null;
      app.ui.selectedId = 0;
      renderHUD(app.snap);
      render();
    },
  });
}

// specStats — главные числа отряда: их видно в строке лавки всегда. Обзор
// пишем, только если он дальше соседней клетки: так видят почти все, и
// «обзор 1» на каждой строке — шум, за которым теряются разведчики.
function specStats(spec) {
  const parts = [];
  if (spec.maxHp) parts.push(`${spec.maxHp} HP`);
  if (spec.move) parts.push(`шаг ${spec.move}`);
  if (spec.damage) parts.push(`урон ${spec.damage}${spec.range > 1 ? ` / дальн. ${spec.range}` : ''}`);
  if (spec.vision > 1) parts.push(`обзор ${spec.vision}`);
  return parts.join(' · ');
}

// specTraits — особенности отряда. Их много, и они длинные, поэтому в лавке
// они видны у выбранного отряда и в подсказке, а не на каждой строке.
function specTraits(spec) {
  const parts = [];
  if (spec.charges) parts.push('сносит соседний сегмент одним зарядом, гибнет во взрыве');
  if (spec.name === 'trebuchet') parts.push('с места не сходит');
  if (spec.capacity) parts.push(`везёт ${spec.capacity} отряда на стену`);
  if (spec.campOnly) parts.push('собирают только в лагере');
  if (spec.levy) parts.push('вне предела армии');
  if (spec.bound) parts.push('не выходит за стены замка');
  if (spec.wheeled) parts.push('ход ромбом: прямо 2, вкось 1');
  if (spec.rolls) parts.push('на катках: через ров не пройдёт, нужны фашины');
  if (spec.name === 'ladder') parts.push('приставьте к стене — пешие полезут наверх');
  if (spec.name === 'scout') parts.push('глаза отряда');
  if (spec.name === 'engineer') parts.push('ловушки, частокол пряслом, ремонт кладки и машин, деревянные ворота');
  if (spec.name === 'sapper') parts.push('аванпост, вышка, частокол, мантелеты; фашины как все');
  if (spec.name === 'mantlet') parts.push('щит: прикрывает того, кто стоит прямо за ним');
  if (spec.machine) parts.push('машина: чинят инженеры, лекарь не лечит');
  if (spec.healer) parts.push(`лечит соседа на ${app.meta.heal}`);
  if (spec.splash) parts.push(`накрытие ${spec.splash * 2 + 1}×${spec.splash * 2 + 1}`);
  if (spec.noUnits) parts.push('бьёт только по укреплениям');
  // Пометка нужна только тем, от кого штурма и ждут: защитник по стене не
  // бьёт в принципе, и для гарнизона это не характеристика, а шум.
  if (!spec.defender && spec.vsWall === 0 && spec.damage && spec.range <= 1) parts.push('стены не ломает');
  return parts.join(' · ');
}

// renderCourt — стройка замка во дворе. Это и есть длинная стратегия
// защитника: во что вложить припасы, пока осаждающие тащат машины.
function renderCourt(g) {
  const you = g.you;
  const builds = you.myTurn && you.side === 'defender' ? you.courtBuilds || [] : [];
  $('grp-court').hidden = builds.length === 0;
  const list = $('court-list');
  list.innerHTML = '';
  if (!builds.length) return;
  const free = (you.court || []).length;
  const limits = $('court-limits');
  // Двор застроен — место освободится, только если постройку разобьют.
  limits.textContent = free ? `свободно клеток: ${free}` : 'двор застроен';
  limits.classList.toggle('full', free === 0);
  for (const item of builds) {
    const cost = { supplies: item.cost, ap: item.ap || app.meta.constructAp };
    list.appendChild(walletItem({
      label: item.label,
      meta: item.max ? `построено ${item.have || 0}/${item.max}` : '',
      sub: item.hint,
      cost,
      block: item.affordable ? null : blockFor(item.why, g, cost, { max: `больше ${item.max} не построить` }),
      icon: (canvas) => drawItemIcon(canvas, item.key),
      active: app.ui.mode === 'construct' && app.ui.court === item.key,
      pick: () => {
        const same = app.ui.mode === 'construct' && app.ui.court === item.key;
        app.ui.mode = same ? 'select' : 'construct';
        app.ui.court = same ? null : item.key;
        app.ui.selectedId = 0;
        renderHUD(app.snap);
        render();
      },
    }));
  }
}

// renderSkills — умения за припасы (у замка) и воодушевление (у всех
// троих). Решётка припасов не стоит — она уезжает в кошелёк очков действия.
function renderSkills(g) {
  const list = $('ability-list');
  const apList = $('ap-list');
  list.innerHTML = '';
  apList.innerHTML = '';
  if (g.you.myTurn) {
    if (g.you.side === 'defender') {
      for (const ab of app.meta.abilities) {
        const cost = abilityCost(ab, g);
        (cost.supplies > 0 ? list : apList).appendChild(abilityItem(ab, g, cost));
      }
    }
    if (app.meta.rally) list.appendChild(rallyItem(g));
  }
  $('grp-skills').hidden = list.children.length === 0;
  $('wallet-ap').hidden = apList.children.length === 0;
}

function abilityCost(ab, g) {
  // Отстройка пролома дешевеет от кузницы — цену берём из снимка, а не из
  // неизменной меты.
  const supplies = ab.key === 'rebuild' && g.you.rebuild ? g.you.rebuild : ab.cost;
  return { supplies, ap: ab.ap || 1 };
}

// Почему умение некуда применить: отстраивать нечего или ворот не осталось.
const NO_TARGET = {
  rebuild: 'проломов нет — отстраивать нечего',
  gate: 'ворот не осталось — все выбиты',
};

function abilityItem(ab, g, cost) {
  const you = g.you;
  const tiles = abilityTiles(ab.key);
  // Ополчение созывают по всему замку сразу — клетку для него выбирать не
  // надо, кнопка срабатывает на месте.
  const instant = Boolean(ab.instant);
  // Ремонт стены и смола упираются не только в припасы: за ход их
  // разрешено сделать считанное число раз, и остаток надо видеть на кнопке.
  const limits = { rebuild: [you.fixes, you.fixesMax], pitch: [you.pitches, you.pitchesMax] };
  const limit = limits[ab.key];
  let meta = '';
  if (limit) meta = limit[0] > 0 ? `ещё ${limit[0]} за ход` : 'на этот ход всё';
  else if (ab.key === 'militia') meta = 'раз в ход';

  let block = null;
  if (ab.key === 'militia') {
    // У ополчения условий пять, и какое не выполнено, говорит сервер.
    if (you.militiaWhy) {
      block = blockFor(you.militiaWhy, g, cost, {
        used: 'уже созывали в этом ходу',
        max: 'ополченцев на поле предел — больше не поднять',
        room: 'у короля негде встать',
      });
    }
  } else if (limit && limit[0] <= 0) {
    block = { text: `не больше ${limit[1]} ${plural(limit[1], 'раза', 'раз', 'раз')} за ход` };
  } else if (!tiles.length) {
    block = { text: NO_TARGET[ab.key] || 'применить некуда' };
  } else if (g.ap < cost.ap) {
    block = blockFor('ap', g, cost);
  } else if (you.supplies < cost.supplies) {
    block = blockFor('supplies', g, cost);
  }

  return walletItem({
    label: ab.label,
    meta,
    sub: ab.hint,
    cost,
    block,
    icon: (canvas) => drawItemIcon(canvas, ab.key),
    active: !instant && app.ui.mode === 'ability' && app.ui.ability === ab.key,
    pick: instant
      ? () => act({ kind: 'ability', ability: ab.key })
      : () => {
        app.ui.mode = app.ui.ability === ab.key && app.ui.mode === 'ability' ? 'select' : 'ability';
        app.ui.ability = app.ui.mode === 'ability' ? ab.key : null;
        app.ui.segments = tiles;
        app.ui.selectedId = 0;
        renderHUD(app.snap);
        render();
      },
  });
}

// rallyItem — воодушевление: решающий штурм за всё накопленное. Доступно
// всем троим, поэтому лежит в умениях у каждого. Пока действует, строка
// подсвечена, а повторный клик только напоминает об этом.
function rallyItem(g) {
  const sp = app.meta.rally;
  const you = g.you;
  const cost = { supplies: sp.cost, ap: sp.ap || 1 };
  let block = null;
  if (you.rally) block = { text: 'уже действует до конца хода', quiet: true };
  else if (!you.rallyOk) {
    block = g.ap < cost.ap ? blockFor('ap', g, cost)
      : you.supplies < cost.supplies ? blockFor('supplies', g, cost)
        : { text: 'сейчас недоступно' };
  }
  return walletItem({
    label: sp.label,
    meta: you.rally ? 'действует до конца хода' : '',
    sub: sp.hint,
    cost,
    block,
    icon: (canvas) => drawItemIcon(canvas, 'rally'),
    lit: you.rally,
    pick: () => act({ kind: 'ability', ability: 'rally' }),
  });
}

// blockFor — причина недоступности по коду сервера (Why* в view.go).
// Валюту можно накопить — для неё полоска и прикидка, через сколько ходов;
// остальное ожиданием дохода не лечится, там только слова. quiet — причина
// общая для всей группы и уже написана в её заголовке: повторять её на
// каждой строке — шум.
function blockFor(code, g, cost, texts = {}) {
  const you = g.you;
  switch (code) {
    case 'supplies': return shortage(you.supplies, cost.supplies, you.income, 'supplies');
    case 'silver': return shortage(you.silver, cost.silver, you.silverIncome, 'silver');
    case 'ap': return { text: `нужно ${cost.ap} ${RES_WORDS.ap(cost.ap)}, осталось ${g.ap}`, quiet: g.ap === 0 };
    case 'hires': return { text: 'наём на этот ход исчерпан', quiet: true };
    case 'army': return { text: `армия на пределе: ${you.army}/${you.armyMax}`, quiet: true };
    case 'court': return { text: 'во дворе нет свободной клетки', quiet: true };
    case 'used': return { text: texts.used || 'уже применяли в этом ходу' };
    case 'room': return { text: texts.room || 'негде поставить' };
    case 'max': return { text: texts.max || 'предел уже достигнут' };
    default: return { text: 'сейчас недоступно' };
  }
}

// shortage — сколько не хватает и когда накопится при нынешнем доходе.
// Прикидка грубая: остаток очков в конце хода тоже капает в припасы.
function shortage(have, need, income, res) {
  const lack = need - have;
  let text = `ещё ${lack} ${RES_WORDS[res](lack)}`;
  if (income > 0) {
    const turns = Math.ceil(lack / income);
    text += ` · ≈${turns} ${plural(turns, 'ход', 'хода', 'ходов')}`;
  } else if (res === 'silver') {
    text += ' · займите рудник';
  }
  return { text, bar: { res, have, need } };
}

// walletItem — одна строка кошелька: что, почём и, если сейчас нельзя,
// почему. Недоступную строку не выключаем: клик по ней объясняет причину —
// на телефоне подсказку при наведении не увидеть.
function walletItem(o) {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'shop-item' + (o.icon ? ' has-icon' : '') + (o.active ? ' active' : '')
    + (o.lit ? ' lit' : '') + (o.block ? ' blocked' : '') + (o.danger ? ' danger' : '');
  if (o.block) btn.setAttribute('aria-disabled', 'true');
  // Первая строка — значок, название и цена; всё остальное идёт под ней во
  // всю ширину: боковая колонка узкая, и рядом с ценой цифрам тесно.
  btn.innerHTML = (o.icon ? '<canvas class="unit-icon" width="26" height="26"></canvas>' : '')
    + `<span class="item-name">${escapeHTML(o.label)}`
    + (o.meta ? ` <small class="item-meta">${escapeHTML(o.meta)}</small>` : '')
    + '</span>'
    + `<span class="price">${priceChips(o.cost)}</span>`
    + (o.sub ? `<small class="item-sub">${escapeHTML(o.sub)}</small>` : '')
    + (o.more && o.active ? `<small class="item-sub">${escapeHTML(o.more)}</small>` : '')
    + (o.block && !o.block.quiet ? blockLine(o.block) : '');
  if (o.icon) o.icon(btn.querySelector('canvas'));
  btn.title = [o.sub, o.more].filter(Boolean).join(' · ')
    + (o.block ? `\nСейчас нельзя: ${o.block.text}` : '');
  btn.onclick = () => {
    if (o.block) {
      toast(`${o.label}: ${o.block.text}`);
      return;
    }
    o.pick();
  };
  if (!o.block) {
    btn.onmouseenter = btn.onfocus = () => previewSpend(o.cost);
    btn.onmouseleave = btn.onblur = () => previewSpend(null);
  }
  return btn;
}

function blockLine(b) {
  if (!b.bar) return `<small class="item-why">${escapeHTML(b.text)}</small>`;
  const pct = Math.max(0, Math.min(100, Math.round((b.bar.have / b.bar.need) * 100)));
  return `<span class="item-need"><span class="need-bar ${b.bar.res}"><i style="width:${pct}%"></i></span>`
    + `<small>${escapeHTML(b.text)}</small></span>`;
}

// priceChips — цена во всех валютах, каждая со своим значком: «20» без
// подписи не говорит, припасы это, серебро или очки действия.
function priceChips(cost) {
  let html = '';
  for (const res of ['supplies', 'silver', 'ap']) {
    const n = cost[res] || 0;
    if (n <= 0) continue;
    html += `<span class="chip ${res}">${n}<i class="ico ${res}" aria-hidden="true"></i>`
      + `<span class="sr-only"> ${RES_WORDS[res](n)}</span></span>`;
  }
  // Пустая цена бывает: пехота кидает фашины даром. Пусть это будет видно,
  // а не выглядит забытым ценником.
  return html || '<span class="chip free">даром</span>';
}

// abilityTiles — куда целится выбранное умение замка. Смола льётся вниз,
// под стену (в воротах — прямо в проём), решётка работает только по самим
// воротам, остальные умения — по любой клетке кладки.
function abilityTiles(key) {
  if (key === 'pitch') return app.meta.pitchTiles || [];
  const wall = (app.snap && app.snap.game && app.snap.game.wall) || [];
  if (key === 'gate') {
    // Деревянные ворота инженеров — такие же створки с решёткой, поэтому
    // список берём из снимка, а не из неизменной геометрии карты.
    return wall
      .filter((seg) => (seg.kind === 'gate' || seg.kind === 'woodgate') && seg.hp > 0)
      .map((seg) => seg.pos);
  }
  if (key === 'rebuild') return wall.filter((seg) => seg.hp <= 0).map((seg) => seg.pos);
  return wall.map((seg) => seg.pos);
}

// --- карточка: выбранный отряд или осмотренная клетка ---
//
// Верхняя карточка боковой панели одна на всё: свой выбранный отряд — с
// действиями, любая другая клетка — со сведениями о ней. Показываем только
// то, что прислал сервер: чего нет в снимке (туман, чужая засада), того нет
// и в карточке, поэтому подсмотреть через неё лишнее нельзя.

// На телефоне карточка выбранного отряда лежит ниже поля, за сгибом: без
// этого после тычка в отряд надо вручную листать вниз, а потом обратно к
// подсвеченным клеткам. Подтягиваем её минимально — 'nearest' оставляет
// поле на экране, — и только когда выбор правда сменился, иначе каждый
// снимок с сервера дёргал бы страницу.
let shownCard = '';

function revealCard(box) {
  if (!window.matchMedia('(max-width: 1150px)').matches) return;
  const id = box.hidden ? '' : `${app.ui.selectedId}:${app.ui.inspect ? `${app.ui.inspect.x},${app.ui.inspect.y}` : ''}`;
  if (id === shownCard) return;
  shownCard = id;
  if (id) box.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
}

function renderCard(g) {
  const box = $('card');
  const unit = (g.units || []).find((x) => x.id === app.ui.selectedId && x.mine);
  box.innerHTML = '';
  if (app.ui.inspect) renderCellCard(box, g, app.ui.inspect, unit);
  else if (unit) renderUnitCard(box, g, unit);
  box.hidden = !box.firstChild;
  revealCard(box);
}

function deselect() {
  app.ui.selectedId = 0;
  app.ui.mode = 'select';
  syncUI(app.snap);
  renderHUD(app.snap);
  render();
}

// inspectCell — показать сведения о клетке в карточке и под полем. Выбор
// отряда при этом не трогаем: правой кнопкой можно осмотреть цель, не
// теряя подсветки его ходов.
function inspectCell(cell) {
  app.ui.inspect = cell;
  renderHUD(app.snap);
  render();
}

function closeInspect() {
  app.ui.inspect = null;
  renderHUD(app.snap);
  render();
}

function sideName(side) {
  return SIDE_COLORS[side] ? SIDE_COLORS[side].name : '';
}

// isEnemy — бьют ли друг друга эти стороны. Осаждающие друг друга не
// атакуют (как и areEnemies на сервере): гонка идёт за пролом, а не за
// головы союзника.
function isEnemy(a, b) {
  if (!a || !b || a === b || a === 'neutral' || b === 'neutral') return false;
  return a === 'defender' || b === 'defender';
}

function specByName(name) {
  return app.meta.specs.find((s) => s.name === name) || {};
}

// cardHead — шапка карточки: значок, название с подписью, прочность и крестик.
function cardHead({ icon, title, sub, hp, closeLabel, onClose }) {
  const head = document.createElement('div');
  head.className = 'card-head';
  head.innerHTML = (icon ? '<canvas class="unit-icon big" width="30" height="30"></canvas>' : '')
    + `<span class="card-title"><b>${escapeHTML(title)}</b>${sub ? `<small>${escapeHTML(sub)}</small>` : ''}</span>`
    + (hp ? hpBar(hp.hp, hp.max) : '')
    + `<button type="button" class="card-close" aria-label="${escapeHTML(closeLabel)}" title="${escapeHTML(closeLabel)}">×</button>`;
  if (icon) icon(head.querySelector('canvas'));
  head.querySelector('.card-close').onclick = onClose;
  return head;
}

// hpBar — прочность полоской: зелёная, на половине жёлтая, на трети красная.
function hpBar(hp, max) {
  const pct = max > 0 ? Math.max(0, Math.min(100, Math.round((hp / max) * 100))) : 0;
  const tone = pct <= 34 ? ' low' : pct <= 50 ? ' mid' : '';
  return `<span class="hp"><span class="hp-bar${tone}"><i style="width:${pct}%"></i></span><small>${hp}/${max}</small></span>`;
}

function cardLine(box, cls, text) {
  if (!text) return;
  const p = document.createElement('p');
  p.className = cls;
  p.textContent = text;
  box.appendChild(p);
}

// cardSection — подзаголовок и строки: так в карточке клетки разделены
// отряд, постройка, сама клетка и обстановка вокруг.
function cardSection(box, title, lines) {
  const list = lines.filter(Boolean);
  if (!list.length) return;
  const sec = document.createElement('div');
  sec.className = 'card-sec';
  sec.innerHTML = `<h4>${escapeHTML(title)}</h4>` + list.map((l) => `<p>${escapeHTML(l)}</p>`).join('');
  box.appendChild(sec);
}

// unitLine — числа отряда с поправкой на звёзды и вышку: то, с чем он выйдет
// в бой прямо сейчас, а не строчка из справочника.
function unitLine(u, spec) {
  const parts = [];
  if (spec.move) parts.push(`шаг ${spec.move}`);
  if (spec.damage) {
    const dmg = spec.damage + (u.stars || 0) * (app.meta.veteranDamage || 0);
    const reach = u.reach || spec.range;
    parts.push(`урон ${dmg}${reach > 1 ? ` / дальн. ${reach}` : ''}`);
  }
  if (spec.vision > 1) parts.push(`обзор ${spec.vision}`);
  return [parts.join(' · '), specTraits(spec)].filter(Boolean).join(' · ');
}

// unitTags — состояние отряда метками: может ли он ещё идти и бить, в засаде
// ли, ветеран ли. Раньше это читалось только из строки внизу панели.
function unitTags(g, u) {
  const tags = [];
  if (g.you.myTurn && u.mine) {
    if ((u.moves || []).length) tags.push(['может идти', 'go']);
    else if (u.moved) tags.push(['уже ходил', 'spent']);
    if ((u.targets || []).length) tags.push(['может бить', 'hit']);
    else if (u.acted) tags.push(['уже действовал', 'spent']);
  }
  if (u.lurking) tags.push([`в засаде: первый удар ×${app.meta.ambush}`, 'ambush']);
  if (u.stars) {
    tags.push([`ветеран: +${u.stars * app.meta.veteranDamage} урона, +${u.stars * app.meta.veteranHp} HP`, 'vet']);
  }
  if (!tags.length) return null;
  const row = document.createElement('div');
  row.className = 'tags';
  row.innerHTML = tags.map(([text, kind]) => `<span class="tag ${kind}">${escapeHTML(text)}</span>`).join('');
  return row;
}

function renderUnitCard(box, g, u) {
  const spec = specByName(u.type);
  box.appendChild(cardHead({
    icon: (canvasEl) => drawUnitIcon(canvasEl, u.type, u.side, app.meta),
    // Звёзды ветерана — часть имени отряда: по ним и выбирают, кем добивать.
    title: u.label + (u.stars ? ` ${'★'.repeat(u.stars)}` : ''),
    sub: sideName(u.side),
    hp: { hp: u.hp, max: u.maxHp },
    closeLabel: 'Снять выбор',
    onClose: deselect,
  }));
  cardLine(box, 'card-stats', unitLine(u, spec));
  const tags = unitTags(g, u);
  if (tags) box.appendChild(tags);

  const { build, acts } = unitActions(g, u);
  const hasAction = [...build, ...acts].some((el) => !el.classList.contains('blocked'));
  cardLine(box, 'card-legend', modeHint(u, hasAction));
  if (g.ap === 0) cardLine(box, 'card-legend warn', 'Очки действия кончились — остаётся завершить ход.');
  for (const [title, items] of [['Строить', build], ['Действия', acts]]) {
    if (!items.length) continue;
    const grp = document.createElement('div');
    grp.className = 'group';
    grp.innerHTML = `<h4>${title}</h4>`;
    const list = document.createElement('div');
    list.className = 'shop-list';
    for (const el of items) list.appendChild(el);
    grp.appendChild(list);
    box.appendChild(grp);
  }
  if (u.capacity) box.appendChild(cargoList(g, u));
}

// modeHint — что значит подсветка на поле прямо сейчас: в каждом режиме
// клетки красятся по-своему.
function modeHint(u, hasAction) {
  const mode = app.ui.mode;
  if (mode === 'unload') return 'Светлые клетки — куда сходит выбранный отряд: на стену по трапу или на землю рядом с башней.';
  if (mode === 'charge') return 'Красные клетки — под какой сегмент заложить заряд. Подрывник погибнет вместе с ним.';
  if (mode === 'strike') return 'Выберите вышку: следующий клик по ней — удар, а не вход.';
  if (mode === 'heal') return 'Зелёные клетки — кого перевязать. Одно лечение за ход.';
  if (mode === 'build' && app.ui.build === 'fascine') return 'Жёлтые клетки — какую клетку рва засыпать. По клетке за действие.';
  if (mode === 'build' && app.ui.build === 'mantlet') return 'Жёлтые клетки — куда выкатить щит. Прикрывает того, кто встанет прямо за ним.';
  if (mode === 'build' && app.ui.build === 'barricade') return 'Жёлтые клетки — куда ставить частокол. Под курсором видно всё прясло: оно встаёт поперёк направления, за одно действие и одну цену.';
  if (mode === 'build') return 'Жёлтые клетки — куда ставить или что чинить.';
  if (mode === 'bombard') return `Выберите клетку в пределах пунктира: накрытие ${u.area * 2 + 1}×${u.area * 2 + 1}, можно вслепую.`;
  if (u.area) return 'Светлые клетки — куда идти. Для обстрела — «Навесной удар».';
  if ((u.moves || []).length || (u.targets || []).length) return 'Светлые клетки — шаг, красные — удар: по очку действия.';
  // Отряд мог уже сходить, но действие при этом остаться: у подрывника после
  // марша к стене как раз тот случай, и «отработал» тут врёт.
  return hasAction ? 'Ходить он уже не может, но действие ещё доступно.' : 'В этом ходу отряд уже отработал.';
}

// Почему действие некуда применить, если сервер не прислал ни одной клетки.
// Ловушки, частокол и вышки ставят только в поле — со стены и из двора не
// выйдет, и это надо сказать прямо.
const NO_SPOT = {
  trap: 'ставят в поле — рядом нет свободной клетки',
  barricade: 'ставят в поле — рядом нет свободной клетки',
  palisade: 'ставят в поле — рядом нет свободной клетки',
  repair: 'рядом нечего чинить',
  woodgate: 'рядом нет пролома',
  mantlet: 'рядом негде выкатить щит',
  watchpost: 'ставят в поле — рядом нет свободной клетки',
  warehouse: 'склад ставят в своём лагере',
  outpost: 'здесь аванпост не поставить',
  fascine: 'рядом нет открытого рва',
  charge: 'рядом нет сегмента стены',
  heal: 'рядом нет раненых своих',
};

// splitHint — первая фраза подсказки идёт в строку, остальное — в подсказку
// при наведении и в строку выбранного действия.
function splitHint(hint) {
  const text = hint || '';
  const i = text.indexOf('. ');
  return i < 0 ? [text, ''] : [text.slice(0, i), text.slice(i + 2)];
}

// unitActions — что умеет выбранный отряд, строками кошелька: цена со
// значками и, если сейчас нельзя, причина. Кнопки показываем всем, кто в
// принципе это умеет, — так о них можно узнать заранее.
function unitActions(g, u) {
  const build = [];
  const acts = [];
  const spent = { text: 'отряд уже действовал в этом ходу' };
  // blockOf — причина в том же порядке, что в кошельках: сначала «некуда»
  // (или «уже действовал»), потом очки действия, потом припасы.
  const blockOf = (spots, key, cost, extra) => {
    if (extra) return extra;
    if (spots && !spots.length) return u.acted ? spent : { text: NO_SPOT[key] || 'сейчас некуда' };
    if (g.ap < (cost.ap || 0)) return blockFor('ap', g, cost);
    if ((cost.supplies || 0) > g.you.supplies) return blockFor('supplies', g, cost);
    return null;
  };
  const isBuild = (key) => app.ui.mode === 'build' && app.ui.build === key;
  const pickBuild = (key, spots) => () => {
    const same = isBuild(key);
    app.ui.mode = same ? 'select' : 'build';
    app.ui.build = same ? null : key;
    app.ui.buildSpots = same ? [] : spots;
    renderHUD(app.snap);
    render();
  };
  const pickMode = (mode, apply) => () => {
    const same = app.ui.mode === mode;
    app.ui.mode = same ? 'select' : mode;
    if (apply) apply(same);
    renderHUD(app.snap);
    render();
  };
  // Значок строки: мантелет — отряд, у него фишка отряда, остальное — плитка
  // постройки или действия.
  const iconOf = (key) => (key === 'mantlet'
    ? (canvas) => drawUnitIcon(canvas, 'mantlet', g.you.side, app.meta)
    : (canvas) => drawItemIcon(canvas, key));
  const addBuild = (key, label, hint, cost, spots, extra, meta) => {
    const [sub, more] = splitHint(hint);
    build.push(walletItem({
      label, meta, sub, more, cost,
      block: blockOf(spots, key, cost, extra),
      icon: iconOf(key),
      active: isBuild(key),
      pick: pickBuild(key, spots),
    }));
  };

  // Строить. Инженеры у сторон разные: замок ставит ловушки, вяжет частокол
  // пряслом и чинит, осада ставит частокол по колу и катит щиты.
  if (u.builder) {
    for (const b of buildMenu(g.you.side)) {
      let extra = null;
      if (b.key === 'mantlet') {
        const max = specByName('mantlet').maxAlive || 0;
        const have = (g.units || []).filter((x) => x.mine && x.type === 'mantlet').length;
        if (max && have >= max) extra = { text: `больше ${max} щитов не держат` };
      }
      addBuild(b.key, b.label, b.hint, { supplies: b.cost, ap: b.ap || 1 }, buildSpotsFor(u, b.key), extra);
    }
  }
  // Наблюдательный пункт: цена у сторон разная, поэтому берём её из снимка,
  // а не из неизменной меты.
  if (u.canWatch) {
    const w = app.meta.watchPost;
    const full = g.you.watchHad >= g.you.watchMax;
    addBuild('watchpost', w.label, w.hint, { supplies: g.you.watch, ap: app.meta.watchPostAp || 1 }, u.watchAt || [],
      full ? { text: `больше ${g.you.watchMax} вышек на сторону не держат` } : null,
      `построено ${g.you.watchHad}/${g.you.watchMax}`);
  }
  // Склад в лагере: без него на воодушевление и аванпост не накопить.
  if (u.canStore) {
    const w = app.meta.store;
    addBuild('warehouse', w.label, w.hint, { supplies: w.cost, ap: w.ap || 1 }, u.storeAt || []);
  }
  // Аванпост: временный лагерь осаждающих, из которого можно нанимать.
  if (u.canOutpost) {
    const o = app.meta.outpost;
    addBuild('outpost', o.label, o.hint, { supplies: o.cost, ap: app.meta.outpostAp || 1 }, u.outpostAt || []);
  }
  // Фашины: без них таран и осадная башня к стене не подъедут вовсе.
  if (u.canFascine) {
    const f = app.meta.fascine;
    addBuild('fascine', f.label, f.hint, { ap: u.fascineAp || 0 }, u.fascineAt || []);
  }

  // Действия — то, что отряд делает сам, а не ставит рядом.
  const one = { ap: 1 };
  if (u.canClimb) {
    acts.push(walletItem({
      label: 'Лезть на стену', sub: 'по приставленной лестнице — на боевой ход',
      cost: one, block: blockOf(null, '', one), icon: iconOf('climb'),
      pick: () => act({ kind: 'climb', unitId: u.id }),
    }));
  }
  // Подрывник: сегмент выбирает игрок. Вплотную к нему их может оказаться
  // несколько, и какой ронять — решение, а не геометрия.
  if (u.charges > 0) {
    const spots = u.chargeAt || [];
    acts.push(walletItem({
      label: 'Заложить заряд', meta: `зарядов ${u.charges}`,
      sub: 'соседний сегмент складывается сразу — вместе с подрывником',
      cost: one, block: blockOf(spots, 'charge', one), danger: true, icon: iconOf('charge'),
      active: app.ui.mode === 'charge',
      pick: pickMode('charge', (same) => { app.ui.chargeSpots = same ? [] : spots; }),
    }));
  }
  // Навесной стрелок: клик по ближней клетке двигает машину, поэтому для
  // удара себе под нос нужен отдельный режим прицеливания.
  if (u.area) {
    const side = u.area * 2 + 1;
    acts.push(walletItem({
      label: 'Навесной удар', sub: `камни по клетке в пределах пунктира, накрытие ${side}×${side}, можно вслепую`,
      cost: one,
      block: u.acted ? spent : !u.reach ? { text: 'отсюда не достать' } : blockOf(null, '', one),
      icon: iconOf('bombard'),
      active: app.ui.mode === 'bombard',
      pick: pickMode('bombard'),
    }));
  }
  // Если рядом пустая вышка, клик по ней уводит внутрь. Чтобы её всё-таки
  // снести, нужен явный режим — иначе из рукопашной вышку не разбить вовсе.
  if ((u.targets || []).some((p) => isEmptyTower(p))) {
    acts.push(walletItem({
      label: 'Разбить вышку', sub: 'по умолчанию клик по пустой вышке заводит отряд внутрь',
      cost: one, block: blockOf(null, '', one), icon: iconOf('strike'),
      active: app.ui.mode === 'strike',
      pick: pickMode('strike'),
    }));
  }
  if (u.healer) {
    const spots = u.healAt || [];
    acts.push(walletItem({
      label: 'Лечить', meta: `+${app.meta.heal}`, sub: 'перевязать соседний свой отряд; машины не лечит',
      cost: one, block: blockOf(spots, 'heal', one), icon: iconOf('heal'),
      active: app.ui.mode === 'heal',
      pick: pickMode('heal', (same) => { app.ui.healSpots = same ? [] : spots; }),
    }));
  }
  // Осадная башня: трап опускают вплотную к стене, и это отдельное действие —
  // после него по трапу сходят на боевой ход.
  if (u.capacity) {
    acts.push(walletItem({
      label: u.ramp ? 'Трап опущен' : 'Открыть трап',
      sub: 'вплотную к стене; падая, трап давит того, кто стоял на этой клетке стены, — кроме короля',
      cost: one,
      block: u.ramp ? { text: 'трап уже опущен — высаживайте на стену', quiet: true }
        : !u.canRamp ? (u.acted ? spent : { text: 'подведите башню вплотную к стене' })
          : blockOf(null, '', one),
      icon: iconOf('ramp'),
      lit: u.ramp,
      pick: () => act({ kind: 'ramp', unitId: u.id }),
    }));
  }
  // Посадка: отряд рядом со своей башней забирается внутрь.
  if ((u.boardAt || []).length) {
    acts.push(walletItem({
      label: 'Сесть в башню', sub: 'внутри отряд не видно и не обстрелять — но он разделит судьбу башни',
      cost: one, block: blockOf(null, '', one), icon: iconOf('board'),
      pick: () => act({ kind: 'board', unitId: u.id, to: u.boardAt[0] }),
    }));
  }
  return { build, acts };
}

// cargoList — экипаж осадной башни: кто внутри и кого высаживать. Список
// приходит только хозяину — противник не знает, пуста башня или полна.
function cargoList(g, u) {
  const box = document.createElement('div');
  box.className = 'cargo';
  const cargo = u.cargo || [];
  const head = document.createElement('p');
  head.className = 'card-legend';
  head.textContent = `Внутри: ${cargo.length} из ${u.capacity}`
    + (u.ramp ? '. Трап опущен — высаживайте на стену.' : '. Трап закрыт: вылезти можно только на землю рядом.');
  box.appendChild(head);
  for (const c of cargo) {
    const row = document.createElement('button');
    row.className = 'cargo-row' + (app.ui.mode === 'unload' && app.ui.unloadId === c.id ? ' active' : '');
    // Два выхода: по трапу на стену и через люк на землю. Клетки обоих
    // подсвечиваем одним списком — какая сработает, решает сама клетка.
    const spots = [...(u.unloadAt || []), ...(u.exitAt || [])];
    row.disabled = !spots.length || c.spent || g.ap < 1;
    row.innerHTML = `<canvas class="unit-icon" width="22" height="22"></canvas>`
      + `<span>${escapeHTML(c.label)}<small>${c.hp}/${c.maxHp} HP${c.spent ? ' · уже ходил' : ''}</small></span>`;
    const icon = row.querySelector('canvas');
    if (icon) drawUnitIcon(icon, c.type, u.side, app.meta);
    row.onclick = () => {
      const same = app.ui.mode === 'unload' && app.ui.unloadId === c.id;
      app.ui.mode = same ? 'select' : 'unload';
      app.ui.unloadId = same ? 0 : c.id;
      app.ui.unloadSpots = same ? [] : spots;
      renderHUD(app.snap);
      render();
    };
    box.appendChild(row);
  }
  return box;
}

// cellInfo — всё, что известно о клетке из снимка: местность, кто и что на
// ней стоит, видна ли она сейчас.
function cellInfo(g, cell) {
  const meta = app.meta;
  const terrain = meta.terrain || {};
  const at = (p) => Boolean(p) && p.x === cell.x && p.y === cell.y;
  const ch = meta.tiles[cell.y][cell.x];
  const seg = (g.wall || []).find((s) => s.pos && s.pos.x === cell.x && s.pos.y === cell.y);
  // Сегмент стены меняет вид по ходу партии: пролом и заколоченный пролом
  // описаны отдельно от того, что стояло здесь в начале осады.
  let key = ch;
  if (seg && seg.hp <= 0) key = 'breach';
  else if (seg && seg.kind === 'woodgate') key = 'woodgate';
  return {
    ch,
    seg,
    terrain: terrain[key] || terrain[ch] || { label: 'Клетка', short: '', hint: '' },
    visible: !g.fog || (g.fog[cell.y] || '')[cell.x] === '1',
    unit: (g.units || []).find((u) => at(u.pos)),
    building: (g.buildings || []).find((b) => at(b.pos)),
    trap: (g.traps || []).some(at),
    spot: (g.mine || []).find((m) => at(m.pos)) || (g.farm || []).find((f) => at(f.pos)),
    moatOpen: ch === 'D' && (g.moat || []).some(at),
  };
}

// placeLabel — название клетки; у лагеря — ещё и чей он.
function placeLabel(c) {
  if (c.ch === 'a') return `${c.terrain.label} · ${sideName('attackerA')}`;
  if (c.ch === 'b') return `${c.terrain.label} · ${sideName('attackerB')}`;
  return c.terrain.label;
}

// placeShort — правило клетки в пару слов. Засыпанный ров уже не преграда,
// и писать про брод там было бы неправдой.
function placeShort(c) {
  if (c.ch === 'D' && !c.moatOpen) return 'засыпан: проходят все';
  return c.terrain.short;
}

// threatsTo — кто из видимых врагов достаёт до клетки. Тот же расчёт, что у
// накладки «Зоны обстрела»: чего не видно, того и нет.
function threatsTo(g, cell) {
  const count = new Map();
  for (const u of g.units || []) {
    if (u.mine || !u.reach || !isEnemy(g.you.side, u.side)) continue;
    const d = Math.max(Math.abs(u.pos.x - cell.x), Math.abs(u.pos.y - cell.y));
    if (d < 1 || d > u.reach) continue;
    count.set(u.label, (count.get(u.label) || 0) + 1);
  }
  return [...count].map(([label, n]) => (n > 1 ? `${label} ×${n}` : label));
}

// selRelation — что выбранный отряд может сделать с этой клеткой. Шаги и
// цели присылает сервер, поэтому подсказка совпадает с подсветкой на поле.
// Подлежащее — «отряд»: названия бывают и «Пехота», и «Инженеры», и глагол
// с ними иначе не согласовать.
function selRelation(sel, cell) {
  const at = (p) => p.x === cell.x && p.y === cell.y;
  if (at(sel.pos)) return '';
  const who = `Ваш отряд «${sel.label}»`;
  if ((sel.targets || []).some(at)) return `${who} может сюда ударить.`;
  if (sel.area && !sel.acted) {
    const d = Math.max(Math.abs(sel.pos.x - cell.x), Math.abs(sel.pos.y - cell.y));
    if (d >= 1 && d <= sel.reach) return `${who} может накрыть эту клетку навесом.`;
  }
  if ((sel.moves || []).some(at)) return `${who} может сюда дойти.`;
  return `${who} отсюда не дойдёт и не достанет.`;
}

function renderCellCard(box, g, cell, sel) {
  const c = cellInfo(g, cell);
  const place = placeLabel(c);
  const u = c.unit;
  const b = c.building;
  // Шапка — главное на клетке: отряд, постройка, сегмент стены или сама
  // местность.
  let head;
  if (u) {
    head = {
      icon: (canvasEl) => drawUnitIcon(canvasEl, u.type, u.side, app.meta),
      title: u.label + (u.stars ? ` ${'★'.repeat(u.stars)}` : ''),
      sub: `${sideName(u.side)} · ${place}`,
      hp: { hp: u.hp, max: u.maxHp },
    };
  } else if (b) {
    head = { title: b.label, sub: [b.side !== 'neutral' ? sideName(b.side) : '', place].filter(Boolean).join(' · '), hp: { hp: b.hp, max: b.maxHp } };
  } else if (c.seg && c.seg.hp > 0) {
    head = { title: c.terrain.label, sub: 'стена замка', hp: { hp: c.seg.hp, max: c.seg.maxHp } };
  } else {
    head = { title: place, sub: c.visible ? '' : 'в тумане' };
  }
  box.appendChild(cardHead({ ...head, closeLabel: 'Закрыть', onClose: closeInspect }));

  if (u) {
    cardSection(box, 'Отряд', [unitLine(u, specByName(u.type))]);
    const tags = unitTags(g, u);
    if (tags) box.appendChild(tags);
    if (u.mine && u.capacity) cardLine(box, 'card-legend', `Внутри: ${(u.cargo || []).length} из ${u.capacity}`);
  }
  if (b) {
    const s = (app.meta.structures || {})[b.kind] || {};
    const lines = [u ? `${b.label}: прочность ${b.hp}/${b.maxHp}.` : '', s.hint];
    if (b.life) lines.push(`Свернётся через ${b.life} ${plural(b.life, 'день', 'дня', 'дней')}.`);
    if (b.kind === 'tower') lines.push(b.hold && b.hold !== 'neutral' ? `Держит: ${sideName(b.hold)}.` : 'Сейчас ничья.');
    cardSection(box, u ? 'Постройка' : 'Что даёт', lines);
  }

  // Клетка: правило местности, а у рва, ворот, рудника и пашни — ещё и их
  // нынешнее состояние.
  const lines = [c.terrain.hint];
  if (c.ch === 'D' && !c.moatOpen) lines.push('Сейчас засыпан: по настилу проходят все.');
  if (c.seg && c.seg.hp > 0 && (c.seg.kind === 'gate' || c.seg.kind === 'woodgate')) {
    lines.push(c.seg.open ? 'Решётка поднята — проход открыт для всех.' : 'Решётка опущена.');
  }
  if (c.spot) lines.push(c.spot.side && c.spot.side !== 'neutral' ? `Занят: ${sideName(c.spot.side)}.` : 'Свободен.');
  if (c.trap) {
    const trap = (app.meta.builds || []).find((x) => x.key === 'trap');
    const hint = trap ? trap.hint : '';
    lines.push(`Ваша ловушка${hint ? `: ${hint[0].toLowerCase()}${hint.slice(1)}` : ''}.`);
  }
  cardSection(box, u || b || (c.seg && c.seg.hp > 0) ? `Клетка · ${place}` : 'Как работает', lines);

  // Обстановка: видно ли клетку, кто по ней достаёт и что здесь может
  // выбранный отряд.
  const threats = threatsTo(g, cell);
  cardSection(box, 'Обстановка', [
    c.visible ? '' : 'Клетка в тумане: кто на ней сейчас, не видно.',
    threats.length ? `Сюда достают: ${threats.join(', ')}.` : c.visible ? 'Из видимых врагов сюда никто не достаёт.' : '',
    sel ? selRelation(sel, cell) : '',
  ]);

  // Осмотр правой кнопкой не снимает выбор — вернуться к отряду можно в
  // один щелчок.
  if (sel) {
    const back = document.createElement('button');
    back.type = 'button';
    back.className = 'card-back';
    back.textContent = `← К отряду: ${sel.label}`;
    back.onclick = closeInspect;
    box.appendChild(back);
  }
}

// --- строка под полем ---

const TOUCH = window.matchMedia('(hover: none)').matches;

// renderStrip — строка под полем: клетка под курсором, а если курсора нет —
// осмотренная. Пара слов вместо карточки: взгляд остаётся на поле.
function renderStrip() {
  const el = $('cell-strip');
  const g = app.snap && app.snap.game;
  const cell = app.ui.hover || app.ui.inspect;
  el.classList.toggle('idle', !g || !cell);
  if (!g || !cell) {
    el.textContent = TOUCH
      ? 'Нажмите на клетку — тут будет, что на ней. Долгое нажатие — подробно.'
      : 'Наведите на клетку — тут будет, что на ней. Щелчок или правая кнопка — подробно.';
    return;
  }
  const c = cellInfo(g, cell);
  const parts = [placeLabel(c), placeShort(c)];
  if (c.unit) parts.push(`${c.unit.label}${c.unit.mine ? '' : ` (${sideName(c.unit.side)})`} ${c.unit.hp}/${c.unit.maxHp}`);
  else if (c.building) parts.push(`${c.building.label} ${c.building.hp}/${c.building.maxHp}`);
  else if (c.seg && c.seg.hp > 0) parts.push(`прочность ${c.seg.hp}/${c.seg.maxHp}`);
  if (!c.visible) parts.push('в тумане');
  const threats = threatsTo(g, cell);
  if (threats.length) parts.push(`достают: ${threats.join(', ')}`);
  el.textContent = parts.filter(Boolean).join(' · ');
}

// renderVoice рисует панель звонка: кто на связи и кто сейчас говорит.
function renderVoice(snap) {
  const box = $('voice');
  const hotseat = snap.mode === 'hotseat';
  // За одним экраном созваниваться не с кем.
  box.hidden = hotseat;
  if (hotseat || !app.voice) return;

  const st = app.voice.state();
  if (app.music) app.music.duck(st.active); // пока говорят, музыка уходит вглубь
  const inCall = new Set(snap.voice || []);
  const mySeat = snap.you ? snap.you.seat : -1;

  const btn = $('btn-voice');
  btn.textContent = st.active ? 'Выйти из звонка' : 'Войти в звонок';
  btn.classList.toggle('primary', !st.active);
  btn.disabled = !st.supported && !st.active;

  const mute = $('btn-mute');
  mute.hidden = !st.active;
  mute.textContent = st.muted ? 'Включить микрофон' : 'Выключить микрофон';
  mute.classList.toggle('active', st.muted);

  const err = $('voice-error');
  err.hidden = !st.error;
  err.textContent = st.error;

  const list = $('voice-list');
  list.innerHTML = '';
  for (const seat of (snap.game ? snap.game.seats : [])) {
    const li = document.createElement('li');
    const mine = seat.seat === mySeat;
    const peer = st.peers[seat.seat];
    const connected = mine ? st.active : Boolean(peer && peer.connected);
    const speaking = mine ? (st.active && !st.muted) : Boolean(peer && peer.speaking);

    li.className = [connected ? 'on' : '', speaking ? 'speaking' : '', mine ? 'me' : ''].join(' ').trim();
    const bulb = document.createElement('span');
    bulb.className = 'bulb';
    li.appendChild(bulb);
    const label = document.createElement('span');
    // Бот звонить не умеет: «не в звонке» про него только сбивало бы с толку.
    label.textContent = mine
      ? `${seat.name} — вы${st.active ? (st.muted ? ', микрофон выключен' : '') : ', не в звонке'}`
      : botOf(snap, seat.seat)
        ? `${seat.name} — бот, без голоса`
        : `${seat.name} — ${inCall.has(seat.seat) ? (connected ? 'на связи' : st.active ? 'соединяем…' : 'в звонке') : 'не в звонке'}`;
    li.appendChild(label);
    list.appendChild(li);
  }
}

// Чат разведён на два канала: общий слышат все, осадный — только двое
// осаждающих. Сервер и так не кладёт чужой канал в снимок; здесь просто
// раскладываем пришедшее по вкладкам.
function chatOf(snap, channel) {
  return (snap.chat || []).filter((m) => (m.channel || 'all') === channel);
}

function renderChat(snap) {
  const siege = Boolean(snap.siege);
  $('chat-channels').hidden = !siege;
  if (!siege && app.channel === 'siege') app.channel = 'all';
  $('chan-all').classList.toggle('active', app.channel === 'all');
  $('chan-siege').className = 'tab siege' + (app.channel === 'siege' ? ' active' : '');
  $('chat-input').placeholder = app.channel === 'siege'
    ? 'Только второму осаждающему…'
    : 'Сообщение всем…';

  const list = $('chat-list');
  const chat = chatOf(snap, app.channel);
  list.innerHTML = '';
  if (!chat.length) {
    const li = document.createElement('li');
    li.className = 'empty';
    li.textContent = app.channel === 'siege'
      ? 'Канал на двоих: замок этого не слышит. Делите стену — или обманывайте.'
      : snap.mode === 'hotseat'
        ? 'Общие заметки на всех: уговор, счёт, напоминание.'
        : 'Пока тихо. Договаривайтесь — или блефуйте.';
    list.appendChild(li);
  }
  for (const m of chat) {
    const li = document.createElement('li');
    li.className = m.side;
    li.innerHTML = `<b>${escapeHTML(m.name)}:</b> ${escapeHTML(m.text)}`;
    list.appendChild(li);
  }
  list.scrollTop = list.scrollHeight;

  // Внутри чата значок на вкладке «Осадный» горит, когда там написали, а
  // открыт общий. Снаружи о новом говорит счётчик на кнопке чата в пульте.
  const seenSiege = chatOf(snap, 'siege').length;
  if (app.channel === 'siege') app.siegeSeen = seenSiege;
  $('siege-badge').hidden = seenSiege <= app.siegeSeen;
}

function switchChannel(channel) {
  app.channel = channel;
  if (app.snap) renderChat(app.snap);
  $('chat-input').focus();
}

// --- окна пульта: чат и меню ---
//
// Чат свёрнут в кнопку: он нужен между ходами, а не на каждом шаге. Пока он
// закрыт, на кнопке видно, сколько пришло нового; открытый ложится поверх
// боковой колонки, а не поля, и закрывается той же кнопкой, Esc или щелчком мимо.

function renderChatButton(snap) {
  let newest = 0;
  for (const m of snap.chat || []) newest = Math.max(newest, m.ms || 0);
  // Переписку, которая была до входа в партию, новой не считаем.
  if (app.chatSeenMs === null || app.drop === 'chat') app.chatSeenMs = newest;
  const unread = (snap.chat || []).filter((m) => (m.ms || 0) > app.chatSeenMs).length;
  const count = $('chat-unread');
  count.hidden = unread === 0;
  count.textContent = unread > 9 ? '9+' : String(unread);
}

// setDrop открывает одно окно пульта и закрывает другое: чат и меню
// выпадают в одно место и перекрывали бы друг друга.
function setDrop(which) {
  app.drop = which;
  for (const [name, drop, btn] of [['chat', 'chat-drop', 'btn-chat'], ['menu', 'menu-drop', 'btn-menu']]) {
    $(drop).hidden = which !== name;
    $(btn).classList.toggle('open', which === name);
    $(btn).setAttribute('aria-expanded', String(which === name));
  }
  if (app.snap) renderChatButton(app.snap);
  if (which === 'chat') {
    // Пока чат был скрыт, прокрутка списка не работала — догоняем её.
    const list = $('chat-list');
    list.scrollTop = list.scrollHeight;
    $('chat-input').focus();
  }
}

// openRules показывает правила посреди партии. Текст тот же, что на главном
// экране: копируем его при первом открытии, а не держим в разметке дважды.
function openRules() {
  setDrop(null);
  const box = $('rules-copy');
  if (!box.childElementCount) {
    const src = document.querySelector('#screen-home .rules .rules-body');
    if (src) for (const p of src.children) box.appendChild(p.cloneNode(true));
  }
  $('rules-overlay').hidden = false;
  // Читать начинают сверху: фокус на кнопке внизу не должен прокручивать текст.
  box.parentElement.scrollTop = 0;
  $('btn-rules-close').focus({ preventScroll: true });
}

function closeRules() {
  $('rules-overlay').hidden = true;
}

function toggleThreat() {
  app.showThreat = !app.showThreat;
  $('btn-threat').classList.toggle('active', app.showThreat);
  $('btn-threat').setAttribute('aria-pressed', String(app.showThreat));
  // На узком экране значка нет — переключатель живёт в меню.
  $('menu-threat').setAttribute('aria-pressed', String(app.showThreat));
  render();
}


async function sendChat(ev) {
  ev.preventDefault();
  const input = $('chat-input');
  const text = input.value.trim();
  if (!text || !app.session) return;
  input.value = '';
  try {
    await api(`/api/rooms/${app.session.room}/chat`, { token: app.session.token, text, channel: app.channel });
  } catch (e) {
    toast(e.message);
    input.value = text; // не теряем набранное
  }
}

// renderOverlay — итог партии поверх поля. Партия — одна осада: король пал
// или замок выстоял, и следующей осады нет — только новая партия.
function renderOverlay(snap) {
  const g = snap.game;
  const over = g.phase === 'over' && Boolean(g.result);
  $('overlay').hidden = !over;
  if (!over) return;
  $('ov-summary').textContent = g.result.summary;
  const list = $('ov-points');
  list.innerHTML = '';
  // Первым — победитель: у кого больше очков, тот и взял партию.
  for (const s of [...(g.seats || [])].sort((a, b) => b.score - a.score)) {
    const side = SIDE_COLORS[s.side] ? SIDE_COLORS[s.side].name : '';
    const li = document.createElement('li');
    li.innerHTML = `<span>${escapeHTML(s.name)} <small>${side}</small></span>`
      + `<span class="gain">${s.score} ${plural(s.score, 'очко', 'очка', 'очков')}</span>`;
    list.appendChild(li);
  }
}

// --- действия ---

async function act(action) {
  try {
    await api(`/api/rooms/${app.session.room}/action`, { token: app.session.token, action });
    app.ui.mode = 'select';
    app.ui.unit = null;
    app.ui.ability = null;
    app.ui.build = null;
    app.ui.buildSpots = [];
    // После действия обстановка другая: осмотренная до него клетка уступает
    // место карточке отряда.
    app.ui.inspect = null;
    // Снимок по SSE мог прийти раньше сброса — перерисовываем панель,
    // иначе кнопка найма остаётся подсвеченной.
    if (app.snap && app.snap.game) { renderHUD(app.snap); render(); }
  } catch (e) {
    toast(e.message);
  }
}

// zoomAt приближает карту, оставляя клетку под курсором на месте.
function zoomAt(clientX, clientY, factor) {
  const before = layout(canvas, app.meta, app.view);
  const next = Math.max(1, Math.min(3.5, app.view.scale * factor));
  if (next === app.view.scale) return false;
  // Точка под курсором в клетках карты — до и после изменения масштаба.
  const fx = (clientX - before.rect.left - before.ox) / before.tile;
  const fy = (clientY - before.rect.top - before.oy) / before.tile;
  app.view.scale = next;
  const after = layout(canvas, app.meta, app.view);
  app.view.ox += (clientX - after.rect.left - after.ox) - fx * after.tile;
  app.view.oy += (clientY - after.rect.top - after.oy) - fy * after.tile;
  clampView(canvas, app.meta, app.view);
  syncTouchAction();
  render();
  return true;
}

function resetView() {
  app.view = { scale: 1, ox: 0, oy: 0 };
  syncTouchAction();
  render();
}

// Пока карта вписана в экран, тащить её некуда — и вертикальный свайп по
// полю пусть листает страницу, как везде. Как только приблизили, поле
// забирает жесты себе: иначе палец одновременно тянет карту и прокручивает
// страницу, и не выходит ни то, ни другое. Щипок наш в обоих случаях.
function syncTouchAction() {
  if (canvas) canvas.style.touchAction = app.view.scale > 1 ? 'none' : 'pan-y';
}

// Перетаскивание карты. Короткое нажатие остаётся кликом по клетке.
// timer и long — долгое нажатие пальцем: на телефоне это осмотр клетки, как
// правая кнопка мыши. Сработав, оно гасит щелчок, который пришёл бы следом.
const drag = { active: false, moved: 0, x: 0, y: 0, timer: 0, long: false };
const LONG_PRESS_MS = 450;

// Щипок двумя пальцами: единственный способ приблизить карту на телефоне —
// колесо мыши там взять неоткуда, а 28 клеток в ширину экрана не читаются.
const touches = new Map(); // активные касания поля
const pinch = { on: false, dist: 0, x: 0, y: 0 };

function pinchGeom() {
  const [a, b] = [...touches.values()];
  return { dist: Math.hypot(a.x - b.x, a.y - b.y), x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
}

function onPointerDown(ev) {
  if (ev.button !== 0) return;
  touches.set(ev.pointerId, { x: ev.clientX, y: ev.clientY });
  if (touches.size === 2) {
    // Второй палец отменяет и перетаскивание, и долгое нажатие: это щипок.
    clearTimeout(drag.timer);
    drag.active = false;
    drag.long = false;
    const g = pinchGeom();
    pinch.on = true;
    pinch.dist = g.dist;
    pinch.x = g.x;
    pinch.y = g.y;
    return;
  }
  if (touches.size > 2) return;
  drag.active = true;
  drag.moved = 0;
  drag.x = ev.clientX;
  drag.y = ev.clientY;
  drag.long = false;
  clearTimeout(drag.timer);
  if (ev.pointerType !== 'mouse') {
    const { clientX, clientY } = ev;
    drag.timer = setTimeout(() => {
      if (!drag.active || drag.moved >= 6) return;
      const cell = pickTile(canvas, app.meta, clientX, clientY, app.view);
      if (!cell || !app.snap || !app.snap.game) return;
      drag.long = true;
      inspectCell(cell);
    }, LONG_PRESS_MS);
  }
  // Палец может исчезнуть между событиями — тогда захватывать уже нечего.
  try { canvas.setPointerCapture(ev.pointerId); } catch { /* указателя уже нет */ }
}

function onPointerMove(ev) {
  if (touches.has(ev.pointerId)) touches.set(ev.pointerId, { x: ev.clientX, y: ev.clientY });
  if (pinch.on) {
    if (touches.size < 2) return;
    const g = pinchGeom();
    // Пальцы разъехались — масштаб, поехали вместе — панорама.
    app.view.ox += g.x - pinch.x;
    app.view.oy += g.y - pinch.y;
    pinch.x = g.x;
    pinch.y = g.y;
    const factor = pinch.dist > 0 ? g.dist / pinch.dist : 1;
    pinch.dist = g.dist;
    if (!zoomAt(g.x, g.y, factor)) { clampView(canvas, app.meta, app.view); render(); }
    return;
  }
  if (!drag.active) {
    const cell = pickTile(canvas, app.meta, ev.clientX, ev.clientY, app.view);
    const prev = app.ui.hover;
    if ((prev && cell && prev.x === cell.x && prev.y === cell.y) || (!prev && !cell)) return;
    app.ui.hover = cell;
    renderStrip();
    render();
    return;
  }
  const dx = ev.clientX - drag.x;
  const dy = ev.clientY - drag.y;
  drag.moved += Math.abs(dx) + Math.abs(dy);
  drag.x = ev.clientX;
  drag.y = ev.clientY;
  // Палец поехал — это перетаскивание карты, а не долгое нажатие.
  if (drag.moved >= 6) clearTimeout(drag.timer);
  app.view.ox += dx;
  app.view.oy += dy;
  clampView(canvas, app.meta, app.view);
  render();
}

function onPointerUp(ev) {
  touches.delete(ev.pointerId);
  if (pinch.on) {
    // Палец, поднятый после щипка, щелчком по клетке не считается.
    if (touches.size < 2) { pinch.on = false; drag.active = false; }
    try { canvas.releasePointerCapture(ev.pointerId); } catch { /* этот палец не захватывали */ }
    return;
  }
  if (!drag.active) return;
  drag.active = false;
  clearTimeout(drag.timer);
  // Захват мог сняться раньше — например, системным жестом браузера.
  try { canvas.releasePointerCapture(ev.pointerId); } catch { /* уже отпущен */ }
  if (drag.long) return; // долгое нажатие уже показало клетку
  if (drag.moved < 6) onCanvasClick(ev); // это был клик, а не перетаскивание
}

// onContextMenu — правая кнопка: осмотр клетки без действия, даже когда
// выбран отряд. Меню браузера над полем не нужно.
function onContextMenu(ev) {
  ev.preventDefault();
  const cell = pickTile(canvas, app.meta, ev.clientX, ev.clientY, app.view);
  if (!cell || !app.snap || !app.snap.game) return;
  // Строка под полем говорит о клетке под курсором — пусть это будет та,
  // которую сейчас осматривают, даже если мышь до щелчка не шевелилась.
  if (ev.pointerType !== 'touch') app.ui.hover = cell;
  inspectCell(cell);
}

function onCanvasClick(ev) {
  const snap = app.snap;
  if (!snap || !snap.game) return;
  const g = snap.game;
  const cell = pickTile(canvas, app.meta, ev.clientX, ev.clientY, app.view);
  if (!cell) return;
  if (ev.pointerType === 'mouse') app.ui.hover = cell;
  // В чужой ход действовать нельзя, а смотреть — можно: и на свои отряды,
  // и на то, что творится у стены.
  if (!g.you.myTurn) { inspectCell(cell); return; }

  if (app.ui.mode === 'deploy') {
    act({ kind: 'deploy', unit: app.ui.unit, to: cell });
    return;
  }
  if (app.ui.mode === 'ability') {
    // Сервер ждёт номер столбца: у смолы подсвеченная клетка лежит рядом
    // со стеной, а не на ней, но столбец тот же самый.
    const ok = (app.ui.segments || []).some((p) => p.x === cell.x && p.y === cell.y);
    if (!ok) { toast(app.ui.ability === 'pitch' ? 'Выберите клетку под стеной' : 'Выберите сегмент стены'); return; }
    act({ kind: 'ability', ability: app.ui.ability, at: cell });
    return;
  }
  if (app.ui.mode === 'build') {
    act({ kind: 'build', unitId: app.ui.selectedId, build: app.ui.build, to: cell });
    return;
  }
  if (app.ui.mode === 'construct') {
    act({ kind: 'construct', build: app.ui.court, to: cell });
    return;
  }
  if (app.ui.mode === 'heal') {
    act({ kind: 'heal', unitId: app.ui.selectedId, to: cell });
    return;
  }
  if (app.ui.mode === 'charge') {
    act({ kind: 'charge', unitId: app.ui.selectedId, to: cell });
    return;
  }
  if (app.ui.mode === 'unload') {
    act({ kind: 'unload', unitId: app.ui.unloadId, to: cell });
    return;
  }

  const here = (g.units || []).find((u) => u.pos.x === cell.x && u.pos.y === cell.y);
  if (here && here.mine) {
    app.ui.inspect = null;
    app.ui.selectedId = here.id === app.ui.selectedId ? 0 : here.id;
    syncUI(snap);
    renderHUD(snap);
    render();
    return;
  }
  const area = areaOfSelected();
  const canStep = app.ui.moves.some((p) => p.x === cell.x && p.y === cell.y);
  // Зона навесного обстрела накрывает и те клетки, куда машина может просто
  // шагнуть. По умолчанию выигрывает шаг — иначе катапульту не сдвинуть;
  // чтобы положить камни себе под нос, есть режим «Навесной удар».
  if (area && (app.ui.mode === 'bombard' || !canStep)) {
    const dist = Math.max(Math.abs(area.pos.x - cell.x), Math.abs(area.pos.y - cell.y));
    if (dist >= 1 && dist <= area.reach) {
      act({ kind: 'attack', unitId: app.ui.selectedId, to: cell });
      return;
    }
    if (!here) { toast('Слишком далеко для навесного удара'); return; }
  }
  if (app.ui.selectedId) {
    // Пустая вышка одновременно и цель, и клетка для шага. По умолчанию
    // выигрывает шаг: заходят в них куда чаще, чем ломают.
    // Снести её можно издали или кнопкой «Разбить вышку».
    const enterable = canStep && app.ui.mode !== 'strike' && isEmptyTower(cell);
    if (!enterable && app.ui.targets.some((p) => p.x === cell.x && p.y === cell.y)) {
      act({ kind: 'attack', unitId: app.ui.selectedId, to: cell });
      return;
    }
    if (canStep) {
      act({ kind: 'move', unitId: app.ui.selectedId, to: cell });
      return;
    }
  }
  // Щелчок, за которым нет действия, — осмотр клетки. Выбор отряда при этом
  // снимается, как и раньше: карточка одна, и теперь в ней клетка.
  app.ui.selectedId = 0;
  syncUI(snap);
  inspectCell(cell);
}

// --- отрисовка ---

let rafPending = false;
// threatCells — объединение зон обстрела всех видимых врагов.
function threatCells() {
  const g = app.snap && app.snap.game;
  if (!g || !app.showThreat) return [];
  const seen = new Set();
  const out = [];
  for (const u of g.units || []) {
    // Союзник по осаде не враг: его зону закрашивать незачем.
    if (u.mine || !u.reach || !isEnemy(g.you.side, u.side)) continue;
    for (let dy = -u.reach; dy <= u.reach; dy++) {
      for (let dx = -u.reach; dx <= u.reach; dx++) {
        if (!dx && !dy) continue;
        const x = u.pos.x + dx, y = u.pos.y + dy;
        if (x < 0 || y < 0 || x >= app.meta.cols || y >= app.meta.rows) continue;
        const key = x + ',' + y;
        if (seen.has(key)) continue;
        seen.add(key);
        out.push({ x, y });
      }
    }
  }
  return out;
}

// reachOfSelected — зона поражения выбранного юнита.
function reachOfSelected() {
  const u = selectedUnit();
  if (!u || !u.reach || u.reach < 1) return null;
  return { pos: u.pos, range: u.reach };
}

function selectedUnit() {
  const g = app.snap && app.snap.game;
  if (!g || !app.ui.selectedId) return null;
  return (g.units || []).find((x) => x.id === app.ui.selectedId) || null;
}

// areaOfSelected — выбран ли сейчас навесной стрелок: он бьёт по клетке,
// а не по цели, поэтому подсветка целей ему не нужна.
function areaOfSelected() {
  const u = selectedUnit();
  if (!u || !u.mine || !u.area) return null;
  return { pos: u.pos, reach: u.reach, radius: u.area };
}

// isEmptyTower — пустой ли наблюдательный пункт на этой клетке. Только в него
// можно войти, и только из-за неё клик бывает двусмысленным.
function isEmptyTower(cell) {
  const g = app.snap && app.snap.game;
  if (!g) return false;
  const b = (g.buildings || []).find((x) => x.kind === 'tower' && x.pos.x === cell.x && x.pos.y === cell.y);
  if (!b) return false;
  return !(g.units || []).some((u) => u.pos.x === cell.x && u.pos.y === cell.y);
}

// areaPreview — показывать ли призрак накрытия под курсором. В обычном
// режиме клик по светлой клетке двигает машину, поэтому там накрытие не
// рисуем: иначе подсказка врала бы.
function areaPreview() {
  const g = app.snap && app.snap.game;
  if (!g || !g.you.myTurn) return null;
  const a = areaOfSelected();
  if (!a) return null;
  const h = app.ui.hover;
  if (app.ui.mode !== 'bombard' && h && app.ui.moves.some((p) => p.x === h.x && p.y === h.y)) return null;
  return a;
}

// fencePreview — призрак прясла под курсором. Частокол замка встаёт линией
// в три клетки, и видеть надо всю линию, а не клетку под мышью. Линии
// считает сервер (fenceAt): крайние колья стоят в двух клетках от инженера,
// и годность этих клеток клиенту знать не откуда.
function fencePreview() {
  if (app.ui.mode !== 'build' || app.ui.build !== 'barricade' || !app.ui.hover) return null;
  const g = app.snap && app.snap.game;
  if (!g || !g.you.myTurn) return null;
  const u = (g.units || []).find((x) => x.id === app.ui.selectedId && x.mine);
  const h = app.ui.hover;
  const line = ((u && u.fenceAt) || []).find((l) => l.length && l[0].x === h.x && l[0].y === h.y);
  return line || null;
}

function render() {
  if (rafPending) return;
  rafPending = true;
  requestAnimationFrame(() => {
    rafPending = false;
    if (!app.meta) return;
    draw(ctx, canvas, app.meta, app.snap, {
      ...app.ui,
      court: app.ui.courtSpots,
      healSpots: app.ui.healSpots,
      chargeSpots: app.ui.chargeSpots,
      unloadSpots: app.ui.unloadSpots,
      threat: threatCells(),
      reach: reachOfSelected(),
      area: areaPreview(),
      fence: fencePreview(),
    }, app.view);
  });
}

// tickTimer ведёт часы хода: в свой ход — на кнопке «Завершить ход», в чужой —
// рядом с именем ходящего. Без часов на кнопке подсказка про Enter: вместе
// они не помещаются.
function tickTimer() {
  const snap = app.snap;
  const on = Boolean(snap && snap.deadlineMs && snap.game && snap.game.phase === 'play');
  $('end-timer').hidden = !on;
  $('wait-timer').hidden = !on;
  $('end-key').hidden = on;
  if (!on) return;
  const left = Math.max(0, Math.round((snap.deadlineMs - (Date.now() + app.clockSkew)) / 1000));
  const text = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`;
  $('end-timer').textContent = text;
  $('wait-timer').textContent = `· ${text}`;
}

// --- утилиты ---

// initial — одна буква на узкой плашке игрока. «Игрок 2» — цифрой: имена по
// умолчанию начинаются одинаково.
function initial(name) {
  const num = /(\d+)\s*$/.exec(name);
  return num ? num[1] : (String(name).trim()[0] || '?').toUpperCase();
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 10 || m100 >= 20)) return few;
  return many;
}

// --- запуск ---

async function boot() {
  canvas = $('board');
  // Музыку поднимаем до запроса к серверу: она должна играть на главном
  // экране даже тогда, когда сервер не ответил.
  app.music = createMusic({ onChange: paintMusic });
  $('btn-music').onclick = toggleMusic;
  $('btn-music-home').onclick = toggleMusic;
  for (const id of ['music-vol', 'music-vol-home']) {
    $(id).oninput = (e) => app.music.setVolume(Number(e.target.value) / 100);
  }
  paintMusic();
  app.rawMeta = await api('/api/meta');
  app.meta = Object.assign({}, app.rawMeta, app.rawMeta.boards.classic);
  app.boardKey = 'classic';

  app.voice = createVoice({
    iceServers: app.meta.iceServers || [],
    post: (what, body) => api(`/api/rooms/${app.session.room}/${what}`, { token: app.session.token, ...body }),
    onChange: () => { if (app.snap) renderVoice(app.snap); },
  });

  ctx = resize(canvas);
  new ResizeObserver(() => { ctx = resize(canvas); render(); }).observe(canvas);

  canvas.addEventListener('pointerdown', onPointerDown);
  canvas.addEventListener('pointermove', onPointerMove);
  canvas.addEventListener('pointerup', onPointerUp);
  canvas.addEventListener('pointercancel', (ev) => {
    touches.delete(ev.pointerId);
    if (touches.size < 2) pinch.on = false;
    drag.active = false;
    clearTimeout(drag.timer);
  });
  // iOS масштабирует щипком всю страницу поверх touch-action — свой щипок
  // по полю важнее, поэтому системный здесь гасим.
  canvas.addEventListener('gesturestart', (ev) => ev.preventDefault());
  syncTouchAction();
  canvas.addEventListener('mouseleave', () => { app.ui.hover = null; renderStrip(); render(); });
  canvas.addEventListener('contextmenu', onContextMenu);
  canvas.addEventListener('dblclick', resetView);
  canvas.addEventListener('wheel', (ev) => {
    ev.preventDefault();
    zoomAt(ev.clientX, ev.clientY, ev.deltaY < 0 ? 1.15 : 1 / 1.15);
  }, { passive: false });

  // Выбор ботов: на общем экране — у каждого места, против ботов — общий
  // уровень, в лобби — для того, кого сажают.
  for (const [i, id] of ['hs1-who', 'hs2-who', 'hs3-who'].entries()) {
    const who = $(id);
    fillLevels(who, { human: true });
    who.onchange = () => { $(`hs${i + 1}`).placeholder = who.value ? 'имя дома из лора' : ['Аня', 'Боря', 'Витя'][i]; };
  }
  fillLevels($('solo-level'), { value: savedLevel() });
  $('solo-level').onchange = () => { saveLevel($('solo-level').value); showLevelHint(); };
  showLevelHint();
  for (const [id, hint] of [['hs-map', 'hs-map-hint'], ['solo-map', 'solo-map-hint']]) {
    const sel = $(id);
    if (!sel) continue;
    fillMaps(sel, savedMap());
    sel.onchange = () => { saveMap(sel.value); showMapHint(sel, hint); };
    showMapHint(sel, hint);
  }
  fillLevels($('lobby-bot-level'), { value: savedLevel() });
  $('btn-add-bot').onclick = () => {
    saveLevel($('lobby-bot-level').value);
    botOp({ op: 'add', level: $('lobby-bot-level').value });
  };
  $('btn-solo').onclick = createSolo;
  $('btn-hotseat').onclick = createHotseat;
  $('btn-create').onclick = createOnline;
  $('btn-join').onclick = joinOnline;
  $('btn-end').onclick = () => act({ kind: 'endTurn' });
  $('btn-threat').onclick = toggleThreat;
  $('menu-threat').onclick = toggleThreat;
  $('btn-chat').onclick = () => setDrop(app.drop === 'chat' ? null : 'chat');
  $('btn-menu').onclick = () => setDrop(app.drop === 'menu' ? null : 'menu');
  $('btn-rules').onclick = openRules;
  $('btn-rules-close').onclick = closeRules;
  $('rules-overlay').onclick = (e) => { if (e.target === e.currentTarget) closeRules(); };
  // Щелчок мимо открытого окна пульта закрывает его.
  document.addEventListener('pointerdown', (e) => {
    if (!app.drop) return;
    const drop = $(app.drop === 'chat' ? 'chat-drop' : 'menu-drop');
    const btn = $(app.drop === 'chat' ? 'btn-chat' : 'btn-menu');
    if (!drop.contains(e.target) && !btn.contains(e.target)) setDrop(null);
  });
  $('btn-voice').onclick = async () => {
    const st = app.voice.state();
    if (st.active) await app.voice.leave();
    else await app.voice.join(app.snap && app.snap.you ? app.snap.you.seat : -1);
    if (app.snap) renderHUD(app.snap);
  };
  $('btn-mute').onclick = () => {
    app.voice.toggleMute();
    if (app.snap) renderHUD(app.snap);
  };
  $('btn-leave').onclick = () => { setDrop(null); leaveRoom(true); };
  $('btn-new-game').onclick = () => { forgetSession(); show('screen-home'); homeError(''); };
  $('btn-leave-lobby').onclick = () => leaveRoom(false);
  $('chan-all').onclick = () => switchChannel('all');
  $('chan-siege').onclick = () => switchChannel('siege');
  $('chat-form').onsubmit = sendChat;
  $('btn-copy').onclick = async () => {
    try {
      await navigator.clipboard.writeText(location.href);
      $('btn-copy').textContent = 'Ссылка скопирована';
    } catch { $('btn-copy').textContent = location.href; }
  };

  document.addEventListener('keydown', (e) => {
    // Esc сначала закрывает то, что открыто поверх поля: правила, чат, меню.
    // Выбор отряда при этом не сбрасывается.
    if (e.key === 'Escape' && (app.drop || !$('rules-overlay').hidden)) {
      if (e.target instanceof HTMLElement && e.target.closest('.drop')) e.target.blur();
      setDrop(null);
      closeRules();
      return;
    }
    if (!(e.target instanceof HTMLInputElement)) {
      const rect = canvas.getBoundingClientRect();
      const cx = rect.left + rect.width / 2, cy = rect.top + rect.height / 2;
      if (e.key === '+' || e.key === '=') zoomAt(cx, cy, 1.25);
      if (e.key === '-' || e.key === '_') zoomAt(cx, cy, 1 / 1.25);
      if (e.key === '0') resetView();
      if (e.key === 't' || e.key === 'T' || e.key === 'е' || e.key === 'Е') toggleThreat();
    }
    if (e.key === 'Escape' && !(e.target instanceof HTMLInputElement)) {
      // Первым Esc закрывается осмотр клетки — выбор отряда под ним остаётся.
      if (app.ui.inspect) {
        app.ui.inspect = null;
      } else {
        app.ui.mode = 'select';
        app.ui.unit = app.ui.ability = app.ui.build = null;
        app.ui.buildSpots = [];
        app.ui.selectedId = 0;
      }
      if (app.snap && app.snap.game) { renderHUD(app.snap); render(); }
    }
    // Enter на кнопке нажимает саму кнопку: завершать ход ещё раз поверх
    // этого нельзя — после щелчка по «Завершить ход» фокус остаётся на ней.
    const typing = e.target instanceof HTMLInputElement || e.target instanceof HTMLButtonElement;
    if (e.key === 'Enter' && !typing && $('rules-overlay').hidden && app.snap && app.snap.game && app.snap.game.you.myTurn) {
      act({ kind: 'endTurn' });
    }
  });

  setInterval(tickTimer, 500);

  // Закрыли вкладку — выходим из звонка, иначе собеседники будут видеть
  // «соединяем…» до истечения комнаты.
  window.addEventListener('pagehide', () => {
    if (app.voice && app.voice.state().active && app.session) {
      navigator.sendBeacon?.(
        `/api/rooms/${app.session.room}/voice`,
        new Blob([JSON.stringify({ token: app.session.token, active: false })], { type: 'application/json' }),
      );
    }
  });

  // Возврат в партию: код из ссылки, токен из localStorage.
  const code = new URL(location.href).searchParams.get('room');
  const saved = loadSession();
  if (code) $('net-code').value = code;
  if (saved && (!code || saved.room === code)) {
    app.session = saved;
    try {
      const snap = await api(`/api/rooms/${saved.room}/state?token=${encodeURIComponent(saved.token)}`);
      onSnapshot(snap);
      connect();
      return;
    } catch {
      localStorage.removeItem(STORE_KEY); // комната истекла
    }
  }
  show('screen-home');
}

boot().catch((e) => { show('screen-home'); homeError(`Не удалось связаться с сервером: ${e.message}`); });
