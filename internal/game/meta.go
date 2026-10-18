package game

import "strconv"

// BoardMeta — геометрия одной карты. Карт две, и клиент забирает обе разом:
// какая из них в партии, он узнаёт из снимка.
type BoardMeta struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`

	// Ряды классической карты. У карты с кольцом стены единого «ряда стены»
	// нет, и там эти поля равны −1: клиент по ним и отличает одну от другой.
	WallRow   int    `json:"wallRow"`
	WalkRow   int    `json:"walkRow"`
	ThroneRow int    `json:"throneRow"`
	CampRow   int    `json:"campRow"`
	WallCols  [2]int `json:"wallCols"`
	GateCols  [2]int `json:"gateCols"`

	MineTiles  []Point  `json:"mineTiles"`
	FarmTiles  []Point  `json:"farmTiles"`
	PitchTiles []Point  `json:"pitchTiles"`
	GateTiles  []Point  `json:"gateTiles"`
	Tiles      []string `json:"tiles"` // плоская карта типов клеток, строка за строкой
}

// Meta — неизменные данные: клиент забирает их один раз и не дублирует баланс.
type Meta struct {
	Boards      map[string]BoardMeta `json:"boards"`
	Days        int                  `json:"days"`
	SeatSides   []string             `json:"seatSides"` // роль каждого места: в лобби её видно до начала партии
	AP          int                  `json:"ap"`
	FarmSupply  int                  `json:"farmSupply"`
	SilverPer   int                  `json:"silverPerTile"`
	BushCost    int                  `json:"bushCost"`
	Specs       []UnitSpec           `json:"specs"`
	Abilities   []Ability            `json:"abilities"`
	Builds      []Ability            `json:"builds"`       // что умеют инженеры замка
	SapBuilds   []Ability            `json:"sapperBuilds"` // ... и полевые инженеры осады
	Outpost     Ability              `json:"outpost"`      // аванпост осаждающих
	WatchPost   Ability              `json:"watchPost"`    // наблюдательный пункт: ставят обе стороны
	Fascine     Ability              `json:"fascine"`      // засыпать ров: работа осаждающих
	Store       Ability              `json:"store"`        // склад в лагере осаждающих
	WatchPostAP int                  `json:"watchPostAp"`
	OutpostAP   int                  `json:"outpostAp"`
	ConstructAP int                  `json:"constructAp"` // стройка во дворе замка
	Rally       Ability              `json:"rally"`       // воодушевление: доступно всем троим
	TowerSupply int                  `json:"towerSupply"` // припасы за удержанный наблюдательный пункт
	TowerBonus  int                  `json:"towerBonus"`  // +дальность тому, кто сидит внутри
	APCarry     int                  `json:"apCarry"`     // припасов за одно неизрасходованное очко
	BushCut     int                  `json:"bushCut"`
	Ambush      int                  `json:"ambush"`     // множитель удара из засады
	Heal        int                  `json:"heal"`       // сколько HP возвращает лекарь
	PitchTiles  []Point              `json:"pitchTiles"` // куда можно лить смолу
	MaxRepairs  int                  `json:"maxRepairs"` // сколько раз за ход можно отстраивать стену
	MaxHires    int                  `json:"maxHires"`   // и сколько отрядов можно нанять

	// Потолок обоза и насколько его поднимает склад: без этих чисел
	// исчезающий излишек припасов выглядел бы ошибкой сервера.
	SupplyCap    int `json:"supplyCap"`
	WarehouseCap int `json:"warehouseCap"`

	// Звёзды ветерана: сколько их бывает и что даёт каждая.
	VeteranStars  int `json:"veteranStars"`
	VeteranDamage int `json:"veteranDamage"`
	VeteranHP     int `json:"veteranHp"`

	// Terrain — что за клетка и как она работает: по символу из Tiles, плюс
	// woodgate и breach для сегментов стены, которые сменили вид по ходу
	// партии. Structures — то же для построек, по их kind. Карточка клетки
	// и строка под полем берут описания отсюда, а не держат свои.
	Terrain    map[string]Info `json:"terrain"`
	Structures map[string]Info `json:"structures"`
}

// Info — название и правило клетки или постройки. Short — пара слов для
// строки под полем, Hint — правило целиком для карточки. Числа подставляются
// из констант, поэтому описание не разойдётся с тем, как игра считает.
type Info struct {
	Label string `json:"label"`
	Short string `json:"short"`
	Hint  string `json:"hint"`
}

type Ability struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Cost  int    `json:"cost"`
	Hint  string `json:"hint"`

	// AP — цена в очках действия. Не задана — одно очко: столько стоит почти
	// любое действие. Панель показывает её на каждой кнопке рядом с
	// припасами, иначе «2» без подписи не говорит, чего именно два.
	AP int `json:"ap,omitempty"`

	// Instant — умение срабатывает сразу, без выбора клетки на карте.
	Instant bool `json:"instant,omitempty"`
}

func NewMeta() Meta {
	m := Meta{
		Days: SiegeDays, AP: APPerTurn,
		Boards:        map[string]BoardMeta{},
		FarmSupply:    FarmSupply,
		SilverPer:     SilverPerTile,
		BushCost:      BushMoveCost,
		Specs:         AllSpecs(),
		BushCut:       BushCoverCut,
		Ambush:        AmbushFactor,
		Heal:          HealAmount,
		MaxRepairs:    MaxWallRepairs,
		MaxHires:      MaxDeploysPerTurn,
		SupplyCap:     SupplyCap,
		WarehouseCap:  WarehouseCap,
		VeteranStars:  VeteranMaxStars,
		VeteranDamage: VeteranDamage,
		VeteranHP:     VeteranHP,
		Terrain:       terrainInfo(),
		Structures:    structureInfo(),
		Builds: []Ability{
			{Key: "trap", Label: "Ловушка", Cost: TrapCost, Hint: "Невидима осаждающим, бьёт на " + itoa(TrapDamage) + " при входе"},
			{Key: "barricade", Label: "Частокол", Cost: BarricadeCost,
				Hint: "Прясло в " + itoa(BarricadeSpan) + " клетки за одно действие и одну цену: линия встаёт " +
					"поперёк направления на выбранную клетку, прочность " + itoa(BarricadeHP) + " у каждого кола. " +
					"Где кол не встаёт, клетка выпадает — цена та же"},
			{Key: "repair", Label: "Починить", Cost: EngineerFixCost,
				Hint: "Рядом: своя постройка (+" + itoa(EngineerRepair) + ") или своя машина (+1/" +
					itoa(EngineerEngShare) + " прочности). Стоящая кладка — " + itoa(EngineerWallCost) +
					" припасов за 1/" + itoa(EngineerWallShare) + " прочности"},
			{Key: "woodgate", Label: "Деревянные ворота", Cost: WoodGateCost,
				Hint: "Заколотить соседний пролом створкой на " + itoa(WoodGateHP) +
					" прочности. Не на месте замковых ворот"},
		},
		SapBuilds: []Ability{
			{Key: "palisade", Label: "Частокол", Cost: PalisadeCost,
				Hint: "Перекрывает клетку, прочность " + itoa(PalisadeHP) + ". По колу за действие: " +
					"прясло в три клетки вяжут только во дворе замка"},
			{Key: "mantlet", Label: "Мантелет", Cost: MantletCost,
				Hint: "Передвижной щит: " + itoa(Spec(UnitMantlet).MaxHP) + " прочности, шаг " +
					itoa(Spec(UnitMantlet).Move) + ". Кто стоит прямо за ним, тот под стрелы не попадает — " +
					"бьют по щиту. Навесом и в рукопашной не спасает. Не больше " +
					itoa(Spec(UnitMantlet).MaxAlive)},
		},
		Fascine: Ability{
			Key: "fascine", Label: "Фашины",
			Hint: "Завалить хворостом клетку рва с водой: по клетке за раз, пехоте — даром, " +
				"прочим " + itoa(FascineAPCost) + " ОД. Настил держит " + itoa(FascineHP) +
				" прочности; по нему ров переходят и машины, а пешие не теряют темпа",
		},
		Store: Ability{
			Key: "warehouse", Label: "Склад", Cost: WarehouseCost,
			Hint: "В своём лагере: потолок обоза +" + itoa(WarehouseCap) +
				" припасов, не больше " + itoa(MaxWarehouse),
		},
		Outpost: Ability{
			Key: "outpost", Label: "Аванпост", Cost: OutpostCost,
			Hint: "Временный лагерь на " + itoa(OutpostLife) + " дней: из него можно нанимать отряды. " +
				itoa(OutpostAPCost) + " ОД, не ближе " + itoa(OutpostWallGap) + " рядов к стене, не больше " + itoa(OutpostMax),
		},
		WatchPost: Ability{
			Key: "watchpost", Label: "Наблюдательный пункт",
			Hint: "Вышка на открытом поле: обзор " + itoa(VisionTower) + ". Стрелку внутри +" +
				itoa(TowerVisionBonus) + " обзора, +" + itoa(TowerGarrisonBonus) + " дальности и +" +
				itoa(TowerDamageBonus) + " урона. +" + itoa(TowerSupply) + " припас в ход. " +
				"У замка ставят инженеры, у осаждающих — пехота. Не больше " + itoa(WatchPostMax) + " на сторону",
		},
		WatchPostAP: WatchPostAPCost,
		OutpostAP:   OutpostAPCost,
		ConstructAP: ConstructAPCost,
		Rally: Ability{
			Key: "rally", Label: "Воодушевление", Cost: RallyCost, AP: RallyAP,
			Hint: "Весь ваш отряд бьёт в " + itoa(RallyFactor) + " раза сильнее до конца хода — " +
				"и по людям, и по укреплениям",
		},
		TowerSupply: TowerSupply,
		TowerBonus:  TowerGarrisonBonus,
		APCarry:     APCarryPerPoint,
		// Пределы «сколько раз за ход» в подсказки не пишем: панель показывает
		// их рядом с названием вместе с остатком на этот ход.
		Abilities: []Ability{
			{Key: "pitch", Label: "Кипящая смола", Cost: PitchCost,
				Hint: "Урон " + itoa(PitchDamage) + " всем осаждающим у выбранной клетки под стеной"},
			{Key: "rebuild", Label: "Отстроить стену", Cost: RebuildCost,
				Hint: "Поднять сегмент заново на месте пустого пролома"},
			{Key: "gate", Label: "Решётка", AP: GateAPCost,
				Hint: "Поднять или опустить створку. Пока поднята, в проём войдёт и осаждающий — " +
					"вылазка оставляет за собой открытый вход"},
			{Key: "militia", Label: "Призвать ополчение", Cost: MilitiaCost, AP: MilitiaAP, Instant: true,
				Hint: "До " + itoa(MilitiaCount) + " горожан у короля: " + itoa(Spec(UnitMilitia).MaxHP) + " HP, урон " +
					itoa(Spec(UnitMilitia).Damage) + ". В предел армии не входят, но больше " +
					itoa(MaxMilitia) + " на поле не бывает"},
		},
	}
	for _, sd := range seatSides {
		m.SeatSides = append(m.SeatSides, sd.String())
	}
	for _, b := range Boards() {
		m.Boards[b.Key] = b.meta()
	}
	return m
}

// meta — геометрия карты для клиента.
func (b *Board) meta() BoardMeta {
	bm := BoardMeta{
		Key: b.Key, Name: b.Name, Cols: b.Cols, Rows: b.Rows,
		WallRow: -1, WalkRow: -1, ThroneRow: -1, CampRow: -1,
		WallCols: [2]int{-1, -1}, GateCols: [2]int{-1, -1},
		MineTiles: b.MineTiles(), FarmTiles: b.FarmTiles(),
		PitchTiles: b.PitchTiles(), GateTiles: b.GateTiles(),
	}
	if b == Classic {
		bm.WallRow, bm.WalkRow = WallRow, WalkRow
		bm.ThroneRow, bm.CampRow = ThroneRow, CampRowFirst
		bm.WallCols = [2]int{WallFirstCol, WallLastCol}
		bm.GateCols = [2]int{GateColLeft, GateColRight}
	}
	for y := 0; y < b.Rows; y++ {
		row := make([]byte, 0, b.Cols)
		for x := 0; x < b.Cols; x++ {
			row = append(row, b.tileChar(Point{x, y}))
		}
		bm.Tiles = append(bm.Tiles, string(row))
	}
	return bm
}

func itoa(v int) string { return strconv.Itoa(v) }

// terrainInfo — описания клеток по символам из tileChar. Сегменты стены
// меняют вид по ходу партии, поэтому для заколоченного и разрушенного
// сегмента есть отдельные ключи.
func terrainInfo() map[string]Info {
	return map[string]Info{
		".": {Label: "Поле", Short: "открытое место",
			Hint: "Шаг стоит 1. Ни укрытия, ни засады: по стоящему здесь бьют в полную силу."},
		"B": {Label: "Кусты", Short: "шаг " + itoa(BushMoveCost) + " · укрытие −" + itoa(BushCoverCut),
			Hint: "Войти стоит " + itoa(BushMoveCost) + " шага. Дальний урон по цели в кустах срезается на " +
				itoa(BushCoverCut) + ", но хотя бы 1 долетит — даже если цель видно. Кто вошёл незамеченным, " +
				"пропадает с чужой карты, а первый удар из засады бьёт ×" + itoa(AmbushFactor) + "."},
		"V": {Label: "Обрыв", Short: "непроходим · простреливается",
			Hint: "Не пройти, но стрелять через него можно. Режет подход к стене на коридоры."},
		"R": {Label: "Скалы", Short: "непроходимы",
			Hint: "Ни пройти, ни встать: из-за них стену не обойти с флангов."},
		"M": {Label: "Рудник", Short: "+" + itoa(SilverPerTile) + " серебра в ход",
			Hint: "Кто стоит на клетке в начале своего хода, получает " + itoa(SilverPerTile) +
				" серебра. Серебро платит только рудник, а нужно оно на элиту: рыцарей и арбалетчиков."},
		"F": {Label: "Пашня", Short: "+" + itoa(FarmSupply) + " припаса в ход",
			Hint: "Кто стоит на клетке в начале своего хода, получает +" + itoa(FarmSupply) + " припаса."},
		"~": {Label: "Река", Short: "в воду не входит никто",
			Hint: "Та же вода, что и ров: ни пеший, ни колёсный в неё не войдут. Через реку " +
				"переходят по мостам, по форпосту у истока или по настилу из фашин — " +
				"настил держит " + itoa(FascineHP) + " прочности."},
		"A": {Label: "Водяные ворота", Short: "арка над рекой · " + itoa(ArchHP) + " прочности",
			Hint: "Свод, под которым река входит в замок. Решётки в нём нет, зато кладки меньше, " +
				"чем в стене: пролом в арке впускает в замок по воде, минуя ров."},
		"D": {Label: "Ров с водой", Short: "в воду не входит никто",
			Hint: "Воду не переходит ни пеший, ни колёсный: единственная дорога через ров — настил. " +
				"Осаждающие засыпают ров фашинами: настил держит " + itoa(FascineHP) + " прочности, а сломают — вода вернётся."},
		"C": {Label: "Двор", Short: "найм и стройка замка",
			Hint: "Здесь стоят король и постройки замка. Замок нанимает сюда отряды и строит казармы, башни, кузницу и склад."},
		"W": {Label: "Боевой ход", Short: "за стеной",
			Hint: "Защитники стоят за стеной и бьют поверх неё, замок нанимает сюда отряды. " +
				"Осадная башня высаживает штурмующих прямо сюда."},
		"#": {Label: "Стена", Short: "ломают только машины",
			Hint: "Сегмент стены, " + itoa(WallHP) + " прочности. Мечом камень не рубят: кладку ломают катапульта и подрывник."},
		"T": {Label: "Башня стены", Short: "прочнее стены",
			Hint: "Утолщённый сегмент стены: " + itoa(TowerHP) + " прочности против " + itoa(WallHP) + " у обычного."},
		"G": {Label: "Ворота", Short: "решётка за " + itoa(GateAPCost) + " ОД",
			Hint: "Решётку поднимают и опускают за " + itoa(GateAPCost) + " очко действия. Поднятая пропускает всех — " +
				"и осаждающих тоже. Прочность " + itoa(GateHP) + ", и только по воротам бьёт таран."},
		"a": {Label: "Лагерь", Short: "зона найма",
			Hint: "Здесь осаждающий нанимает отряды и собирает осадную башню."},
		"b": {Label: "Лагерь", Short: "зона найма",
			Hint: "Здесь осаждающий нанимает отряды и собирает осадную башню."},
		"woodgate": {Label: "Деревянные ворота", Short: "заколоченный пролом",
			Hint: "Створка на месте пролома: " + itoa(WoodGateHP) + " прочности и своя решётка. Ставят инженеры замка."},
		"breach": {Label: "Пролом", Short: "проход внутрь",
			Hint: "Сегмент разрушен — здесь проходят все. Замок может отстроить его заново или заколотить деревянными воротами."},
	}
}

// structureInfo — описания построек по их kind: что даёт и кому.
func structureInfo() map[string]Info {
	return map[string]Info{
		BuildBarracks.String(): {Label: BuildBarracks.Label(), Short: "+" + itoa(BarracksSupply) + " припаса замку",
			Hint: "Кормят замок: +" + itoa(BarracksSupply) + " припаса в начале его хода, пока целы. Поэтому их и выгодно разбить катапультой."},
		BuildTower.String(): {Label: BuildTower.Label(), Short: "обзор " + itoa(VisionTower) + " · можно войти",
			Hint: "Вышка с обзором " + itoa(VisionTower) + ". Кто её держит, получает +" + itoa(TowerSupply) +
				" припас в ход. Стоящему внутри дальний урон режется на " + itoa(TowerCoverCut) + ", а стрелку внутри +" +
				itoa(TowerVisionBonus) + " обзора, +" + itoa(TowerGarrisonBonus) + " дальности и +" + itoa(TowerDamageBonus) +
				" урона. Чужую отнимают, просто заняв её; у вышки двое — она ничья."},
		BuildWorkshop.String(): {Label: BuildWorkshop.Label(), Short: "машины дешевле на " + itoa(WorkshopCut),
			Hint: "Пока цела, осадные машины обходятся хозяину на " + itoa(WorkshopCut) + " припаса дешевле."},
		BuildBarricade.String(): {Label: BuildBarricade.Label(), Short: "перекрывает клетку",
			Hint: "Перекрывает клетку, " + itoa(BarricadeHP) + " прочности. Ставят инженеры замка — " +
				"пряслом в " + itoa(BarricadeSpan) + " клетки за одно действие."},
		BuildOutpost.String(): {Label: BuildOutpost.Label(), Short: "найм у стены",
			Hint: "Временный лагерь осаждающего: из него нанимают отряды прямо в поле. Стоит " + itoa(OutpostLife) +
				" дней, потом сворачивается сам."},
		BuildArrowTower.String(): {Label: BuildArrowTower.Label(), Short: "+" + itoa(TowerRangeBonus) + " дальности стрелкам",
			Hint: "+" + itoa(TowerRangeBonus) + " дальности своим стрелкам в " + itoa(ArrowTowerRange) + " клетках вокруг, пока цела."},
		BuildForge.String(): {Label: BuildForge.Label(), Short: "отстройка −" + itoa(ForgeRebuildCut),
			Hint: "Отстройка пролома дешевле на " + itoa(ForgeRebuildCut) + " припасов, пока кузница цела."},
		BuildWarehouse.String(): {Label: BuildWarehouse.Label(), Short: "потолок обоза +" + itoa(WarehouseCap),
			Hint: "Потолок обоза +" + itoa(WarehouseCap) + " припасов, пока склад цел."},
		BuildFascine.String(): {Label: BuildFascine.Label(), Short: "настил через ров",
			Hint: "Настил из вязанок, " + itoa(FascineHP) + " прочности: по нему ров переходят и машины, а пешие не теряют темпа. " +
				"Сломают — вода вернётся."},
		BuildPalisade.String(): {Label: BuildPalisade.Label(), Short: "перекрывает клетку",
			Hint: "Перекрывает клетку, " + itoa(PalisadeHP) + " прочности. Ставят полевые инженеры осады — " +
				"по колу за действие."},
		BuildFort.String(): {Label: BuildFort.Label(), Short: "верхняя переправа · " + itoa(FortHP) + " прочности",
			Hint: "Бревенчатая застава поперёк ручья у самого истока, ничья. Это единственная " +
				"переправа выше по течению, и до неё от обоих лагерей ровно одинаково. " +
				"Сломают — переправы здесь больше нет, и река выше замка становится сплошной."},
		BuildBridge.String(): {Label: BuildBridge.Label(), Short: "переправа у ворот",
			Hint: "Старый мост напротив ворот, " + itoa(BridgeHP) + " прочности. Он ничей: осада подходит по нему к воротам с первого дня, замок ломает его, чтобы вернуть воду под стену, а полевые инженеры осады чинят. Сломают — на его месте снова открытая вода."},
	}
}

// tileChar — компактная кодировка карты для отрисовки.
func (b *Board) tileChar(p Point) byte {
	switch b.TileAt(p) {
	case TileCliff:
		return 'R'
	case TileRavine:
		return 'V'
	case TileBush:
		return 'B'
	case TileMine:
		return 'M'
	case TileFarm:
		return 'F'
	case TileMoat:
		return 'D'
	case TileRiver:
		return '~'
	case TileCourtyard:
		return 'C'
	case TileWalk:
		return 'W'
	case TileWall:
		switch b.segKind[p] {
		case SegGate:
			return 'G'
		case SegTower:
			return 'T'
		case SegArch:
			return 'A'
		}
		return '#'
	case TileCamp:
		if b.CampSide(p) == SideAttackerA {
			return 'a'
		}
		return 'b'
	}
	return '.'
}
