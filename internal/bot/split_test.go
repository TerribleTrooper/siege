package bot

import (
	"testing"

	"siege/internal/game"
)

// На второй карте боты должны хотя бы играть: ходить, нанимать, не падать и
// не зависать на каждом ходу. Их тактика писана под «Теснину», и на кольце
// стены она слабее, но партия обязана идти.
func TestBotsPlayOnSplitMap(t *testing.T) {
	s, st := simulateOn(t, game.Split, [3]Level{Normal, Normal, Normal}, 7, 10)
	if s.Day < 10 {
		t.Fatalf("партия не дошла до десятого дня: день %d, фаза %v", s.Day, s.Phase)
	}
	if st.actions == 0 {
		t.Fatal("боты не сделали ни одного действия")
	}
	// Отбитых действий быть не должно совсем: бот обязан предлагать только
	// то, что сервер разрешит. Небольшой хвост допускаем — он и на
	// «Теснине» есть, — но не половину хода.
	if st.rejects > st.actions/4 {
		t.Fatalf("слишком много отбитых действий: %d из %d, причины: %v",
			st.rejects, st.actions, st.rejected)
	}
	if st.stalls > 2 {
		t.Fatalf("боты зависали %d раз: ход пришлось завершать за них", st.stalls)
	}
}

// И не просто играть, а воевать: за осаду боты должны дойти до стены и
// начать её ломать, иначе вторая карта — красивая прогулка.
func TestBotsActuallySiegeOnSplitMap(t *testing.T) {
	if testing.Short() {
		t.Skip("целая партия")
	}
	s, st := simulateOn(t, game.Split, [3]Level{Normal, Normal, Normal}, 5, 0)
	if st.byKind[game.ActAttack] < 20 {
		t.Fatalf("за всю осаду всего %d ударов: стороны не сходятся", st.byKind[game.ActAttack])
	}
	hurt := 0
	for _, seg := range s.Wall {
		if seg.HP < seg.MaxHP {
			hurt++
		}
	}
	if hurt == 0 {
		t.Fatal("кладка цела до последнего дня: осада даже не началась")
	}
	if st.rejects > 0 || st.stalls > 0 {
		t.Fatalf("отбито %d действий, зависало %d ходов: %v", st.rejects, st.stalls, st.rejected)
	}
}
