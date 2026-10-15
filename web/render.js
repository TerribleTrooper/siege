// Отрисовка поля на canvas. Модуль ничего не знает про сеть: получает
// снимок состояния от сервера и рисует его.

export const SIDE_COLORS = {
  defender: { body: '#4c7fb8', edge: '#9ec6ef', name: 'Замок' },
  attackerA: { body: '#b4503c', edge: '#edb4a4', name: 'Осада I' },
  attackerB: { body: '#4f8c55', edge: '#aedcae', name: 'Осада II' },
  neutral: { body: '#6b6b6b', edge: '#bbbbbb', name: '—' },
};

const TILE_MAX = 54;
const TILE_DETAIL = 26; // ниже этого размера мелкую фактуру не рисуем

// fitTile — размер клетки, при котором карта целиком влезает в canvas.
function fitTile(rect, meta) {
  return Math.min(rect.width / meta.cols, rect.height / meta.rows);
}

// layout считает размер клетки и отступы с учётом зума и панорамы.
// view = {scale, ox, oy}: scale 1 — вся карта в экране, больше — приближение.
export function layout(canvas, meta, view) {
  const rect = canvas.getBoundingClientRect();
  const scale = (view && view.scale) || 1;
  const tile = Math.max(6, Math.min(TILE_MAX, Math.floor(fitTile(rect, meta) * scale)));
  const w = tile * meta.cols;
  const h = tile * meta.rows;
  const ox = Math.floor((rect.width - w) / 2 + ((view && view.ox) || 0));
  const oy = Math.floor((rect.height - h) / 2 + ((view && view.oy) || 0));
  return { tile, ox, oy, w, h, rect };
}

// clampView не даёт утащить карту за пределы экрана.
export function clampView(canvas, meta, view) {
  const { w, h, rect } = layout(canvas, meta, view);
  const limitX = Math.max(0, (w - rect.width) / 2);
  const limitY = Math.max(0, (h - rect.height) / 2);
  view.ox = Math.max(-limitX, Math.min(limitX, view.ox));
  view.oy = Math.max(-limitY, Math.min(limitY, view.oy));
  return view;
}

// pickTile переводит координаты мыши в клетку поля.
export function pickTile(canvas, meta, clientX, clientY, view) {
  const { tile, ox, oy, rect } = layout(canvas, meta, view);
  const x = Math.floor((clientX - rect.left - ox) / tile);
  const y = Math.floor((clientY - rect.top - oy) / tile);
  if (x < 0 || y < 0 || x >= meta.cols || y >= meta.rows) return null;
  return { x, y };
}

// Детерминированный шум: трава не должна «кипеть» между кадрами.
function noise(x, y) {
  const n = Math.sin(x * 127.1 + y * 311.7) * 43758.5453;
  return n - Math.floor(n);
}

export function resize(canvas) {
  const dpr = window.devicePixelRatio || 1;
  const rect = canvas.getBoundingClientRect();
  canvas.width = Math.floor(rect.width * dpr);
  canvas.height = Math.floor(rect.height * dpr);
  const ctx = canvas.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  return ctx;
}

export function draw(ctx, canvas, meta, snap, ui, view) {
  const rect = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, rect.width, rect.height);
  const L = layout(canvas, meta, view);
  const game = snap && snap.game;

  const wallByCol = new Map();
  if (game) for (const seg of game.wall || []) wallByCol.set(seg.col, seg);

  // Ров: сервер присылает только **незасыпанные** клетки. Остальные уже
  // работают как поле, и канавой их рисовать нельзя — иначе игрок не увидит
  // по карте, куда можно подвести таран.
  const ditch = new Set();
  if (game) for (const p of game.moat || []) ditch.add(p.x);

  for (let y = 0; y < meta.rows; y++) {
    for (let x = 0; x < meta.cols; x++) {
      drawTile(ctx, L, meta, x, y, wallByCol.get(x), y === meta.wallRow, ditch.has(x));
    }
  }

  if (!game) { drawFrame(ctx, L); return; }

  drawSpots(ctx, L, game.mine || [], 'mine');
  drawSpots(ctx, L, game.farm || [], 'farm');
  // Мост в две клетки: перила идут по внешним краям, а на стыке половинок
  // их быть не должно — иначе посреди настила вырастает забор.
  bridgeCells.clear();
  for (const b of game.buildings || []) if (b.kind === 'bridge') bridgeCells.add(`${b.pos.x},${b.pos.y}`);
  for (const b of game.buildings || []) drawBuilding(ctx, L, b);
  // Туман ложится поверх ландшафта и построек, но под свои отряды и подсветку:
  // карту мы помним, а что на ней происходит сейчас — нет.
  if (game.fog) drawFog(ctx, L, meta, game.fog);
  if (ui.threat && ui.threat.length) drawThreat(ctx, L, ui.threat);
  if (ui.reach) drawReach(ctx, L, ui.reach);
  if (ui.area && ui.hover) drawAreaPreview(ctx, L, ui.hover, ui.area.radius);
  drawHighlights(ctx, L, ui);
  for (const t of game.traps || []) drawTrap(ctx, L, t);
  for (const u of game.units || []) drawUnit(ctx, L, u, ui, specOf(meta, u.type));
  drawFrame(ctx, L);
}

// specOf — характеристики типа отряда из meta. Форма фишки и серебряный обод
// берутся из них, а не из отдельного списка: новый тип отряда сразу получит
// правильную фишку. Указатель по имени строим один раз на набор характеристик.
const specIndex = new WeakMap();
function specOf(meta, type) {
  const specs = meta && meta.specs;
  if (!specs) return null;
  let byName = specIndex.get(specs);
  if (!byName) {
    byName = new Map(specs.map((sp) => [sp.name, sp]));
    specIndex.set(specs, byName);
  }
  return byName.get(type) || null;
}

function tileRect(L, x, y) {
  return { x: L.ox + x * L.tile, y: L.oy + y * L.tile, s: L.tile };
}

// Земля под клетками. Цвета почти прежние, меняется фактура: по ней клетку
// узнают и тогда, когда на ней стоит отряд.
const GROUND = {
  '.': '#3f5a33', // поле
  R: '#2d2823', // скалы по флангам: непроходимы
  V: '#16120f', // обрыв: не пройти, но простреливается
  B: '#33502b', // кусты: укрытие от стрел
  M: '#5b503d', // рудник: источник серебра
  F: '#6f6334', // пашня: обоз подвозит припасы
  C: '#5d5648', // двор
  W: '#665440', // боевой ход: дощатый настил
  a: '#4d4430', // лагеря осаждающих
  b: '#4d4430',
};

function drawTile(ctx, L, meta, x, y, seg, isWallRow, moatOpen) {
  const kind = meta.tiles[y][x];
  const { x: px, y: py, s } = tileRect(L, x, y);
  const isWall = kind === '#' || kind === 'T' || kind === 'G';

  let base = GROUND[kind] || GROUND['.'];
  // Ров: сервер присылает только **незасыпанные** клетки. Засыпанная — уже
  // земля, а настил из вязанок поверх неё рисуется постройкой: у него своя
  // прочность, и по карте видно, куда можно подвести таран.
  if (kind === 'D') base = moatOpen ? '#1b3542' : '#3d3a26';
  if (isWall) base = kind === 'T' ? '#7f776b' : '#8d8579';
  ctx.fillStyle = shade(base, noise(x, y) * 10 - 5);
  ctx.fillRect(px, py, s, s);

  // Купы, валуны, волны и обломки нарочно заходят за край — обрезаем их
  // клеткой. Остальная фактура в клетку укладывается сама, и лишний clip
  // на каждой из шестисот клеток кадру ни к чему.
  const clip = kind === 'B' || kind === 'R' || kind === 'D' || isWall;
  if (clip) {
    ctx.save();
    ctx.beginPath();
    ctx.rect(px, py, s, s);
    ctx.clip();
  }
  switch (kind) {
    case '.':
      drawGrass(ctx, px, py, s, x, y);
      break;
    case 'B':
      drawBushes(ctx, px, py, s, x, y);
      break;
    case 'V':
      drawRavine(ctx, px, py, s, x, y);
      break;
    case 'R':
      drawRocks(ctx, px, py, s, x, y);
      break;
    case 'M':
      drawOre(ctx, px, py, s, x, y);
      break;
    case 'F':
      drawFurrows(ctx, px, py, s);
      break;
    case 'D':
      drawMoat(ctx, px, py, s, x, y, moatOpen);
      break;
    case 'C':
      drawPaving(ctx, px, py, s, y);
      break;
    case 'W':
      drawPlanks(ctx, px, py, s, x);
      break;
    case 'a':
    case 'b':
      drawCamp(ctx, px, py, s, x, y, kind === 'a' ? 'attackerA' : 'attackerB');
      break;
  }
  if (isWall) drawWallTile(ctx, px, py, s, x, kind, isWallRow ? seg : null);
  if (clip) ctx.restore();

  // Сетка.
  ctx.strokeStyle = 'rgba(0,0,0,.22)';
  ctx.lineWidth = 1;
  ctx.strokeRect(px + 0.5, py + 0.5, s - 1, s - 1);

  // Прочность сегмента — только у повреждённого: целая стена не рябит
  // зелёными чёрточками, а слабые места видно с первого взгляда. Пролом
  // полоски не получает — он нарисован обломками.
  if (isWallRow && seg && seg.hp > 0 && seg.hp < seg.maxHp) {
    drawBar(ctx, px + s / 2, py + Math.max(2, s * 0.06), s * 0.8, Math.max(2.5, s * 0.09), seg.hp / seg.maxHp);
  }
}

// Травинки вместо точек: фон, на котором видно всё остальное.
function drawGrass(ctx, px, py, s, x, y) {
  const n = s >= TILE_DETAIL ? 4 : 2;
  const h = s * 0.09;
  ctx.strokeStyle = 'rgba(20,32,14,.42)';
  ctx.lineWidth = 1;
  ctx.lineCap = 'round';
  ctx.beginPath();
  for (let i = 0; i < n; i++) {
    const gx = px + s * (0.12 + noise(x * 3 + i, y * 5) * 0.76);
    const gy = py + s * (0.18 + noise(y * 3 + i, x * 5) * 0.7);
    ctx.moveTo(gx - h * 0.5, gy - h);
    ctx.lineTo(gx, gy);
    ctx.lineTo(gx + h * 0.5, gy - h);
  }
  ctx.stroke();
}

// Кусты обрамляют клетку, а не закрывают её: отряд в укрытии виден посреди
// листвы — ровно то, что значит «он в кустах».
const BUSH_CLUMPS = [
  [0.2, 0.22, 0.2], [0.55, 0.12, 0.17], [0.86, 0.3, 0.19],
  [0.12, 0.62, 0.18], [0.84, 0.74, 0.2], [0.42, 0.88, 0.17], [0.66, 0.5, 0.11],
];

function drawBushes(ctx, px, py, s, x, y) {
  const clumps = BUSH_CLUMPS.map(([fx, fy, fr], i) => [
    px + s * (fx + (noise(x * 9 + i, y * 23) - 0.5) * 0.08),
    py + s * (fy + (noise(y * 9 + i, x * 23) - 0.5) * 0.08),
    s * fr,
    i,
  ]);
  // Три прохода — тень, крона, блик: купы ложатся друг на друга, а не
  // сливаются в одно пятно. Каждый проход — один путь, одна заливка.
  const pass = (color, pick, place) => {
    ctx.fillStyle = color;
    ctx.beginPath();
    for (const c of clumps) {
      if (!pick(c[3])) continue;
      const [cx, cy, r] = place(c);
      ctx.moveTo(cx + r, cy);
      ctx.arc(cx, cy, r, 0, Math.PI * 2);
    }
    ctx.fill();
  };
  pass('rgba(12,22,9,.55)', () => true, ([cx, cy, r]) => [cx + r * 0.12, cy + r * 0.2, r]);
  pass('#3e6531', (i) => i % 2 === 0, ([cx, cy, r]) => [cx, cy, r]);
  pass('#365e2b', (i) => i % 2 === 1, ([cx, cy, r]) => [cx, cy, r]);
  pass('rgba(150,196,104,.26)', () => true, ([cx, cy, r]) => [cx - r * 0.3, cy - r * 0.32, r * 0.45]);
}

