// Package bot — игроки-боты «Осады».
//
// Бот садится на место игрока и ходит по тем же правилам и с тем же знанием,
// что и человек. На вход он получает снимок game.View для своего места —
// ровно тот, что ушёл бы в браузер, — и отвечает одним действием
// game.Action. Туман войны, засада в кустах и чужие ловушки для него так же
// закрыты: их нет в снимке, и подсмотреть их бот не может, даже если бы
// захотел. Проверяет действия по-прежнему сервер — бот только предлагает.
//
// Устроен бот как оценщик: на каждом шаге он перебирает всё, что можно
// сделать прямо сейчас (удар, шаг, наём, умение), даёт каждому варианту
// оценку в «припасах» и выбирает лучший. Неистраченное очко действия в игре
// и так превращается в припас, поэтому вариант дешевле этого не делается
// вовсе — бот завершает ход. Уровень сложности задаёт, какие приёмы бот
// знает, сколько очков готов тратить и насколько точно выбирает.
package bot

import (
	"fmt"
	"math/rand"
	"sort"

	"siege/internal/game"
)

// Level — уровень сложности бота.
type Level string

const (
	Easy   Level = "easy"
	Normal Level = "normal"
	Hard   Level = "hard"
)

// LevelInfo — уровень для интерфейса: ключ, подпись и чего от него ждать.
type LevelInfo struct {
	Key   Level  `json:"key"`
	Label string `json:"label"`
	Title string `json:"title"`
	Hint  string `json:"hint"`
}

var levelInfo = []LevelInfo{
	{Key: Easy, Label: "лёгкий", Title: "Новобранец",
		Hint: "Тратит не весь день и путается в выборе. Ломает стену одной катапультой и " +
			"подрывников не знает; проломы не чинит, а король бежит, только когда враг уже рядом."},
	{Key: Normal, Label: "средний", Title: "Воевода",
		Hint: "Играет всем основным: подрывник под стену, катапульта, рудник и пашня. " +
			"В замке льёт смолу, заколачивает проломы, уводит короля и ставит баллисту."},
	{Key: Hard, Label: "сложный", Title: "Полководец",
		Hint: "Без поблажек: копит штурмовой отряд перед подрывом, ведёт хрупких через кусты, " +
			"добивает короля с воодушевлением. Замок держит требушет и две баллисты."},
}

// Levels — все уровни по возрастанию сложности.
func Levels() []LevelInfo { return append([]LevelInfo(nil), levelInfo...) }

// ParseLevel разбирает ключ уровня из запроса.
func ParseLevel(s string) (Level, bool) {
	for _, l := range levelInfo {
		if string(l.Key) == s {
			return l.Key, true
		}
	}
	return "", false
}

// Info — описание уровня; неизвестный уровень описывается как средний.
func (l Level) Info() LevelInfo {
	for _, li := range levelInfo {
		if li.Key == l {
			return li
		}
	}
	return levelInfo[1]
}

// Имена ботов — три стороны войны из лора: имперский дом, дом мятежного
// барона и северные кланы. Двух одинаковых имён в комнате быть не может: по
// имени игроков различают в списке, в чате и в итоге осады.
var houses = []string{"Валкрейн", "Равенхольд", "Кхар-Дум"}

// Name подбирает боту имя, которое ещё никем не занято.
func Name(taken func(string) bool) string {
	for _, h := range houses {
		if !taken(h) {
			return h
		}
	}
	for i := 1; ; i++ {
		if n := fmt.Sprintf("Бот %d", i); !taken(n) {
			return n
		}
	}
}

// profile — что умеет бот данного уровня. Числа подобраны так, чтобы
// уровни отличались не одной точностью, а набором приёмов: лёгкий бот
// просто не знает о подрывнике и аванпосте, а не забывает о них через раз.
// Как они сказываются на игре, видно по TestSimEval.
type profile struct {
	noise    float64 // разброс оценок: 0.6 — почти наугад, 0 — строго лучшее
	apBudget int     // сколько очков действия бот готов тратить за ход
	minScore float64 // дешевле этого действие не стоит очка действия

	// Осада.
	bombers   int  // сколько подрывников держать разом
	catapults int  // и катапульт
	ladders   int  // и лестниц
	assault   int  // сколько штурмовых отрядов собирать
	eyes      int  // сколько разведчиков водить с собой
	economy   bool // занимать пашню и рудник
	elite     bool // нанимать рыцарей за серебро
	healer    bool // держать лекаря
	rally     bool // добивать короля с воодушевлением
	cautious  bool // не подставлять хрупких под стрелы, подходить кустами
	gather    int  // скольких штурмовых дождаться у стены перед подрывом
	fascines  bool // закладывать ров перед проломом
	outpost   bool // ставить аванпост у сборного пункта

	// Замок.
	shooters   int  // сколько стрелков держать на стене
	guards     int  // сколько бойцов ближнего боя
	builders   int  // сколько инженеров
	barracks   int  // до скольких казарм достраивать двор
	ballista   int  // сколько баллист
	trebuchet  bool // ставить ли требушет
	pitch      bool // лить смолу
	mend       bool // отстраивать и заколачивать проломы, латать кладку
	kingFlee   bool // уводить короля от проломов заранее
	militia    bool // поднимать ополчение
	castleMine bool // держать человека на руднике во дворе
	forge      bool // ставить кузницу, когда стену начали ломать
}

var profiles = map[Level]profile{
	// Новобранец всё-таки обороняется: смола и ополчение — две кнопки, до
	// которых догадывается любой. Без них лёгкий замок падал за три недели
	// и гонку за короля у человека выигрывал не человек, а второй бот.
	Easy: {
		noise: 0.6, apBudget: 5, minScore: 1.5,
		catapults: 1, assault: 6, eyes: 1,
		shooters: 4, guards: 2, builders: 1, barracks: 3,
		pitch: true, militia: true,
	},
	Normal: {
		noise: 0.2, apBudget: game.APPerTurn, minScore: 1.2,
		bombers: 1, catapults: 1, assault: 6, eyes: 1,
		economy: true, elite: true, rally: true, fascines: true, outpost: true,
		shooters: 6, guards: 3, builders: 1, barracks: 4, ballista: 1,
		pitch: true, mend: true, kingFlee: true, militia: true, castleMine: true,
	},
	Hard: {
		noise: 0.03, apBudget: game.APPerTurn, minScore: 1.1,
		bombers: 2, catapults: 2, ladders: 1, assault: 8, eyes: 2,
		economy: true, elite: true, healer: true, rally: true, cautious: true, gather: 3, fascines: true, outpost: true,
		shooters: 8, guards: 4, builders: 2, barracks: 4, ballista: 2, trebuchet: true,
		pitch: true, mend: true, kingFlee: true, militia: true, castleMine: true, forge: true,
	},
}

// Bot — один игрок-бот. Не потокобезопасен: комната зовёт его под своим
// замком.
type Bot struct {
	level Level
	prof  profile
	rng   *rand.Rand
	mem   memory
}

// memory — что бот помнит между шагами. Память — не подглядывание: это то,
// что он сам видел раньше, как помнил бы человек.
type memory struct {
	side game.Side
	day  int

	rejected map[string]bool // что сервер отбил в этом ходу

	aim       game.Point // участок кладки, который ломаем
	front     game.Point // замок: где враг последний раз жался к стене
	refuge    game.Point // замок: куда уводим короля
	hasRefuge bool
	wing      int        // замок: в какой половине двора прятать короля, ±1
	maybe     grid[bool] // осада: где может быть король, пока его не видно
	maybeDay  int        // по какой день круг поиска разросся
	seen      grid[int]  // осада: в какой день бот последний раз видел клетку
	farmer    int        // кто держит пашню
	miner     int        // кто держит рудник
}

// New создаёт бота. seed задаёт случайность: с одним и тем же зерном бот
// на одной и той же партии ходит одинаково — это нужно тестам.
func New(level Level, seed int64) *Bot {
	if _, ok := ParseLevel(string(level)); !ok {
		level = Normal
	}
	return &Bot{level: level, prof: profiles[level], rng: rand.New(rand.NewSource(seed))}
}

// SetLevel меняет уровень посреди партии: память при этом сохраняется.
func (b *Bot) SetLevel(l Level) {
	if _, ok := ParseLevel(string(l)); ok {
		b.level, b.prof = l, profiles[l]
	}
}

// cand — один вариант действия и его оценка.
type cand struct {
	a     game.Action
	score float64
	ap    int // сколько очков действия он стоит
}

// Next — следующее действие бота. v — снимок для его места; вне своего
// хода бот отвечает завершением хода, и комната его просто не зовёт.
func (b *Bot) Next(v *game.View) game.Action {
	end := game.Action{Kind: game.ActEndTurn}
	if v == nil || v.Phase != game.PhasePlay.String() || !v.You.MyTurn {
		return end
	}
	b.sync(v)
	if v.AP <= 0 || v.APMax-v.AP >= b.prof.apBudget {
		return end
	}
	w := newWorld(v)
	var cs []cand
	if w.side == game.SideDefender {
		cs = b.defend(w)
	} else {
		cs = b.siege(w)
	}
	if c := b.pick(cs, v.AP); c != nil {
		return c.a
	}
	return end
}

// Reject — сервер отбил предложенное действие. В этом ходу бот его больше
// не предлагает: иначе он упирался бы в одну и ту же стену до конца дня.
func (b *Bot) Reject(a game.Action) {
	if b.mem.rejected == nil {
		b.mem.rejected = map[string]bool{}
	}
	b.mem.rejected[actionKey(a)] = true
}