// Обрыв: светлый край сверху, к середине провал темнеет, на дне осыпь.
function drawRavine(ctx, px, py, s, x, y) {
  const j = (i) => noise(x * 13 + i, y * 7) * s * 0.12;
  const rim = [
    [px, py + s * 0.08 + j(1)],
    [px + s * 0.3, py + s * 0.02 + j(2)],
    [px + s * 0.62, py + s * 0.1 + j(3)],
    [px + s, py + s * 0.04 + j(4)],
  ];
  const g = ctx.createLinearGradient(0, py, 0, py + s);
  g.addColorStop(0, '#2c241d');
  g.addColorStop(0.35, '#0f0c0a');
  g.addColorStop(1, '#070605');
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.moveTo(rim[0][0], rim[0][1]);
  for (const [rx, ry] of rim.slice(1)) ctx.lineTo(rx, ry);
  ctx.lineTo(px + s, py + s);
  ctx.lineTo(px, py + s);
  ctx.closePath();
  ctx.fill();
  ctx.strokeStyle = 'rgba(160,136,108,.5)';
  ctx.lineWidth = Math.max(1, s * 0.04);
  ctx.beginPath();
  ctx.moveTo(rim[0][0], rim[0][1]);
  for (const [rx, ry] of rim.slice(1)) ctx.lineTo(rx, ry);
  ctx.stroke();
  ctx.fillStyle = 'rgba(120,104,86,.45)';
  const d = Math.max(1, s * 0.05);
  for (let i = 0; i < 4; i++) {
    ctx.fillRect(px + s * (0.1 + noise(x * 5 + i, y * 11) * 0.8), py + s * (0.3 + noise(y * 5 + i, x * 11) * 0.6), d, d);
  }
}

// Скалы: три крупных валуна без мелкой ряби — фон, а не узор.
const ROCKS = [[0.3, 0.32, 0.3], [0.76, 0.4, 0.26], [0.42, 0.8, 0.28]];

function drawRocks(ctx, px, py, s, x, y) {
  for (let i = 0; i < ROCKS.length; i++) {
    const [fx, fy, fr] = ROCKS[i];
    const cx = px + s * (fx + (noise(x * 11 + i, y * 17) - 0.5) * 0.12);
    const cy = py + s * (fy + (noise(y * 11 + i, x * 17) - 0.5) * 0.12);
    const r = s * fr;
    const p = new Path2D();
    for (let k = 0; k < 6; k++) {
      const a = (k / 6) * Math.PI * 2 + noise(i, k + x) * 0.6;
      const rr = r * (0.78 + noise(k + y, i) * 0.26);
      const vx = cx + Math.cos(a) * rr;
      const vy = cy + Math.sin(a) * rr * 0.8;
      if (k) p.lineTo(vx, vy);
      else p.moveTo(vx, vy);
    }
    p.closePath();
    ctx.save();
    ctx.translate(0, r * 0.16);
    ctx.fillStyle = 'rgba(12,10,8,.55)';
    ctx.fill(p);
    ctx.restore();
    ctx.fillStyle = '#3a332d';
    ctx.fill(p);
    // Свет сверху: верхняя часть валуна светлее.
    ctx.save();
    ctx.clip(p);
    ctx.fillStyle = 'rgba(150,135,115,.16)';
    ctx.fillRect(cx - r, cy - r, r * 2, r * 0.75);
    ctx.restore();
  }
}

// Рудник: порода с серебряными вкраплениями — клетку узнают и с отрядом
// сверху.
function drawOre(ctx, px, py, s, x, y) {
  ctx.strokeStyle = 'rgba(30,24,16,.5)';
  ctx.lineWidth = Math.max(1, s * 0.035);
  ctx.beginPath();
  ctx.moveTo(px, py + s * 0.35);
  ctx.lineTo(px + s * 0.4, py + s * 0.28);
  ctx.lineTo(px + s, py + s * 0.45);
  ctx.moveTo(px + s * 0.2, py + s);
  ctx.lineTo(px + s * 0.5, py + s * 0.7);
  ctx.lineTo(px + s, py + s * 0.78);
  ctx.stroke();
  const crystal = (cx, cy, r) => {
    ctx.moveTo(cx, cy - r * 1.3);
    ctx.lineTo(cx + r, cy);
    ctx.lineTo(cx, cy + r * 1.3);
    ctx.lineTo(cx - r, cy);
    ctx.closePath();
  };
  const specks = [];
  for (let i = 0; i < 7; i++) {
    specks.push([
      px + s * (0.08 + noise(x * 5 + i, y * 31) * 0.84),
      py + s * (0.08 + noise(y * 5 + i, x * 31) * 0.84),
      s * (0.035 + noise(i, x + y) * 0.03),
    ]);
  }
  ctx.fillStyle = 'rgba(20,16,10,.6)';
  ctx.beginPath();
  for (const [cx, cy, r] of specks) crystal(cx + 1, cy + 1, r);
  ctx.fill();
  ctx.fillStyle = '#e9eef4';
  ctx.beginPath();
  for (const [cx, cy, r] of specks) crystal(cx, cy, r);
  ctx.fill();
}

// Пашня: борозды поперёк клетки — её видно даже издали.
function drawFurrows(ctx, px, py, s) {
  for (let i = 0; i < 5; i++) {
    const fy = py + (s / 5) * (i + 0.5);
    ctx.fillStyle = 'rgba(40,34,14,.55)';
    ctx.fillRect(px, fy - s * 0.045, s, s * 0.07);
    ctx.fillStyle = 'rgba(176,156,86,.35)';
    ctx.fillRect(px, fy + s * 0.03, s, s * 0.035);
  }
}

// Ров: земляные берега сверху и снизу, между ними вода с рябью. Волны
// длиннее клетки и сдвинуты через одну — вдоль стены они идут сплошь.
function drawMoat(ctx, px, py, s, x, y, open) {
  ctx.fillStyle = '#5a4630';
  ctx.fillRect(px, py, s, s * 0.14);
  ctx.fillRect(px, py + s * 0.86, s, s * 0.14);
  ctx.fillStyle = 'rgba(0,0,0,.35)';
  ctx.fillRect(px, py + s * 0.14, s, s * 0.05);
  if (!open) return;
  ctx.fillStyle = 'rgba(8,22,30,.5)';
  ctx.fillRect(px, py + s * 0.36, s, s * 0.28);
  ctx.strokeStyle = 'rgba(150,205,225,.8)';
  ctx.lineWidth = Math.max(1, s * 0.045);
  ctx.lineCap = 'round';
  const ox = (x % 2) * s * 0.2;
  ctx.beginPath();
  for (let i = 0; i < 2; i++) {
    const wy = py + s * (0.32 + i * 0.3 + noise(x * 17 + i, y * 29) * 0.06);
    ctx.moveTo(px + s * 0.08 - ox, wy);
    ctx.quadraticCurveTo(px + s * 0.3 - ox, wy - s * 0.07, px + s * 0.52 - ox, wy);
    ctx.quadraticCurveTo(px + s * 0.74 - ox, wy + s * 0.07, px + s * 0.96 - ox, wy);
    ctx.quadraticCurveTo(px + s * 1.18 - ox, wy - s * 0.07, px + s * 1.4 - ox, wy);
  }
  ctx.stroke();
}

// Двор: мощёный камень со швами вразбежку.
function drawPaving(ctx, px, py, s, y) {
  const off = (y % 2) * 0.5;
  ctx.strokeStyle = 'rgba(30,26,20,.32)';
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(px, py + s / 2 + 0.5);
  ctx.lineTo(px + s, py + s / 2 + 0.5);
  ctx.moveTo(px + s * (0.5 + off * 0.5) + 0.5, py);
  ctx.lineTo(px + s * (0.5 + off * 0.5) + 0.5, py + s / 2);
  ctx.moveTo(px + s * (0.25 + off) + 0.5, py + s / 2);
  ctx.lineTo(px + s * (0.25 + off) + 0.5, py + s);
  ctx.stroke();
}

// Боевой ход — дощатый настил вдоль стены: глазом его не спутать с каменным
// двором, а сюда же высаживает штурмующих осадная башня.
function drawPlanks(ctx, px, py, s, x) {
  ctx.strokeStyle = 'rgba(30,20,10,.5)';
  ctx.lineWidth = Math.max(1, s * 0.03);
  ctx.beginPath();
  for (let i = 1; i < 4; i++) {
    ctx.moveTo(px, py + (s / 4) * i);
    ctx.lineTo(px + s, py + (s / 4) * i);
  }
  ctx.stroke();
  if (s < TILE_DETAIL) return;
  ctx.fillStyle = 'rgba(30,20,10,.5)';
  for (let i = 0; i < 4; i++) {
    ctx.fillRect(px + s * (((x + i) % 3) * 0.33 + 0.12), py + (s / 4) * i + s * 0.1, 2, 2);
  }
}

// Лагерь: подкрашенная земля и уголки разметки — зона найма видна, но не
// рисует рамку в каждой из шестнадцати клеток.
function drawCamp(ctx, px, py, s, x, y, side) {
  const col = SIDE_COLORS[side];
  ctx.globalAlpha = 0.16;
  ctx.fillStyle = col.body;
  ctx.fillRect(px, py, s, s);
  ctx.globalAlpha = 1;
  ctx.fillStyle = 'rgba(20,16,10,.25)';
  for (let i = 0; i < 3; i++) {
    ctx.fillRect(px + s * (0.15 + noise(x * 7 + i, y) * 0.7), py + s * (0.2 + noise(y * 7 + i, x) * 0.6), 2, 2);
  }
  const k = s * 0.2;
  const m = s * 0.14;
  ctx.globalAlpha = 0.55;
  ctx.strokeStyle = col.edge;
  ctx.lineWidth = Math.max(1.2, s * 0.05);
  ctx.lineCap = 'round';
  ctx.beginPath();
  ctx.moveTo(px + m, py + m + k);
  ctx.lineTo(px + m, py + m);
  ctx.lineTo(px + m + k, py + m);
  ctx.moveTo(px + s - m - k, py + s - m);
  ctx.lineTo(px + s - m, py + s - m);
  ctx.lineTo(px + s - m, py + s - m - k);
  ctx.stroke();
  ctx.globalAlpha = 1;
}

// drawWallTile — сегмент стены в его настоящем состоянии. Вид определяем по
// сегменту, а не по карте: деревянную створку инженеры вставляют в любой
// пролом, и в неизменной карте её нет.
function drawWallTile(ctx, px, py, s, x, kind, seg) {
  if (seg && seg.hp <= 0) {
    drawBreach(ctx, px, py, s, x);
    return;
  }
  if (seg && (seg.kind === 'gate' || seg.kind === 'woodgate')) {
    drawGate(ctx, px, py, s, Boolean(seg.open), seg.kind === 'woodgate');
    return;
  }
  // Кладка «в полкамня».
  const rows = 3;
  ctx.fillStyle = 'rgba(0,0,0,.2)';
  for (let i = 1; i < rows; i++) ctx.fillRect(px, py + (s / rows) * i, s, 1);
  for (let i = 0; i < rows; i++) {
    ctx.fillRect(px + s * ((i + x) % 2 ? 0.25 : 0.75), py + (s / rows) * i, 1, s / rows);
  }
  // Зубцы со стороны поля: стена читается стеной, а не серой полосой.
  const tower = kind === 'T' || (seg && seg.kind === 'tower');
  const n = tower ? 3 : 2;
  const mh = s * 0.2;
  const mw = s / (n * 2);
  ctx.fillStyle = 'rgba(20,16,12,.55)';
  for (let i = 0; i < n; i++) ctx.fillRect(px + mw * (i * 2 + 1), py + s - mh, mw, mh);
  ctx.fillStyle = 'rgba(255,250,235,.12)';
  ctx.fillRect(px, py, s, Math.max(1, s * 0.06));
  if (!tower) return;
  // Башня выступает из стены: тень по бокам и зубцы по краям.
  const side = Math.max(2, s * 0.1);
  ctx.fillStyle = 'rgba(20,16,12,.38)';
  ctx.fillRect(px, py, side, s);
  ctx.fillRect(px + s - side, py, side, s);
  const w = s * 0.14;
  ctx.fillStyle = 'rgba(20,16,12,.55)';
  for (const fy of [0.22, 0.5]) {
    ctx.fillRect(px, py + s * fy, w, w);
    ctx.fillRect(px + s - w, py + s * fy, w, w);
  }
}