func actionKey(a game.Action) string {
	return fmt.Sprintf("%s|%d|%d,%d|%s|%s|%s|%d", a.Kind, a.UnitID, a.To.X, a.To.Y, a.Unit, a.Ability, a.Build, a.Col)
}

// sync сбрасывает память, которая живёт ход, и всю память, если бота
// пересадили на другую сторону: помнить защитнику за осаждающего нечего.
func (b *Bot) sync(v *game.View) {
	side := game.SideByName(v.You.Side)
	if side != b.mem.side {
		b.mem = memory{side: side}
	}
	if v.Day != b.mem.day || b.mem.rejected == nil {
		b.mem.day = v.Day
		b.mem.rejected = map[string]bool{}
	}
}

// pick выбирает действие. Шум — это и есть «неопытность»: новобранец видит
// те же варианты, но путается в том, какой из них лучше.
func (b *Bot) pick(cs []cand, ap int) *cand {
	var best *cand
	bestS := 0.0
	for i := range cs {
		c := &cs[i]
		if c.ap > ap || b.mem.rejected[actionKey(c.a)] {
			continue
		}
		s := c.score
		if b.prof.noise > 0 {
			s *= 1 + b.prof.noise*(b.rng.Float64()*2-1)
		}
		if s < b.prof.minScore {
			continue
		}
		if best == nil || s > bestS {
			best, bestS = c, s
		}
	}
	return best
}

// --- общее для обеих сторон ---

// want — пожелание найма: кого, насколько срочно и куда поближе.
type want struct {
	t    game.UnitType
	prio float64
	near game.Point
	// walk — только на боевой ход. Баллиста с места не сходит, и во дворе
	// она навсегда перегородила бы королю дорогу.
	walk bool
}

// hires превращает пожелания в варианты найма. Пожелание, на которое
// сейчас не хватает припасов, придерживает их: мелкий наём не должен
// откладывать то, что нужнее. reserve — сколько не трогать вовсе.
func (b *Bot) hires(w *world, wants []want, reserve int) []cand {
	sort.SliceStable(wants, func(i, j int) bool { return wants[i].prio > wants[j].prio })
	var out []cand
	seen := map[game.UnitType]bool{}
	for _, wt := range wants {
		if seen[wt.t] {
			continue
		}
		seen[wt.t] = true
		bv := w.buy[wt.t.String()]
		if bv == nil {
			continue
		}
		if !bv.Affordable {
			// Копить имеет смысл только валюту, и только на важное.
			if bv.Why == game.WhySupplies && wt.prio >= 6 && bv.Cost > reserve {
				reserve = bv.Cost
			}
			continue
		}
		if bv.Cost > 0 && w.v.You.Supplies-bv.Cost < reserve {
			continue
		}
		at, ok := w.deploySpot(wt)
		if !ok {
			continue
		}
		out = append(out, cand{
			a:     game.Action{Kind: game.ActDeploy, Unit: wt.t.String(), To: at},
			score: wt.prio, ap: game.DeployAPCost,
		})
	}
	return out
}

// strikes — варианты удара для одного отряда; цены целей задаёт ev — у
// осады и у замка они свои.
func (b *Bot) strikes(w *world, u *unit, ev *judge) []cand {
	if u.Acted || u.spec.Damage <= 0 {
		return nil
	}
	if u.Area > 0 {
		p, v := ev.bestArea(w, u)
		if v < 1 {
			return nil
		}
		return []cand{{a: game.Action{Kind: game.ActAttack, UnitID: u.ID, To: p}, score: v, ap: 1}}
	}
	var out []cand
	for _, p := range u.Targets {
		v := ev.direct(w, u, u.Pos, p)
		if v <= 0 {
			continue
		}
		out = append(out, cand{a: game.Action{Kind: game.ActAttack, UnitID: u.ID, To: p}, score: v, ap: 1})
	}
	return out
}

// rally — воодушевление: удваивает урон всех своих до конца хода и стоит
// всего обоза. Бот прикидывает, на сколько дороже станут удары, которые
// отряды могут нанести в этом ходу, и читает заклинание, только если
// прибавка того стоит: обычно — когда без него король или пролом выстоят,
// а с ним нет.
func (b *Bot) rally(w *world, ev *judge) []cand {
	if !b.prof.rally || !w.v.You.RallyOK {
		return nil
	}
	plain := 0.0
	for _, u := range w.own {
		plain += ev.bestStrike(w, u)
	}
	w.boost = true
	boosted := 0.0
	for _, u := range w.own {
		boosted += ev.bestStrike(w, u)
	}
	w.boost = false
	gain := boosted - plain
	need := 40.0
	if w.v.You.Supplies >= w.v.You.SupplyCap-15 {
		need = 15 // обоз всё равно упёрся в потолок: излишек лучше сжечь в бою
	}
	if gain < need {
		return nil
	}
	return []cand{{a: game.Action{Kind: game.ActAbility, Ability: "rally"}, score: gain - 25, ap: game.RallyAP}}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