// drawGate рисует решётку в её настоящем состоянии: опущенная перекрывает
// проём прутьями по всей высоте, поднятая собрана наверху, и под ней видна
// дорога насквозь — по ней в замок входят и свои, и чужие.
function drawGate(ctx, px, py, s, open, wood) {
  ctx.fillStyle = wood ? '#6f4f2b' : '#7d756a';
  ctx.fillRect(px, py, s, s);
  const x0 = px + s * 0.16;
  const x1 = px + s * 0.84;
  const top = py + s * 0.12;
  ctx.fillStyle = open ? '#6b5a40' : '#140f0b';
  ctx.fillRect(x0, top, x1 - x0, py + s - top);
  if (open) {
    ctx.fillStyle = 'rgba(30,22,14,.35)';
    ctx.fillRect(x0, top, x1 - x0, s * 0.12);
    ctx.fillStyle = wood ? '#3b2a16' : '#2a2119';
    ctx.beginPath();
    for (let i = 0; i < 4; i++) {
      const bx = x0 + ((x1 - x0) / 4) * (i + 0.5);
      ctx.moveTo(bx - s * 0.035, top);
      ctx.lineTo(bx + s * 0.035, top);
      ctx.lineTo(bx, top + s * 0.14);
      ctx.closePath();
    }
    ctx.fill();
    ctx.strokeStyle = 'rgba(255,240,200,.35)';
    ctx.lineWidth = Math.max(1, s * 0.04);
    ctx.setLineDash([s * 0.08, s * 0.08]);
    ctx.beginPath();
    ctx.moveTo(px + s / 2, py + s * 0.34);
    ctx.lineTo(px + s / 2, py + s);
    ctx.stroke();
    ctx.setLineDash([]);
  } else if (wood) {
    // Заколоченный пролом: доски и железные полосы.
    ctx.fillStyle = '#8a6334';
    ctx.fillRect(x0, top, x1 - x0, py + s - top);
    ctx.fillStyle = 'rgba(40,26,12,.6)';
    for (let i = 1; i < 4; i++) ctx.fillRect(x0 + ((x1 - x0) / 4) * i, top, 1, py + s - top);
    ctx.fillStyle = '#2b2118';
    ctx.fillRect(x0, py + s * 0.32, x1 - x0, s * 0.08);
    ctx.fillRect(x0, py + s * 0.7, x1 - x0, s * 0.08);
  } else {
    ctx.strokeStyle = '#3a2d20';
    ctx.lineWidth = Math.max(1.5, s * 0.07);
    ctx.beginPath();
    for (let i = 0; i < 4; i++) {
      const bx = x0 + ((x1 - x0) / 4) * (i + 0.5);
      ctx.moveTo(bx, top);
      ctx.lineTo(bx, py + s);
    }
    for (const fy of [0.42, 0.74]) {
      ctx.moveTo(x0, py + s * fy);
      ctx.lineTo(x1, py + s * fy);
    }
    ctx.stroke();
  }
  ctx.fillStyle = 'rgba(0,0,0,.35)';
  ctx.fillRect(x0 - 1, top, 1.5, py + s - top);
  ctx.fillRect(x1 - 0.5, top, 1.5, py + s - top);
}

// Пролом: обломки кладки по краям, проход посередине. Подпись не нужна —
// разрушенную стену видно и на мелкой клетке.
const RUBBLE = [
  [0.08, 0.2, 0.16], [0.18, 0.55, 0.14], [0.06, 0.85, 0.12],
  [0.9, 0.25, 0.15], [0.84, 0.62, 0.16], [0.94, 0.88, 0.11], [0.5, 0.12, 0.08], [0.6, 0.86, 0.07],
];

function drawBreach(ctx, px, py, s, x) {
  ctx.fillStyle = '#3a322b';
  ctx.fillRect(px, py, s, s);
  for (let i = 0; i < RUBBLE.length; i++) {
    const [fx, fy, fr] = RUBBLE[i];
    const r = s * fr;
    const cx = px + s * fx;
    const cy = py + s * fy;
    ctx.fillStyle = 'rgba(0,0,0,.4)';
    ctx.fillRect(cx - r, cy - r * 0.35, r * 2, r * 1.3);
    ctx.fillStyle = noise(i, x) > 0.5 ? '#8d8579' : '#776f64';
    ctx.fillRect(cx - r, cy - r * 0.6, r * 2, r * 1.2);
  }
  ctx.strokeStyle = 'rgba(232,217,160,.6)';
  ctx.lineWidth = Math.max(1, s * 0.04);
  ctx.setLineDash([s * 0.08, s * 0.08]);
  ctx.beginPath();
  ctx.moveTo(px + s / 2, py + s * 0.25);
  ctx.lineTo(px + s / 2, py + s * 0.8);
  ctx.stroke();
  ctx.setLineDash([]);
}

// Рудник и пашни обводим цветом того, кто их держит: доходные клетки видно
// всем и всегда, спорить тут не о чем — вопрос только в том, кто там стоит.
// Значок — в правом верхнем углу: середину клетки обычно занимает отряд.
function drawSpots(ctx, L, spots, kind) {
  for (const m of spots) {
    const { x, y, s } = tileRect(L, m.pos.x, m.pos.y);
    const col = SIDE_COLORS[m.side] || SIDE_COLORS.neutral;
    const neutral = m.side === 'neutral';
    ctx.save();
    if (!neutral) {
      ctx.strokeStyle = OUTLINE;
      ctx.lineWidth = Math.max(3, s * 0.12);
      ctx.strokeRect(x + 2.5, y + 2.5, s - 5, s - 5);
    }
    ctx.strokeStyle = neutral ? 'rgba(240,232,210,.7)' : col.edge;
    ctx.lineWidth = neutral ? Math.max(1.4, s * 0.045) : Math.max(2, s * 0.075);
    ctx.setLineDash(neutral ? [s * 0.12, s * 0.08] : []);
    ctx.strokeRect(x + 2.5, y + 2.5, s - 5, s - 5);
    ctx.setLineDash([]);

    const br = Math.max(4, s * 0.15);
    const bx = x + s - br - 1.5;
    const by = y + br + 1.5;
    ctx.fillStyle = OUTLINE;
    ctx.beginPath();
    ctx.arc(bx, by, br + 1, 0, Math.PI * 2);
    ctx.fill();
    if (kind === 'farm') {
      // Сноп: пашня — это припасы, а не серебро.
      ctx.fillStyle = '#d9b64c';
      ctx.beginPath();
      ctx.arc(bx, by, br, 0, Math.PI * 2);
      ctx.fill();
      ctx.strokeStyle = '#3c2d0c';
      ctx.lineWidth = Math.max(1, br * 0.2);
      ctx.lineCap = 'round';
      ctx.beginPath();
      for (const dx of [-0.42, 0, 0.42]) {
        ctx.moveTo(bx + dx * br * 0.5, by + br * 0.62);
        ctx.lineTo(bx + dx * br, by - br * 0.6);
      }
      ctx.moveTo(bx - br * 0.4, by + br * 0.08);
      ctx.lineTo(bx + br * 0.4, by + br * 0.08);
      ctx.stroke();
    } else {
      // Монета: рисуем фигурой, а не символом валюты — шрифты её коверкают.
      const g = ctx.createLinearGradient(0, by - br, 0, by + br);
      g.addColorStop(0, '#ffffff');
      g.addColorStop(1, '#a9b5c2');
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.arc(bx, by, br, 0, Math.PI * 2);
      ctx.fill();
      ctx.strokeStyle = 'rgba(60,66,74,.85)';
      ctx.lineWidth = Math.max(1, br * 0.18);
      ctx.beginPath();
      ctx.arc(bx, by, br * 0.55, 0, Math.PI * 2);
      ctx.stroke();
    }
    ctx.restore();
  }
}

// Туман войны: клетку, которую никто из наших не просматривает, затемняем.
function drawFog(ctx, L, meta, fog) {
  ctx.save();
  ctx.fillStyle = 'rgba(8,7,6,.62)';
  for (let y = 0; y < meta.rows; y++) {
    const row = fog[y] || '';
    for (let x = 0; x < meta.cols; x++) {
      if (row[x] === '1') continue;
      const { x: px, y: py, s } = tileRect(L, x, y);
      ctx.fillRect(px, py, s, s);
    }
  }
  ctx.restore();
}

// Накрытие катапульты: показываем квадрат, куда ляжет залп.
function drawAreaPreview(ctx, L, centre, radius) {
  const { x, y, s } = tileRect(L, centre.x - radius, centre.y - radius);
  const side = s * (radius * 2 + 1);
  ctx.save();
  ctx.fillStyle = 'rgba(220,120,60,.18)';
  ctx.fillRect(x, y, side, side);
  ctx.strokeStyle = 'rgba(240,160,80,.9)';
  ctx.lineWidth = 2;
  ctx.strokeRect(x + 1, y + 1, side - 2, side - 2);
  ctx.restore();
}

// Зона обстрела врага: куда опасно шагать. Рисуем штриховкой, чтобы не
// закрашивать карту и не спорить с подсветкой ходов.
function drawThreat(ctx, L, cells) {
  ctx.save();
  for (const c of cells) {
    const { x, y, s } = tileRect(L, c.x, c.y);
    ctx.fillStyle = 'rgba(200,60,50,.13)';
    ctx.fillRect(x, y, s, s);
  }
  ctx.strokeStyle = 'rgba(230,110,95,.35)';
  ctx.lineWidth = 1;
  for (const c of cells) {
    const { x, y, s } = tileRect(L, c.x, c.y);
    ctx.beginPath();
    ctx.moveTo(x, y + s);
    ctx.lineTo(x + s, y);
    ctx.stroke();
  }
  ctx.restore();
}

// Зона поражения выбранного юнита — рамка по краю его дальности.
function drawReach(ctx, L, reach) {
  const { x, y, s } = tileRect(L, reach.pos.x - reach.range, reach.pos.y - reach.range);
  const side = s * (reach.range * 2 + 1);
  ctx.save();
  ctx.strokeStyle = 'rgba(255,225,150,.75)';
  ctx.lineWidth = 2;
  ctx.setLineDash([6, 4]);
  ctx.strokeRect(x + 1, y + 1, side - 2, side - 2);
  ctx.setLineDash([]);
  ctx.restore();
}

function drawHighlights(ctx, L, ui) {
  const paint = (list, fill, stroke) => {
    for (const p of list || []) {
      const { x, y, s } = tileRect(L, p.x, p.y);
      ctx.fillStyle = fill;
      ctx.fillRect(x + 2, y + 2, s - 4, s - 4);
      if (stroke) {
        ctx.strokeStyle = stroke;
        ctx.lineWidth = 2;
        ctx.strokeRect(x + 2, y + 2, s - 4, s - 4);
      }
    }
  };
  if (ui.mode === 'deploy') paint(ui.zone, 'rgba(230,200,120,.18)', 'rgba(230,200,120,.8)');
  if (ui.mode === 'ability') paint(ui.segments, 'rgba(120,180,230,.18)', 'rgba(150,200,240,.85)');
  if (ui.mode === 'build') paint(ui.buildSpots, 'rgba(210,170,100,.2)', 'rgba(230,190,120,.9)');
  // Прясло частокола: клетка под курсором и те, что встанут вместе с ней, —
  // одной яркой линией. Крайние колья лежат вне buildSpots (они в двух
  // клетках от инженера), так что без этого о них не догадаться.
  if (ui.mode === 'build' && ui.fence) paint(ui.fence, 'rgba(235,195,120,.42)', 'rgba(250,220,155,1)');
  if (ui.mode === 'construct') paint(ui.court, 'rgba(120,180,230,.18)', 'rgba(150,200,240,.85)');
  if (ui.mode === 'heal') paint(ui.healSpots, 'rgba(120,200,120,.2)', 'rgba(150,220,150,.9)');
  if (ui.mode === 'charge') paint(ui.chargeSpots, 'rgba(220,90,60,.25)', 'rgba(240,130,90,.95)');
  if (ui.mode === 'unload') paint(ui.unloadSpots, 'rgba(230,200,120,.22)', 'rgba(240,210,140,.95)');
  if (ui.mode === 'select') {
    paint(ui.moves, 'rgba(255,255,255,.14)', 'rgba(255,255,255,.35)');
    paint(ui.targets, 'rgba(200,60,50,.22)', 'rgba(240,120,100,.9)');
  }
  if (ui.hover) {
    const { x, y, s } = tileRect(L, ui.hover.x, ui.hover.y);
    ctx.strokeStyle = 'rgba(255,255,255,.55)';
    ctx.lineWidth = 2;
    ctx.strokeRect(x + 1, y + 1, s - 2, s - 2);
  }
  // Осмотренная клетка — золотым пунктиром: видно, о какой из них карточка
  // в панели, и не путается с подсветкой ходов.
  if (ui.inspect) {
    const { x, y, s } = tileRect(L, ui.inspect.x, ui.inspect.y);
    ctx.save();
    ctx.strokeStyle = 'rgba(216,169,63,.95)';
    ctx.lineWidth = 2;
    ctx.setLineDash([5, 3]);
    ctx.strokeRect(x + 2, y + 2, s - 4, s - 4);
    ctx.restore();
  }
}

// Тёмная кайма вокруг фишек, построек и отметок: отделяет их и от травы, и
// от цвета стороны — зелёная фишка на зелёном поле без неё теряется.
const OUTLINE = 'rgba(16,11,7,.92)';
const INK = '#fbf8f1'; // значок на фишке
const PARCH = '#efe2c2'; // значок на постройке
const ROYAL_INK = '#f6d77c'; // корона короля

// Ловушку видит только тот, кто её поставил, — сервер чужие и не присылает.
// Капкан с зубьями в пунктирном круге: пунктир и значит «спрятано».
function drawTrap(ctx, L, pos) {
  const { x, y, s } = tileRect(L, pos.x, pos.y);
  const cx = x + s / 2;
  const cy = y + s / 2;
  const r = s * 0.3;
  ctx.save();
  ctx.lineCap = 'round';
  ctx.lineJoin = 'round';
  ctx.setLineDash([Math.max(2, s * 0.08), Math.max(2, s * 0.06)]);
  ctx.strokeStyle = 'rgba(240,200,110,.9)';
  ctx.lineWidth = Math.max(1.2, s * 0.04);
  ctx.beginPath();
  ctx.arc(cx, cy, r * 1.32, 0, Math.PI * 2);
  ctx.stroke();
  ctx.setLineDash([]);
  // Челюсти: сверху четыре зуба, снизу три — входят друг между другом.
  for (const [dir, n] of [[-1, 4], [1, 3]]) {
    const p = new Path2D();
    const y0 = cy + dir * r * 0.1;
    p.moveTo(cx - r, y0);
    p.quadraticCurveTo(cx - r, cy + dir * r, cx, cy + dir * r);
    p.quadraticCurveTo(cx + r, cy + dir * r, cx + r, y0);
    const step = (2 * r) / n;
    for (let i = 0; i < n; i++) {
      const x1 = cx + r - step * i;
      p.lineTo(x1 - step / 2, y0 - dir * r * 0.48);
      p.lineTo(x1 - step, y0);
    }
    p.closePath();
    ctx.strokeStyle = OUTLINE;
    ctx.lineWidth = Math.max(2.4, s * 0.09);
    ctx.stroke(p);
    ctx.fillStyle = '#e2b866';
    ctx.fill(p);
  }
  ctx.restore();
}

// Постройка лежит на земле: квадрат без тени, тёмная кромка и рамка цвета
// хозяина. Значок — залитый силуэт: на клетке в 30 пикселей тонкий контур
// расплывается, а пятно читается.
function drawBuilding(ctx, L, b) {
  const { x, y, s } = tileRect(L, b.pos.x, b.pos.y);
  // Фашины — не постройка на клетке, а настил поверх воды: рисуем их
  // плоско, без короба, иначе переправа выглядит стеной.
  if (b.kind === 'fascine') {
    drawFascines(ctx, x, y, s, b);
    return;
  }
  // Мост — тоже настил, а не постройка: по нему идут, и короб с гербом
  // превратил бы переправу в сарай посреди рва.
  if (b.kind === 'bridge') {
    drawBridge(ctx, x, y, s, b);
    return;
  }
  const col = SIDE_COLORS[b.side] || SIDE_COLORS.neutral;
  // Наблюдательный пункт обводим цветом того, кто его держит: по карте сразу
  // видно, за кем какая точка, и где ничья. Кто-то внутри — рамка золотая.
  const holder = b.hold && SIDE_COLORS[b.hold] ? SIDE_COLORS[b.hold] : col;
  const pad = s * 0.07;
  const w = s - pad * 2;
  ctx.save();
  const plate = new Path2D();
  roundPath(plate, x + pad, y + pad, w, w, s * 0.14);
  ctx.strokeStyle = OUTLINE;
  ctx.lineWidth = Math.max(3, s * 0.13);
  ctx.stroke(plate);
  const grad = ctx.createLinearGradient(0, y, 0, y + s);
  grad.addColorStop(0, '#433629');
  grad.addColorStop(1, '#2a2119');
  ctx.fillStyle = grad;
  ctx.fill(plate);
  ctx.strokeStyle = b.held ? '#ffe08a' : holder.edge;
  ctx.lineWidth = Math.max(1.5, s * (b.held ? 0.085 : 0.06));
  ctx.stroke(plate);

  stampGlyph(ctx, BUILDING_GLYPHS, b.kind, x + s / 2, y + s * 0.51, s * 0.64, PARCH);

  // Временная постройка показывает, сколько дней ей осталось; последние два
  // — на красной плашке.
  if (b.life) {
    const fs = Math.max(8, Math.round(s * 0.26));
    ctx.font = `800 ${fs}px system-ui, sans-serif`;
    const t = String(b.life);
    const bh = fs * 1.15;
    const bw = Math.max(bh, ctx.measureText(t).width + fs * 0.5);
    ctx.fillStyle = b.life <= 2 ? '#b8432f' : 'rgba(16,11,7,.9)';
    ctx.beginPath();
    roundPath(ctx, x + s - bw - 1, y + 1, bw, bh, bh / 2);
    ctx.fill();
    ctx.fillStyle = '#ffe9a8';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(t, x + s - bw / 2 - 1, y + 1 + bh / 2 + 0.5);
  }

  // Прочность постройки — такая же полоса, как у отрядов.
  const ratio = Math.max(0, b.hp) / b.maxHp;
  if (ratio < 1) {
    const bh = Math.max(2.5, s * 0.08);
    drawBar(ctx, x + s / 2, y + s - pad - bh - 1, w - 4, bh, ratio);
  }
  ctx.restore();
}

// drawFascines — настил из вязанок хвороста поверх воды. Рисуется не как
// постройка: это мостки, по которым идут, и выглядеть они должны переправой.
// Перевязь — цвета того, кто засыпал ров. Полоса прочности нужна: настил
// разбивают, и тогда вода возвращается.
function drawFascines(ctx, x, y, s, b) {
  const col = SIDE_COLORS[b.side] || SIDE_COLORS.neutral;
  ctx.save();
  for (const fy of [0.27, 0.5, 0.73]) {
    const cy = y + s * fy;
    const h = s * 0.2;
    ctx.fillStyle = OUTLINE;
    ctx.beginPath();
    roundPath(ctx, x + s * 0.02, cy - h / 2 - 1, s * 0.96, h + 2, h / 2);
    ctx.fill();
    const g = ctx.createLinearGradient(0, cy - h / 2, 0, cy + h / 2);
    g.addColorStop(0, '#c2a566');
    g.addColorStop(1, '#7d6435');
    ctx.fillStyle = g;
    ctx.beginPath();
    roundPath(ctx, x + s * 0.04, cy - h / 2, s * 0.92, h, h / 2);
    ctx.fill();
    ctx.strokeStyle = 'rgba(60,44,20,.7)';
    ctx.lineWidth = Math.max(1, s * 0.025);
    ctx.beginPath();
    ctx.moveTo(x + s * 0.1, cy - h * 0.1);
    ctx.lineTo(x + s * 0.9, cy - h * 0.15);
    ctx.moveTo(x + s * 0.12, cy + h * 0.22);
    ctx.lineTo(x + s * 0.88, cy + h * 0.18);
    ctx.stroke();
    ctx.fillStyle = col.edge;
    for (const fx of [0.3, 0.7]) ctx.fillRect(x + s * fx - s * 0.03, cy - h / 2, s * 0.06, h);
  }
  const ratio = Math.max(0, b.hp) / b.maxHp;
  if (ratio < 1) {
    const bh = Math.max(2.5, s * 0.08);
    drawBar(ctx, x + s / 2, y + s - bh - 2, s - 6, bh, ratio);
  }
  ctx.restore();
}

// drawBridge — старый мост напротив ворот. Доски лежат поперёк хода, у
// берегов они тёмные от воды, середина вытерта ногами, по краям жерди на
// столбиках. Ничей, поэтому никакого цвета стороны: это часть долины, а не
// чья-то постройка.
const bridgeCells = new Set();

function drawBridge(ctx, x, y, s, b) {
  ctx.save();
  ctx.beginPath();
  ctx.rect(x, y, s, s);
  ctx.clip();

  // Тень на воду: мягкая, вниз и вправо, как у всего остального на поле.
  ctx.fillStyle = 'rgba(8,20,28,.55)';
  ctx.fillRect(x + s * 0.04, y + s * 0.06, s, s);

  const planks = 6;
  const h = s / planks;
  for (let i = 0; i < planks; i++) {
    const py = y + i * h;
    // Крайние доски мокрые: мост стоит в воде, и у берегов это видно.
    const wet = i === 0 || i === planks - 1 ? -14 : 0;
    const tone = [0, 8, -6, 4, -9, 6][i % 6] + wet;
    ctx.fillStyle = shade('#7a5a33', tone);
    ctx.fillRect(x, py, s, h + 0.5);
    ctx.fillStyle = 'rgba(190,150,96,.28)';
    ctx.fillRect(x, py, s, Math.max(1, h * 0.12));
    ctx.fillStyle = 'rgba(42,28,12,.55)';
    ctx.fillRect(x, py + h - Math.max(1, h * 0.12), s, Math.max(1, h * 0.12));
  }

  // Тропа посередине: по мосту ходят, и доски там светлее.
  const path = ctx.createLinearGradient(x + s * 0.2, 0, x + s * 0.8, 0);
  path.addColorStop(0, 'rgba(198,160,104,0)');
  path.addColorStop(0.5, 'rgba(198,160,104,.16)');
  path.addColorStop(1, 'rgba(198,160,104,0)');
  ctx.fillStyle = path;
  ctx.fillRect(x, y, s, s);

  // Гвозди по концам досок.
  ctx.fillStyle = 'rgba(38,34,28,.8)';
  const r = Math.max(0.8, s * 0.022);
  for (let i = 0; i < planks; i++) {
    const cy = y + (i + 0.5) * h;
    for (const fx of [0.12, 0.88]) {
      ctx.beginPath();
      ctx.arc(x + s * fx, cy, r, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  // Перила: жердь вдоль каждого края и столбики. У края, которым мост
  // примыкает ко второй своей половине, перил нет.
  const w = Math.max(1.5, s * 0.055);
  const sides = [];
  if (!bridgeCells.has(`${b.pos.x - 1},${b.pos.y}`)) sides.push(0.035);
  if (!bridgeCells.has(`${b.pos.x + 1},${b.pos.y}`)) sides.push(0.965);
  for (const fx of sides) {
    const px = x + s * fx;
    ctx.fillStyle = 'rgba(46,32,16,.85)';
    ctx.fillRect(px - w / 2 + w * 0.7, y, w, s);
    ctx.fillStyle = '#8a6333';
    ctx.fillRect(px - w / 2, y, w, s);
    ctx.fillStyle = '#4a3419';
    for (const fy of [0.08, 0.52]) {
      ctx.fillRect(px - w, y + s * fy, w * 2, s * 0.14);
    }
  }

  const ratio = Math.max(0, b.hp) / b.maxHp;
  if (ratio < 1) {
    const bh = Math.max(2.5, s * 0.08);
    drawBar(ctx, x + s / 2, y + s - bh - 2, s - 6, bh, ratio);
  }
  ctx.restore();
}

// ------------------------------------------------------------------ фишки
//
// Фишка отвечает на три вопроса — в том порядке, в каком их задаёт игрок:
//   чей отряд — цвет;
//   чем он опасен — форма: щит рубится вплотную, круг стреляет, квадратная
//     плита — машина, ромб — специалист, который сам почти не воюет;
//   кто это — значок внутри.
// Засечки по краю — прицел: отряд бьёт по людям издали, будь то стрелок или
// машина. Серебряный обод — элита, золотой — король.

// familyOf — роль отряда по его характеристикам, а не по списку имён: новый
// тип отряда сразу получит правильную фишку.
function familyOf(spec) {
  if (!spec) return 'melee';
  if (spec.engine || spec.machine) return 'machine';
  if (spec.healer || spec.builder || spec.charges || !(spec.damage > 0)) return 'support';
  if (spec.range > 1) return 'ranged';
  return 'melee';
}

function shootsFar(spec) {
  return Boolean(spec && spec.range > 1 && spec.damage > 0 && !spec.noUnits);
}

// Радиус фишки в долях клетки. Щит острым низом теряет площадь, поэтому он
// чуть крупнее круга того же веса; элите место под обод.
const TOKEN_R = { melee: 0.39, ranged: 0.37, machine: 0.385, support: 0.37 };

// Контуры обходим по часовой стрелке: засечки добавляются в тот же Path2D и
// при правиле nonzero сливаются с телом в один силуэт с общей каймой.
function shieldPath(p, cx, cy, r) {
  const w = r * 0.9;
  const top = cy - r * 0.9;
  const k = r * 0.2;
  const shoulder = cy + r * 0.12;
  p.moveTo(cx - w + k, top);
  p.lineTo(cx + w - k, top);
  p.arcTo(cx + w, top, cx + w, top + k, k);
  p.lineTo(cx + w, shoulder);
  p.quadraticCurveTo(cx + w, cy + r * 0.74, cx, cy + r * 1.08);
  p.quadraticCurveTo(cx - w, cy + r * 0.74, cx - w, shoulder);
  p.lineTo(cx - w, top + k);
  p.arcTo(cx - w, top, cx - w + k, top, k);
  p.closePath();
}

// Плита с небольшой фаской: окованный щит машины, но с первого взгляда —
// квадрат. Большая фаска на мелкой клетке делала её кругом.
const PLATE_HALF = 0.88;
const PLATE_CUT = 0.17;

function platePath(p, cx, cy, r) {
  const h = r * PLATE_HALF;
  const c = r * PLATE_CUT;
  p.moveTo(cx - h + c, cy - h);
  p.lineTo(cx + h - c, cy - h);
  p.lineTo(cx + h, cy - h + c);
  p.lineTo(cx + h, cy + h - c);
  p.lineTo(cx + h - c, cy + h);
  p.lineTo(cx - h + c, cy + h);
  p.lineTo(cx - h, cy + h - c);
  p.lineTo(cx - h, cy - h + c);
  p.closePath();
}

// Ромб со скруглёнными вершинами. Начинаем с середины ребра: от вершины
// closePath оставил бы на углу заусенец.
function diamondPath(p, cx, cy, r) {
  const d = r * 1.2;
  const k = r * 0.2;
  p.moveTo(cx + d / 2, cy - d / 2);
  p.arcTo(cx + d, cy, cx, cy + d, k);
  p.arcTo(cx, cy + d, cx - d, cy, k);
  p.arcTo(cx - d, cy, cx, cy - d, k);
  p.arcTo(cx, cy - d, cx + d, cy, k);
  p.closePath();
}

// Засечки прицела — четыре прямоугольника наружу из середин сторон.
function addTicks(p, family, cx, cy, r) {
  const edge = family === 'machine' ? r * PLATE_HALF : r * 0.98;
  const r0 = edge * 0.7;
  const r1 = edge + r * 0.26;
  const w = r * 0.12;
  for (let i = 0; i < 4; i++) {
    const a = -Math.PI / 2 + (i * Math.PI) / 2;
    const ux = Math.cos(a);
    const uy = Math.sin(a);
    p.moveTo(cx + ux * r0 + uy * w, cy + uy * r0 - ux * w);
    p.lineTo(cx + ux * r1 + uy * w, cy + uy * r1 - ux * w);
    p.lineTo(cx + ux * r1 - uy * w, cy + uy * r1 + ux * w);
    p.lineTo(cx + ux * r0 - uy * w, cy + uy * r0 + ux * w);
    p.closePath();
  }
}

function tokenShape(family, cx, cy, r, ticks) {
  const p = new Path2D();
  if (family === 'melee') shieldPath(p, cx, cy, r);
  else if (family === 'ranged') p.arc(cx, cy, r * 0.98, 0, Math.PI * 2);
  else if (family === 'machine') platePath(p, cx, cy, r);
  else diamondPath(p, cx, cy, r);
  if (ticks) addTicks(p, family, cx, cy, r);
  return p;
}

// Сколько места под значок внутри каждой формы и где его центр: у щита
// острый низ, и значок сидит чуть выше середины.
const GLYPH_BOX = {
  melee: { size: 1.62, dy: -0.06 },
  ranged: { size: 1.5, dy: 0 },
  machine: { size: 1.56, dy: 0 },
  support: { size: 1.4, dy: 0 },
};

// Металл обода — вертикальный градиент с бликом посередине: серебро
// элиты (её нанимают за серебро с рудника) и золото короля.
function metal(ctx, cy, r, gold) {
  const g = ctx.createLinearGradient(0, cy - r * 1.2, 0, cy + r * 1.2);
  const stops = gold
    ? ['#fff3c2', '#e6b84a', '#c9962c', '#f5d98a']
    : ['#ffffff', '#d5dde6', '#a9b5c2', '#eef2f7'];
  g.addColorStop(0, stops[0]);
  g.addColorStop(0.45, stops[1]);
  g.addColorStop(0.55, stops[2]);
  g.addColorStop(1, stops[3]);
  return g;
}

// drawToken — сама фишка без полосок и звёзд: её же рисуют лавка и панель.
function drawToken(ctx, cx, cy, r, side, spec, o) {
  const col = SIDE_COLORS[side] || SIDE_COLORS.neutral;
  const family = familyOf(spec);
  const shape = tokenShape(family, cx, cy, r, shootsFar(spec));
  const line = Math.max(1, r * 0.1);
  ctx.save();
  ctx.lineJoin = 'round';
  // Затаившийся в кустах свой отряд полупрозрачен и обведён пунктиром:
  // противник его пока не видит, и это главное, что нужно знать перед ударом.
  if (o.lurking) ctx.globalAlpha = 0.55;

  if (o.shadow !== false) {
    ctx.fillStyle = 'rgba(0,0,0,.38)';
    ctx.beginPath();
    ctx.ellipse(cx, cy + r * 0.82, r * 0.92, r * 0.34, 0, 0, Math.PI * 2);
    ctx.fill();
  }

  if (o.selected) {
    ctx.save();
    ctx.shadowColor = 'rgba(255,225,140,.95)';
    ctx.shadowBlur = Math.max(4, r * 0.6);
    ctx.strokeStyle = '#ffe9a8';
    ctx.lineWidth = line * 2 + Math.max(4, r * 0.42);
    ctx.stroke(shape);
    ctx.restore();
  }

  if (o.royal || o.elite) {
    // Обод: тёмный край — металл — тёмная нить — тело. Тёмные края
    // отделяют металл и от травы, и от цвета стороны.
    const band = Math.max(1.8, r * 0.2);
    const inner = Math.max(0.8, line * 0.7);
    ctx.strokeStyle = OUTLINE;
    ctx.lineWidth = (line + band + inner) * 2;
    ctx.stroke(shape);
    ctx.strokeStyle = metal(ctx, cy, r, o.royal);
    ctx.lineWidth = (band + inner) * 2;
    ctx.stroke(shape);
    ctx.strokeStyle = OUTLINE;
    ctx.lineWidth = inner * 2;
    ctx.stroke(shape);
  } else {
    ctx.strokeStyle = OUTLINE;
    ctx.lineWidth = line * 2;
    ctx.stroke(shape);
  }

  const grad = ctx.createLinearGradient(0, cy - r, 0, cy + r);
  grad.addColorStop(0, shade(col.body, 26));
  grad.addColorStop(1, shade(col.body, -20));
  ctx.fillStyle = grad;
  ctx.fill(shape);

  // Фаска: светлая кромка поверху — фишка выглядит вещью, а не пятном.
  ctx.save();
  ctx.clip(shape);
  ctx.translate(0, Math.max(1, r * 0.1));
  ctx.strokeStyle = 'rgba(255,255,255,.32)';
  ctx.lineWidth = Math.max(1.2, r * 0.14);
  ctx.stroke(shape);
  ctx.restore();

  if (o.lurking) {
    ctx.globalAlpha = 1;
    ctx.setLineDash([Math.max(2, r * 0.3), Math.max(2, r * 0.22)]);
    ctx.strokeStyle = 'rgba(255,255,255,.85)';
    ctx.lineWidth = Math.max(1, r * 0.09);
    ctx.stroke(shape);
    ctx.setLineDash([]);
    ctx.globalAlpha = 0.75;
  }

  const box = GLYPH_BOX[family];
  drawGlyph(ctx, o.type, cx, cy + box.dy * r, box.size * r, o.royal ? ROYAL_INK : INK);
  ctx.restore();
}

function drawUnit(ctx, L, u, ui, spec) {
  const { x: px, y: py, s } = tileRect(L, u.pos.x, u.pos.y);
  const family = familyOf(spec);
  const elite = Boolean(spec && spec.elite);
  const cx = px + s / 2;
  // Король крупнее всех: единственная фигура, которую нельзя терять, и
  // искать её на карте глазами не должно быть работой.
  const cy = py + s * (u.royal ? 0.48 : family === 'melee' ? 0.45 : 0.46);
  const r = s * (u.royal ? 0.41 : TOKEN_R[family] - (elite ? 0.025 : 0));

  // Королю — золотое гало: его видно поверх любой свалки во дворе.
  if (u.royal) {
    ctx.save();
    ctx.strokeStyle = 'rgba(242,205,107,.8)';
    ctx.lineWidth = Math.max(1.5, s * 0.05);
    ctx.setLineDash([s * 0.12, s * 0.07]);
    ctx.beginPath();
    ctx.arc(cx, cy, r * 1.42, 0, Math.PI * 2);
    ctx.stroke();
    ctx.restore();
  }

  drawToken(ctx, cx, cy, r, u.side, spec, {
    type: u.type,
    elite,
    royal: Boolean(u.royal),
    selected: u.id === ui.selectedId,
    lurking: Boolean(u.lurking),
  });

  // Полоса здоровья — только у раненых, как у построек: целое войско не
  // рябит чёрточками, а кого добивать, видно сразу.
  const ratio = Math.max(0, u.hp) / u.maxHp;
  if (ratio < 1) drawBar(ctx, cx, py + s * 0.885, s * 0.62, Math.max(2.5, s * 0.085), ratio);

  // Звёзды ветерана: отряд, добивший чужие, бьёт и держит лучше. Рисуем
  // прямо на фигурке — выбирать, кем добивать, приходится каждый ход.
  if (u.stars > 0 && s >= 16) {
    const rs = Math.max(2.2, s * 0.075);
    for (let i = 0; i < u.stars; i++) drawStar(ctx, px + s * 0.1 + rs + i * rs * 2.2, py + s * 0.04 + rs, rs);
  }

  // Осадная башня: сколько людей внутри (только хозяину — чужой экипаж не
  // виден) и опущен ли трап. И то и другое решает, бить по ней или нет.
  if (u.capacity && s >= 18) {
    if (u.mine) drawPill(ctx, px + s - 2, py + 2, `${(u.cargo || []).length}/${u.capacity}`, s);
    if (u.ramp) {
      ctx.save();
      ctx.lineCap = 'round';
      ctx.strokeStyle = OUTLINE;
      ctx.lineWidth = Math.max(3.5, s * 0.13);
      ctx.beginPath();
      ctx.moveTo(cx, cy - r * 0.6);
      ctx.lineTo(cx, py - s * 0.32);
      ctx.stroke();
      ctx.strokeStyle = '#e9c27a';
      ctx.lineWidth = Math.max(2, s * 0.08);
      ctx.stroke();
      ctx.restore();
    }
  }

  // Заряд подрывника: пока он при нём, стена под угрозой — это стоит видеть
  // прямо на карте, а не в панели. Огонёк в правом верхнем углу.
  if (u.charges > 0 && s >= 18) {
    const cr = Math.max(2.6, s * 0.08);
    const bx = px + s - cr - 2;
    const by = py + cr + 2;
    ctx.save();
    ctx.fillStyle = OUTLINE;
    ctx.beginPath();
    ctx.arc(bx, by, cr + 1.2, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = '#ff9a3c';
    ctx.beginPath();
    ctx.arc(bx, by, cr, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = '#ffe2a3';
    ctx.beginPath();
    ctx.arc(bx - cr * 0.3, by - cr * 0.3, cr * 0.4, 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
  }
}

// drawBar — полоса прочности: тёмная подложка и цвет по остатку.
function drawBar(ctx, cx, y, w, h, ratio) {
  ctx.fillStyle = 'rgba(10,8,6,.78)';
  ctx.fillRect(cx - w / 2 - 1, y - 1, w + 2, h + 2);
  ctx.fillStyle = ratio > 0.6 ? '#8fd16a' : ratio > 0.3 ? '#e3b452' : '#e06a56';
  ctx.fillRect(cx - w / 2, y, w * ratio, h);
}

function drawPill(ctx, right, top, text, s) {
  ctx.save();
  const fs = Math.max(9, Math.floor(s * 0.24));
  ctx.font = `700 ${fs}px system-ui, sans-serif`;
  const w = ctx.measureText(text).width + fs * 0.6;
  const h = fs * 1.25;
  ctx.fillStyle = 'rgba(16,11,7,.88)';
  ctx.beginPath();
  roundPath(ctx, right - w, top, w, h, h / 2);
  ctx.fill();
  ctx.fillStyle = '#ffe9a8';
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.fillText(text, right - w / 2, top + h / 2 + 0.5);
  ctx.restore();
}

// drawStar — пятиконечная звёздочка ветерана в тёмной кайме. Рисуем
// фигурой, а не символом: шрифтовая «★» на мелкой клетке — клякса.
function drawStar(ctx, cx, cy, r) {
  const p = new Path2D();
  for (let i = 0; i < 10; i++) {
    const a = -Math.PI / 2 + (i * Math.PI) / 5;
    const k = i % 2 ? r * 0.47 : r;
    if (i) p.lineTo(cx + Math.cos(a) * k, cy + Math.sin(a) * k);
    else p.moveTo(cx + Math.cos(a) * k, cy + Math.sin(a) * k);
  }
  p.closePath();
  ctx.save();
  ctx.lineJoin = 'round';
  ctx.strokeStyle = OUTLINE;
  ctx.lineWidth = Math.max(1.6, r * 0.55);
  ctx.stroke(p);
  ctx.fillStyle = '#ffd36a';
  ctx.fill(p);
  ctx.restore();
}

// ----------------------------------------------------------------- значки
//
// Иконки рисуем кодом: ни одного внешнего файла, бинарник остаётся один.
// Силуэт описан путями SVG в квадрате ±50 — так его проще править, а Path2D
// понимает эту запись сам. Значок — залитое пятно, а не контур: на клетке в
// 30 пикселей линия расплывается, а пятно читается. Детали вырезаны
// насквозь (cut), и сквозь прорезь видно тело фишки.
//
// Каждый значок — то, что первым приходит в голову при названии отряда, и
// похожих пар быть не должно: крест остался только у лекаря (храмовники
// носят топфхельм), лук — у разведчика, арбалет — у арбалетчиков, а
// баллиста отличается от них треногой и квадратной плитой машины.

const pathCache = new Map();
function svgPath(d) {
  let p = pathCache.get(d);
  if (!p) {
    p = new Path2D(d);
    pathCache.set(d, p);
  }
  return p;
}

// pen — кисть для описания значков. Толщина линий — в единицах значка, но
// не тоньше пикселя устройства: иначе на мелкой клетке линия пропадает.
function pen(ctx, px) {
  const unit = 100 / px;
  const min = (w) => Math.max(w, unit * 1.15);
  const mode = (cut) => {
    ctx.globalCompositeOperation = cut ? 'destination-out' : 'source-over';
  };
  const box = (x0, y0, x1, y1, r) => {
    const p = new Path2D();
    const k = Math.min(r || 0, (x1 - x0) / 2, (y1 - y0) / 2);
    if (k > 0) roundPath(p, x0, y0, x1 - x0, y1 - y0, k);
    else p.rect(x0, y0, x1 - x0, y1 - y0);
    return p;
  };
  const disc = (x, y, r) => {
    const p = new Path2D();
    p.arc(x, y, r, 0, Math.PI * 2);
    return p;
  };
  return {
    fine: px >= 24, // прорези и мелочь — только там, где их видно
    fill: (d) => (mode(false), ctx.fill(svgPath(d))),
    cut: (d) => (mode(true), ctx.fill(svgPath(d))),
    stroke: (d, w) => (mode(false), (ctx.lineWidth = min(w)), ctx.stroke(svgPath(d))),
    cutStroke: (d, w) => (mode(true), (ctx.lineWidth = min(w)), ctx.stroke(svgPath(d))),
    rect: (x0, y0, x1, y1, r) => (mode(false), ctx.fill(box(x0, y0, x1, y1, r))),
    cutRect: (x0, y0, x1, y1, r) => (mode(true), ctx.fill(box(x0, y0, x1, y1, r))),
    circle: (x, y, r) => (mode(false), ctx.fill(disc(x, y, r))),
    cutCircle: (x, y, r) => (mode(true), ctx.fill(disc(x, y, r))),
    star: (x, y, R, r, n) => {
      const p = new Path2D();
      for (let i = 0; i < n * 2; i++) {
        const a = -Math.PI / 2 + (i * Math.PI) / n;
        const k = i % 2 ? r : R;
        if (i) p.lineTo(x + Math.cos(a) * k, y + Math.sin(a) * k);
        else p.moveTo(x + Math.cos(a) * k, y + Math.sin(a) * k);
      }
      p.closePath();
      mode(false);
      ctx.fill(p);
    },
    save: () => ctx.save(),
    restore: () => ctx.restore(),
    rotate: (a) => ctx.rotate(a),
    translate: (x, y) => ctx.translate(x, y),
  };
}

const UNIT_GLYPHS = {
  // --- ближний бой: щит
  infantry(g) { // меч остриём вверх — главный боец осады
    g.fill('M0,-49 L8.5,-37 L8.5,11 L-8.5,11 L-8.5,-37 Z');
    g.rect(-25, 10, 25, 20.5, 4);
    g.rect(-5.5, 19, 5.5, 36, 2);
    g.circle(0, 41.5, 8);
    if (g.fine) g.cutRect(-2, -31, 2, 5, 2); // дол
  },
  garrison(g) { // алебарда: стража на стене
    g.rect(1.5, -36, 9.5, 49, 3);
    g.fill('M5.5,-50 L11.5,-34 L-0.5,-34 Z'); // пика
    g.fill('M2,-34 C-8,-43 -25,-46 -35,-40 C-40,-26 -39,-12 -33,0 C-23,-4 -10,-8 2,-10 Z'); // лезвие
    g.fill('M9,-28 L29,-21 L9,-15 Z'); // клюв на обухе
    if (g.fine) g.cutStroke('M-27,-35 C-31,-24 -30,-14 -27,-7', 3);
  },
  militia(g) { // вилы — оружие тех, кто пахал ещё вчера
    g.rect(-4.5, -10, 4.5, 49, 2.5);
    g.stroke('M-22,-47 L-22,-24 Q-22,-7 0,-7 Q22,-7 22,-24 L22,-47', 8.5);
    g.stroke('M0,-8 L0,-47', 8.5);
  },
  templar(g) { // топфхельм: королевская стража держит, а не рубит
    g.fill('M-31,-30 Q-31,-47 0,-47 Q31,-47 31,-30 L31,26 Q31,40 15,46 L-15,46 Q-31,40 -31,26 Z');
    g.cutRect(-25, -14, 25, -5, 2); // смотровая щель
    g.cutRect(-4, -7, 4, 30, 2);
    if (g.fine) { // крест на лбу
      g.cutRect(-2.6, -39, 2.6, -21, 1);
      g.cutRect(-9, -32.5, 9, -27.5, 1);
    }
  },
  knight(g) { // шахматный конь: рыцарь узнаётся с одного взгляда
    g.fill('M-25,33 C-26,17 -16,7 -5,1 L-20,4 C-31,7 -40,0 -39,-9 L-34,-16 C-26,-29 -13,-38 1,-41 L8,-51 L14,-39 C30,-31 37,-14 35,6 C34,17 31,25 30,33 Z');
    g.rect(-33, 31, 36, 47, 4); // подставка
    g.cutStroke('M-34,30.5 L37,30.5', 3.6);
    if (g.fine) {
      g.cutCircle(-15, -22, 3.8); // глаз
      g.cutStroke('M15,-30 Q27,-14 26,10', 3.2); // грива
    }
  },
  king(g) { // корона: приз осады должен быть виден издали
    g.fill('M-43,11 L-43,-23 L-21.5,-3 L0,-31 L21.5,-3 L43,-23 L43,11 Z');
    g.circle(-43, -28, 7);
    g.circle(0, -37, 7.5);
    g.circle(43, -28, 7);
    g.rect(-43, 16, 43, 33, 3);
    if (g.fine) for (const x of [-21.5, 0, 21.5]) g.cutCircle(x, 24.5, 4.2);
  },

  // --- дальний бой: круг с засечками
  scout(g) { // лук со стрелой: лёгкий стрелок, который видит дальше всех
    g.rotate(-Math.PI / 4);
    g.stroke('M-2,-44 Q-48,0 -2,44', 8);
    g.stroke('M-2,-43 L-2,43', 3); // тетива
    g.stroke('M-20,0 L34,0', 6);
    g.fill('M50,0 L31,-10.5 L31,10.5 Z');
    g.fill('M-12,0 L-22,-11 L-31,-11 L-22,0 L-31,11 L-22,11 Z'); // оперение
  },
  crossbow(g) { // арбалет: дуга поперёк ложа, приклад и спуск
    g.stroke('M-44,-11 Q0,-47 44,-11', 8.5);
    g.stroke('M-43,-11 L0,1 L43,-11', 3); // тетива, взведённая
    g.fill('M-5.5,-38 L5.5,-38 L7.5,17 L16,47 L-16,47 L-7.5,17 Z');
    g.fill('M0,-51 L-9,-36 L9,-36 Z'); // болт
    g.stroke('M8,8 L18,22', 5);
  },

  // --- машины: квадратная плита
  ladder(g) { // приставная лестница, сходится кверху: стоит у стены
    g.stroke('M-26,47 L-16,-47', 8.5);
    g.stroke('M26,47 L16,-47', 8.5);
    for (const y of [-27, -3, 21]) {
      const h = 16 + ((y + 47) / 94) * 10;
      g.stroke(`M${-h},${y} L${h},${y}`, 7.5);
    }
  },
  ram(g) { // таран: навес на колёсах, из-под него — окованное бревно
    g.fill('M-47,-1 L-15,-37 L17,-1 Z');
    g.rect(-40, 3, 30, 15, 5);
    g.fill('M27,-3 L40,-3 L49,9 L40,21 L27,21 Z');
    g.cutCircle(-25, 30, 15);
    g.cutCircle(9, 30, 15);
    g.circle(-25, 30, 12);
    g.circle(9, 30, 12);
    if (g.fine) {
      g.cutCircle(-25, 30, 3.8);
      g.cutCircle(9, 30, 3.8);
      g.cutStroke('M-15,-25 L-15,-5', 3);
    }
  },
  siegetower(g) { // осадная башня с зубцами на катках
    g.fill('M-27,37 L-21,-31 L21,-31 L27,37 Z');
    g.rect(-25, -47, -12, -29, 1.5);
    g.rect(-6.5, -47, 6.5, -29, 1.5);
    g.rect(12, -47, 25, -29, 1.5);
    g.cutRect(-11, -23, 11, -3, 1.5); // проём под трап
    g.cutCircle(-17, 39, 12);
    g.cutCircle(17, 39, 12);
    g.circle(-17, 39, 9.5);
    g.circle(17, 39, 9.5);
    if (g.fine) {
      g.cutStroke('M-26,9 L26,9', 3); // ярусы
      g.cutCircle(-17, 39, 3.2);
      g.cutCircle(17, 39, 3.2);
    }
  },
  mantlet(g) { // мантелет: высокий дощатый щит на подпорке, за ним прячутся
    g.stroke('M24,-6 L44,46', 7);
    g.fill('M-31,40 L-31,-30 Q0,-50 31,-30 L31,40 Z');
    g.cutStroke('M-10.5,-40 L-10.5,40', 3.4);
    g.cutStroke('M10.5,-40 L10.5,40', 3.4);
    g.cutRect(-24, -19, 24, -11, 2); // смотровая щель
    g.rect(-38, 37, 38, 46, 2);
  },
  catapult(g) { // катапульта: рычаг с камнем на колёсной станине
    g.rect(-46, 14, 40, 26, 3);
    g.fill('M-7,15 L8,-13 L23,15 Z');
    g.stroke('M31,18 L-23,-26', 10);
    g.circle(-30, -33, 14);
    g.cutCircle(-27, 35, 15);
    g.cutCircle(22, 35, 15);
    g.circle(-27, 35, 12);
    g.circle(22, 35, 12);
    if (g.fine) {
      g.cutCircle(-27, 35, 3.8);
      g.cutCircle(22, 35, 3.8);
    }
  },
  trebuchet(g) { // требушет: высокая рама и противовес, колёс нет — с места не сходит
    g.rect(-47, 38, 47, 48, 2);
    g.stroke('M-25,40 L3,-12', 8.5);
    g.stroke('M31,40 L3,-12', 8.5);
    g.stroke('M-38,-43 L26,3', 7.5); // коромысло
    g.cutRect(10, 0, 41, 31, 2); // зазор, чтобы противовес не сливался с рамой
    g.rect(14, 4, 37, 27, 2.5);
    g.stroke('M-38,-43 L-44,-24', 3); // праща
    g.circle(-44, -18, 8);
  },
  ballista(g) { // баллиста: большой лук на треноге — машина, а не стрелок
    g.stroke('M-46,-3 Q0,-41 46,-3', 9);
    g.stroke('M-45,-3 L0,13 L45,-3', 3);
    g.stroke('M0,12 L0,-37', 6.5);
    g.fill('M0,-51 L-9.5,-34 L9.5,-34 Z');
    g.stroke('M0,17 L-28,47', 6.5);
    g.stroke('M0,17 L28,47', 6.5);
    g.stroke('M0,17 L0,47', 6.5);
  },

  // --- специалисты: ромб
  healer(g) { // крест
    g.rect(-13.5, -44, 13.5, 44, 4);
    g.rect(-44, -13.5, 44, 13.5, 4);
  },
  engineer(g) { // молоток с гвоздодёром: ловушки, частокол, починка
    g.rotate(-Math.PI / 7);
    g.rect(-6, -26, 6, 49, 3);
    g.rect(-34, -46, -12, -20, 3);
    g.rect(-14, -41, 10, -25, 1);
    g.fill('M8,-41 C24,-42 34,-34 38,-18 L29,-15 C26,-25 19,-28 8,-25 Z');
  },
  sapper(g) { // лопата полевого инженера осады
    g.rect(-16, -50, 16, -41, 3);
    g.rect(-5, -46, 5, 3, 2);
    g.fill('M-20,-3 L20,-3 L20,20 Q18,39 0,49 Q-18,39 -20,20 Z');
    if (g.fine) g.cutStroke('M0,6 L0,36', 3.2);
  },
  bomber(g) { // бомба с горящим фитилём
    g.circle(-7, 11, 33);
    g.save();
    g.translate(17, -17);
    g.rotate(Math.PI / 4);
    g.rect(-10, -8, 10, 8, 2);
    g.restore();
    g.stroke('M20,-22 Q24,-36 36,-36', 5);
    g.star(37, -37, 13, 5, 8); // искра
    if (g.fine) g.cutStroke('M-27,-4 Q-25,-14 -15,-17', 4); // блик
  },
};

const BUILDING_GLYPHS = {
  barracks(g) { // длинный дом с дверью: казармы кормят замок
    g.fill('M-46,-2 L-20,-30 L20,-30 L46,-2 Z');
    g.rect(-38, -4, 38, 36, 2);
    g.cutRect(-9, 12, 9, 36, 1);
    if (g.fine) {
      g.cutRect(-30, 6, -18, 18, 1);
      g.cutRect(18, 6, 30, 18, 1);
    }
  },
  tower(g) { // наблюдательная вышка на сваях: в неё входят
    g.fill('M-30,-20 L0,-46 L30,-20 Z');
    g.rect(-22, -22, 22, -2, 1);
    g.stroke('M-17,-2 L-25,46', 7);
    g.stroke('M17,-2 L25,46', 7);
    g.stroke('M-21,20 L21,20', 5);
    g.stroke('M-19,8 L21,34', 4);
    if (g.fine) g.cutRect(-13, -17, 13, -8, 1);
  },
  workshop(g) { // шестерня: мастерская удешевляет машины
    g.save();
    for (let i = 0; i < 8; i++) {
      g.rotate(Math.PI / 4);
      g.rect(-8, -47, 8, -28, 2);
    }
    g.restore();
    g.circle(0, 0, 33);
    g.cutCircle(0, 0, 13);
  },
  outpost(g) { // шатёр со штандартом
    g.fill('M-44,44 L0,-14 L44,44 Z');
    g.cut('M-11,44 L0,22 L11,44 Z');
    g.stroke('M0,-12 L0,-46', 4.5);
    g.fill('M2,-47 L32,-39 L2,-30 Z');
  },
  arrowTower(g) { // каменная башня с крестообразной бойницей: не вышка на сваях
    g.rect(-24, -26, 24, 47, 1);
    g.rect(-31, -45, -17, -24, 1);
    g.rect(-7, -45, 7, -24, 1);
    g.rect(17, -45, 31, -24, 1);
    g.rect(-31, -28, 31, -20, 1);
    g.cutRect(-3.5, -10, 3.5, 24, 1);
    g.cutRect(-11, 1, 11, 7, 1);
  },
  forge(g) { // наковальня
    g.fill('M-47,-24 L28,-24 L28,-8 L18,-8 L12,6 L20,22 L28,22 L28,34 L-28,34 L-28,22 L-20,22 L-14,6 L-20,-8 C-34,-9 -44,-14 -47,-24 Z');
    g.fill('M28,-24 L42,-24 L42,-14 L28,-14 Z');
    if (g.fine) g.cutStroke('M-26,-14 L22,-14', 2.5);
  },
  warehouse(g) { // бочка: склад поднимает потолок обоза
    g.fill('M-24,-44 L24,-44 Q36,0 24,44 L-24,44 Q-36,0 -24,-44 Z');
    g.cutRect(-34, -27, 34, -21, 0);
    g.cutRect(-34, 21, 34, 27, 0);
    if (g.fine) {
      g.cutStroke('M-10,-44 Q-14,0 -10,44', 2.5);
      g.cutStroke('M10,-44 Q14,0 10,44', 2.5);
    }
  },
  palisade(g) { // частокол: заострённые колья с перекладиной
    for (const x of [-33, -11, 11, 33]) {
      g.fill(`M${x - 9},44 L${x - 9},-27 L${x},-46 L${x + 9},-27 L${x + 9},44 Z`);
    }
    g.cutRect(-47, -6, 47, 7, 0);
    g.rect(-47, -3, 47, 4, 1);
  },
};

// Частокол замка (внутреннее имя barricade, см. building.go) рисуется тем же
// значком, что и частокол осады: заграждение одно и то же, и сторону на
// карте и так видно по цвету фишки.
BUILDING_GLYPHS.barricade = BUILDING_GLYPHS.palisade;

// Готовый значок кладём в кэш по размеру в пикселях устройства: на каждый
// кадр уходит один drawImage, а не два десятка путей. При зуме размеры
// меняются, поэтому кэш ограничен — переполнится, начнём заново.
const glyphCache = new Map();
const GLYPH_CACHE_MAX = 600;

function glyphBitmap(table, type, px, color) {
  const key = `${type}|${px}|${color}`;
  let bmp = glyphCache.get(key);
  if (bmp) return bmp;
  if (glyphCache.size >= GLYPH_CACHE_MAX) glyphCache.clear();
  bmp = document.createElement('canvas');
  bmp.width = bmp.height = px;
  const g = bmp.getContext('2d');
  g.translate(px / 2, px / 2);
  g.scale(px / 100, px / 100);
  g.fillStyle = color;
  g.strokeStyle = color;
  g.lineCap = 'round';
  g.lineJoin = 'round';
  const draw = table[type];
  if (draw) draw(pen(g, px));
  else {
    g.beginPath();
    g.arc(0, 0, 22, 0, Math.PI * 2);
    g.fill();
  }
  glyphCache.set(key, bmp);
  return bmp;
}

// stampGlyph ставит значок с тенью в один пиксель вниз: белый силуэт не
// растворяется на светлом краю фишки.
function stampGlyph(ctx, table, type, cx, cy, size, color) {
  const t = ctx.getTransform();
  const k = Math.hypot(t.a, t.b) || 1;
  const px = Math.max(8, Math.round(size * k));
  const bmp = glyphBitmap(table, type, px, color);
  ctx.save();
  ctx.shadowColor = 'rgba(0,0,0,.5)';
  ctx.shadowOffsetY = Math.max(1, size * 0.04) * k;
  ctx.shadowBlur = 0;
  ctx.drawImage(bmp, cx - size / 2, cy - size / 2, size, size);
  ctx.restore();
}

export function drawGlyph(ctx, type, cx, cy, size, color = INK) {
  stampGlyph(ctx, UNIT_GLYPHS, type, cx, cy, size, color);
}

// drawUnitIcon рисует ту же фишку в маленький canvas: лавка и панель отряда
// показывают ровно то, что стоит на карте, — с формой, засечками и ободом.
export function drawUnitIcon(canvas, type, side, meta) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth || canvas.width || 24;
  const h = canvas.clientHeight || canvas.height || 24;
  canvas.width = Math.round(w * dpr);
  canvas.height = Math.round(h * dpr);
  const ctx = canvas.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  const spec = specOf(meta, type);
  const family = familyOf(spec);
  const elite = Boolean(spec && spec.elite);
  const royal = Boolean(spec && spec.royal);
  // Фишка должна влезть в холст целиком — вместе с засечками и ободом.
  const reach = (shootsFar(spec) ? 1.26 : family === 'support' ? 1.2 : 1.1) + (elite || royal ? 0.3 : 0.1);
  const r = Math.min(w, h) / 2 / reach;
  drawToken(ctx, w / 2, h / 2, r, side, spec, { type, elite, royal, shadow: false });
}

// roundPath обводит скруглённый прямоугольник в путь, не открывая новый:
// годится и для Path2D, и для ctx после beginPath.
function roundPath(p, x, y, w, h, r) {
  p.moveTo(x + r, y);
  p.arcTo(x + w, y, x + w, y + h, r);
  p.arcTo(x + w, y + h, x, y + h, r);
  p.arcTo(x, y + h, x, y, r);
  p.arcTo(x, y, x + w, y, r);
  p.closePath();
}

// Плитка под значком того, что не отряд: тот же тёмный цвет, что у построек
// на карте. Цветная фишка — отряд, тёмная плитка — постройка, умение или
// действие: это видно ещё до того, как прочитаешь строку.
const ITEM_PLATE = '#2e261d';

// drawItemIcon рисует значок строки кошелька или карточки отряда: постройки
// во дворе, умения, работы и действия отрядов. key — ключ из меты или
// действия; для незнакомого ключа рисуется нейтральная точка.
export function drawItemIcon(canvas, key) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth || canvas.width || 24;
  const h = canvas.clientHeight || canvas.height || 24;
  canvas.width = Math.round(w * dpr);
  canvas.height = Math.round(h * dpr);
  const ctx = canvas.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  const r = Math.min(w, h) / 2 - 1;
  ctx.fillStyle = ITEM_PLATE;
  ctx.strokeStyle = '#6b5a40';
  ctx.lineWidth = 1;
  roundRect(ctx, w / 2 - r, h / 2 - r, r * 2, r * 2, r * 0.3);
  ctx.fill();
  ctx.stroke();
  // Чуть мельче фигурки отряда: у плитки нет цветной заливки, и крупный
  // силуэт упирался бы в её край.
  drawItemGlyph(ctx, key, w / 2, h / 2, r * 2.15, ITEM_PLATE);
}

// drawItemGlyph — силуэты для drawItemIcon. Устроены как drawGlyph: доли u,
// светлая заливка, детали вырезаны цветом плитки (cut).
function drawItemGlyph(ctx, key, cx, cy, size, cut) {
  const u = size * 0.3;
  const ink = '#fbf8f1';
  const fine = u >= 8; // мелкие прорези рисуем только там, где их видно
  ctx.save();
  ctx.translate(cx, cy);
  ctx.scale(u, u);
  ctx.lineCap = 'round';
  ctx.lineJoin = 'round';

  const paint = (color) => {
    ctx.fillStyle = color;
    ctx.strokeStyle = color;
  };
  const width = (k) => {
    ctx.lineWidth = Math.max(k, 1.15 / u);
  };
  const trace = (pts) => {
    ctx.beginPath();
    ctx.moveTo(pts[0], pts[1]);
    for (let i = 2; i < pts.length; i += 2) ctx.lineTo(pts[i], pts[i + 1]);
  };
  const poly = (...pts) => {
    trace(pts);
    ctx.closePath();
    ctx.fill();
  };
  const line = (w, ...pts) => {
    width(w);
    trace(pts);
    ctx.stroke();
  };
  const disc = (x, y, r) => {
    ctx.beginPath();
    ctx.arc(x, y, r, 0, Math.PI * 2);
    ctx.fill();
  };
  const rect = (x0, y0, x1, y1) => ctx.fillRect(x0, y0, x1 - x0, y1 - y0);
  const slab = (x0, y0, x1, y1, r) => {
    roundRect(ctx, x0, y0, x1 - x0, y1 - y0, r);
    ctx.fill();
  };
  const plus = (x, y, s, t) => {
    rect(x - t, y - s, x + t, y + s);
    rect(x - s, y - t, x + s, y + t);
  };
  // Арка: ворота, решётка — у обоих один проём.
  const arch = (x) => {
    ctx.beginPath();
    ctx.moveTo(-x, 1.02);
    ctx.lineTo(-x, -0.2);
    ctx.quadraticCurveTo(-x, -1.02, 0, -1.02);
    ctx.quadraticCurveTo(x, -1.02, x, -0.2);
    ctx.lineTo(x, 1.02);
  };
  // Вышка на сваях: наблюдательный пункт и его же «разбить».
  const lookout = () => {
    poly(-0.78, -0.56, 0, -1.04, 0.78, -0.56);
    rect(-0.56, -0.58, 0.56, -0.06);
    line(0.15, -0.44, -0.06, -0.74, 1.0);
    line(0.15, 0.44, -0.06, 0.74, 1.0);
  };

  paint(ink);
  switch (key) {
    // --- стройка во дворе ---
    case 'barracks': // казармы: дом под скатной крышей
      poly(-1.0, 0.95, -1.0, -0.12, 0, -1.0, 1.0, -0.12, 1.0, 0.95);
      paint(cut);
      slab(-0.24, 0.28, 0.24, 1.0, 0.12);
      if (fine) {
        rect(-0.72, 0.08, -0.44, 0.36);
        rect(0.44, 0.08, 0.72, 0.36);
      }
      break;

    case 'arrowTower': // стрелковая башня: каменная, с зубцами и крестовой бойницей
      rect(-0.62, -0.5, 0.62, 0.62);
      poly(-0.62, 0.6, -0.8, 1.02, 0.8, 1.02, 0.62, 0.6);
      rect(-0.8, -0.6, 0.8, -0.42);
      rect(-0.8, -1.02, -0.44, -0.55);
      rect(-0.18, -1.02, 0.18, -0.55);
      rect(0.44, -1.02, 0.8, -0.55);
      paint(cut);
      rect(-0.08, -0.26, 0.08, 0.52);
      rect(-0.28, 0.02, 0.28, 0.16);
      break;

    case 'forge': // кузница: наковальня и искра над ней
      ctx.beginPath();
      ctx.moveTo(-1.08, -0.42);
      ctx.quadraticCurveTo(-0.82, -0.6, -0.55, -0.58);
      ctx.lineTo(0.98, -0.58);
      ctx.lineTo(0.98, -0.26);
      ctx.lineTo(0.42, -0.22);
      ctx.quadraticCurveTo(0.26, 0.04, 0.38, 0.36);
      ctx.lineTo(0.78, 0.56);
      ctx.lineTo(0.84, 0.9);
      ctx.lineTo(-0.72, 0.9);
      ctx.lineTo(-0.66, 0.56);
      ctx.lineTo(-0.28, 0.36);
      ctx.quadraticCurveTo(-0.16, 0.04, -0.32, -0.22);
      ctx.lineTo(-0.58, -0.26);
      ctx.quadraticCurveTo(-0.84, -0.3, -1.08, -0.42);
      ctx.closePath();
      ctx.fill();
      ctx.beginPath();
      for (let i = 0; i < 8; i++) {
        const a = (i * Math.PI) / 4;
        const rad = i % 2 ? 0.08 : 0.26;
        ctx.lineTo(0.5 + Math.cos(a) * rad, -0.95 + Math.sin(a) * rad);
      }
      ctx.closePath();
      ctx.fill();
      break;

    case 'warehouse': // склад: бочка с обручами — и у замка, и в лагере
      ctx.beginPath();
      ctx.moveTo(-0.6, -0.98);
      ctx.quadraticCurveTo(-0.96, 0, -0.6, 0.98);
      ctx.lineTo(0.6, 0.98);
      ctx.quadraticCurveTo(0.96, 0, 0.6, -0.98);
      ctx.closePath();
      ctx.fill();
      paint(cut);
      line(0.13, -0.78, -0.52, 0.78, -0.52);
      line(0.13, -0.78, 0.52, 0.78, 0.52);
      if (fine) {
        line(0.06, -0.28, -0.4, -0.3, 0.4);
        line(0.06, 0.28, -0.4, 0.3, 0.4);
      }
      break;

    // --- умения ---
    case 'pitch': // кипящая смола: котёл и пар над ним
      rect(-0.98, -0.12, 0.98, 0.1);
      ctx.beginPath();
      ctx.moveTo(-0.86, 0.08);
      ctx.quadraticCurveTo(-0.86, 0.98, 0, 0.98);
      ctx.quadraticCurveTo(0.86, 0.98, 0.86, 0.08);
      ctx.closePath();
      ctx.fill();
      width(0.13);
      for (const x of [-0.46, 0, 0.46]) {
        ctx.beginPath();
        ctx.moveTo(x, -0.3);
        ctx.quadraticCurveTo(x + 0.22, -0.48, x, -0.66);
        ctx.quadraticCurveTo(x - 0.22, -0.84, x, -1.02);
        ctx.stroke();
      }
      break;

    case 'rebuild': // отстроить стену: кладка с зубцами и плюс
      rect(-1.02, -0.3, 0.62, 0.98);
      rect(-1.02, -0.66, -0.66, -0.3);
      rect(-0.38, -0.66, -0.02, -0.3);
      paint(cut);
      for (const y of [0.04, 0.38, 0.7]) line(0.09, -1.02, y, 0.62, y);
      for (const [x, y0, y1] of [[-0.42, -0.3, 0.04], [0.1, -0.3, 0.04], [-0.68, 0.04, 0.38], [-0.16, 0.04, 0.38],
        [0.36, 0.04, 0.38], [-0.42, 0.38, 0.7], [0.1, 0.38, 0.7]]) line(0.09, x, y0, x, y1);
      disc(0.6, -0.56, 0.5);
      paint(ink);
      plus(0.6, -0.56, 0.36, 0.11);
      break;

    case 'militia': // призвать ополчение: набатный колокол
      ctx.beginPath();
      ctx.moveTo(-0.86, 0.58);
      ctx.quadraticCurveTo(-0.6, 0.44, -0.6, 0);
      ctx.lineTo(-0.6, -0.26);
      ctx.quadraticCurveTo(-0.6, -0.86, 0, -0.86);
      ctx.quadraticCurveTo(0.6, -0.86, 0.6, -0.26);
      ctx.lineTo(0.6, 0);
      ctx.quadraticCurveTo(0.6, 0.44, 0.86, 0.58);
      ctx.closePath();
      ctx.fill();
      slab(-0.96, 0.52, 0.96, 0.74, 0.08);
      disc(0, 0.9, 0.18);
      width(0.12);
      ctx.beginPath();
      ctx.arc(0, -0.98, 0.14, 0, Math.PI * 2);
      ctx.stroke();
      break;

    case 'rally': // воодушевление: знамя с раздвоенным краем
      rect(-0.78, -0.95, -0.62, 1.06);
      disc(-0.7, -1.0, 0.14);
      poly(-0.62, -0.9, 0.98, -0.9, 0.6, -0.42, 0.98, 0.06, -0.62, 0.06);
      paint(cut);
      poly(-0.2, -0.62, 0.02, -0.28, -0.42, -0.28);
      break;

    case 'gate': // решётка: прутья с остриями в арке
      width(0.16);
      arch(0.9);
      ctx.stroke();
      for (const x of [-0.46, 0, 0.46]) {
        line(0.14, x, x === 0 ? -0.9 : -0.74, x, 0.66);
        poly(x - 0.13, 0.6, x + 0.13, 0.6, x, 0.92);
      }
      line(0.14, -0.84, -0.24, 0.84, -0.24);
      line(0.14, -0.84, 0.26, 0.84, 0.26);
      break;

    // --- работы отрядов ---
    case 'trap': // ловушка: раскрытый капкан — две челюсти с зубьями
      slab(-0.62, 0.66, 0.62, 0.96, 0.1);
      width(0.2);
      ctx.beginPath();
      ctx.moveTo(-0.98, -0.62);
      ctx.quadraticCurveTo(-0.98, 0.66, -0.2, 0.7);
      ctx.moveTo(0.98, -0.62);
      ctx.quadraticCurveTo(0.98, 0.66, 0.2, 0.7);
      ctx.stroke();
      for (const sgn of [-1, 1]) {
        for (const [y, x] of [[-0.48, 0.92], [-0.04, 0.88], [0.36, 0.74]]) {
          poly(sgn * x, y - 0.17, sgn * x, y + 0.17, sgn * (x - 0.42), y);
        }
      }
      break;

    case 'repair': // починить: молоток и плюс
      ctx.save();
      ctx.translate(-0.18, 0.18);
      ctx.rotate(Math.PI / 4);
      slab(-0.11, -0.3, 0.11, 0.98, 0.08);
      slab(-0.56, -0.74, 0.56, -0.3, 0.08);
      ctx.restore();
      plus(0.62, -0.6, 0.36, 0.12);
      break;

    case 'woodgate': // деревянные ворота: доски под аркой
      arch(0.86);
      ctx.closePath();
      ctx.fill();
      paint(cut);
      for (const x of [-0.43, 0, 0.43]) line(0.07, x, -0.9, x, 1.02);
      if (fine) {
        width(0.08);
        ctx.beginPath();
        ctx.arc(0.2, 0.2, 0.12, 0, Math.PI * 2);
        ctx.stroke();
      }
      break;

    // Частокол: у замка и у осады один значок — заграждение одно и то же.
    case 'barricade':
    case 'palisade': // частокол: острые колья в ряд на жерди
      for (const [x, top] of [[-0.8, -0.72], [-0.4, -0.98], [0, -0.78], [0.4, -1.0], [0.8, -0.74]]) {
        poly(x - 0.16, 1.0, x - 0.16, top + 0.26, x, top, x + 0.16, top + 0.26, x + 0.16, 1.0);
      }
      paint(cut);
      rect(-1.0, 0.12, 1.0, 0.32);
      paint(ink);
      rect(-1.02, 0.15, 1.02, 0.29);
      break;

    case 'watchpost': // наблюдательный пункт: вышка на сваях
      lookout();
      line(0.1, -0.56, 0.22, 0.62, 0.68);
      line(0.1, 0.56, 0.22, -0.62, 0.68);
      paint(cut);
      rect(-0.3, -0.42, 0.3, -0.2);
      break;

    case 'outpost': // аванпост: шатёр под флажком
      poly(-1.0, 0.98, -0.12, -0.36, 0.76, 0.98);
      line(0.12, -0.12, -0.36, -0.12, -1.04);
      poly(-0.12, -1.04, 0.66, -0.84, -0.12, -0.62);
      paint(cut);
      poly(-0.12, 0.18, -0.4, 0.98, 0.16, 0.98);
      break;

    case 'fascine': // фашины: вязанка хвороста, перехваченная ремнями
      slab(-1.04, -0.42, 1.04, 0.42, 0.4);
      paint(cut);
      line(0.06, -0.8, -0.14, 0.8, -0.14);
      line(0.06, -0.8, 0.14, 0.8, 0.14);
      for (const x of [-0.45, 0.45]) rect(x - 0.17, -0.6, x + 0.17, 0.6);
      paint(ink);
      for (const x of [-0.45, 0.45]) rect(x - 0.09, -0.56, x + 0.09, 0.56);
      break;

    // --- действия отрядов ---
    case 'climb': // лезть на стену: лестница у кладки
      rect(0.22, -0.55, 1.02, 1.02);
      rect(0.22, -0.98, 0.5, -0.55);
      rect(0.74, -0.98, 1.02, -0.55);
      paint(cut);
      line(0.07, 0.22, 0, 1.02, 0);
      line(0.07, 0.22, 0.5, 1.02, 0.5);
      paint(ink);
      ctx.save();
      ctx.translate(-0.4, 0.12);
      ctx.rotate(0.42);
      line(0.13, -0.26, -0.98, -0.26, 0.98);
      line(0.13, 0.26, -0.98, 0.26, 0.98);
      for (const y of [-0.62, -0.2, 0.22, 0.64]) line(0.1, -0.26, y, 0.26, y);
      ctx.restore();
      break;

    case 'charge': // заложить заряд: вспышка взрыва
      ctx.beginPath();
      for (let i = 0; i < 16; i++) {
        const a = -Math.PI / 2 + (i * Math.PI) / 8;
        const rad = i % 2 ? 0.48 : i % 4 ? 0.82 : 1.06;
        ctx.lineTo(Math.cos(a) * rad, Math.sin(a) * rad);
      }
      ctx.closePath();
      ctx.fill();
      paint(cut);
      disc(0, 0, 0.26);
      break;

    case 'bombard': // навесной удар: дуга полёта и камень над целью
      width(0.12);
      ctx.setLineDash([0.22, 0.2]);
      ctx.beginPath();
      ctx.moveTo(-0.98, 0.9);
      ctx.quadraticCurveTo(-0.3, -1.2, 0.5, 0);
      ctx.stroke();
      ctx.setLineDash([]);
      disc(0.56, 0.12, 0.26);
      ctx.strokeRect(0.24, 0.56, 0.7, 0.44);
      break;

    case 'strike': // разбить вышку: та же вышка с трещиной
      lookout();
      paint(cut);
      line(0.12, -0.2, -0.9, 0.06, -0.52, -0.14, -0.3, 0.1, 0);
      break;

    case 'heal': // лечить: крест, как у лекаря
      slab(-0.3, -0.96, 0.3, 0.96, 0.1);
      slab(-0.96, -0.3, 0.96, 0.3, 0.1);
      break;

    case 'ramp': // открыть трап: башня и откинутая доска
      rect(-1.0, -1.0, -0.3, 1.0);
      paint(cut);
      line(0.08, -1.0, -0.3, -0.3, -0.3);
      line(0.08, -1.0, 0.3, -0.3, 0.3);
      paint(ink);
      line(0.2, -0.3, -0.62, 0.92, 0.02);
      width(0.1);
      ctx.beginPath();
      ctx.arc(-0.3, -0.62, 0.86, -Math.PI * 0.42, -Math.PI * 0.12);
      ctx.stroke();
      break;

    case 'board': // сесть в башню: стрелка в дверь
      rect(0.06, -1.0, 0.98, 1.0);
      paint(cut);
      slab(0.3, 0.12, 0.74, 1.0, 0.18);
      paint(ink);
      poly(-0.04, 0.5, -0.5, 0.06, -0.5, 0.32, -1.04, 0.32, -1.04, 0.68, -0.5, 0.68, -0.5, 0.94);
      break;

    default:
      disc(0, 0, 0.3);
  }
  ctx.restore();
}

function drawFrame(ctx, L) {
  ctx.strokeStyle = 'rgba(0,0,0,.6)';
  ctx.lineWidth = 2;
  ctx.strokeRect(L.ox + 1, L.oy + 1, L.w - 2, L.h - 2);
}

function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function shade(hex, delta) {
  const n = parseInt(hex.slice(1), 16);
  const clamp = (v) => Math.max(0, Math.min(255, Math.round(v)));
  const r = clamp(((n >> 16) & 255) + delta);
  const g = clamp(((n >> 8) & 255) + delta);
  const b = clamp((n & 255) + delta);
  return `rgb(${r},${g},${b})`;
}
