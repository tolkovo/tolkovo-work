package ai

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSearchQuery(t *testing.T) {
	for in, want := range map[string]string{
		"Как подключить аккаунт OnlyFans?": "как | подключить | аккаунт | onlyfans",
		"отчёт, ОТЧЁТ и 2 отчета":          "отчет | 2 | отчета",
		"a & b | c:* !": "",
		"":              "",
		"2fa не приходит код (iOS 17)": "2fa | не | приходит | код | ios | 17",
	} {
		if got := searchQuery(in); got != want {
			t.Errorf("searchQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func (f *fixture) topic(ws uuid.UUID, name string) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	err := f.app.InTenantTx(f.ctx, ws, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `INSERT INTO topics (id, workspace_id, name, created_at, updated_at) VALUES ($1, $2, $3, now(), now())`, id, ws, name)
		return err
	})
	if err != nil {
		f.t.Fatalf("topic: %v", err)
	}
	return id
}

func (f *fixture) article(ws uuid.UUID, in ArticleInput) Article {
	f.t.Helper()
	a, err := f.svc.CreateArticle(f.ctx, ws, in)
	if err != nil {
		f.t.Fatalf("create article %q: %v", in.Title, err)
	}
	return a
}

func titles(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Title)
	}
	return out
}

// The search finds published entries by any word, in the conversation's
// topic and the general ones, and never another workspace's.
func TestKB_Search(t *testing.T) {
	f := setup(t)
	crm, fansly := f.topic(f.ws, "CRM"), f.topic(f.ws, "Fansly")
	connect := f.article(f.ws, ArticleInput{
		Kind: KindArticle, Title: "Как подключить аккаунт OnlyFans", Published: true, TopicIDs: []uuid.UUID{crm, crm},
		Body: "Откройте «Настройки → Аккаунты» и нажмите «Добавить». Подключение занимает до 5 минут.", URL: "https://help.example.test/connect",
	})
	if len(connect.TopicIDs) != 1 || connect.URL != "https://help.example.test/connect" || connect.Kind != KindArticle {
		t.Fatalf("created %+v", connect)
	}
	f.article(f.ws, ArticleInput{Kind: KindQA, Title: "Можно ли выгрузить отчёт в Excel?", Body: "Пока нет: такой функции в приложении нет.", Published: true})
	f.article(f.ws, ArticleInput{Kind: KindArticle, Title: "Черновик про отчёты", Body: "Ещё не готово.", Published: false})
	f.article(f.ws2, ArticleInput{Kind: KindArticle, Title: "Чужой отчёт", Body: "Другое пространство.", Published: true})

	hits, err := f.svc.Search(f.ctx, f.ws, "не получается подключить аккаунт онлифанс", nil, SearchLimit)
	if err != nil || len(hits) != 1 || hits[0].ID != connect.ID || hits[0].Rank <= 0 || hits[0].Body == "" {
		t.Fatalf("connect: %v %+v", err, hits)
	}
	hits, _ = f.svc.Search(f.ctx, f.ws, "как выгрузить отчет", nil, SearchLimit)
	if got := titles(hits); len(got) != 1 || got[0] != "Можно ли выгрузить отчёт в Excel?" {
		t.Fatalf("report: %v", got)
	}
	// A topic narrows to its entries and the general ones.
	if hits, _ = f.svc.Search(f.ctx, f.ws, "подключить аккаунт отчет", &fansly, SearchLimit); len(hits) != 1 || hits[0].Kind != KindQA {
		t.Fatalf("other topic: %v", titles(hits))
	}
	if hits, _ = f.svc.Search(f.ctx, f.ws, "подключить аккаунт отчет", &crm, SearchLimit); len(hits) != 2 {
		t.Fatalf("own topic: %v", titles(hits))
	}
	for _, q := range []string{"", "и как же", "!!!"} {
		if hits, err = f.svc.Search(f.ctx, f.ws, q, nil, SearchLimit); err != nil || len(hits) != 0 {
			t.Fatalf("%q: %v %v", q, err, titles(hits))
		}
	}
}

func TestKB_Articles(t *testing.T) {
	f := setup(t)
	crm := f.topic(f.ws, "CRM")
	a := f.article(f.ws, ArticleInput{Kind: KindArticle, Title: "  Тарифы  ", Body: "Тариф «Старт» — 10 моделей.", Published: true})
	if a.Title != "Тарифы" || len(a.TopicIDs) != 0 {
		t.Fatalf("trimmed %+v", a)
	}
	time.Sleep(2 * time.Millisecond)
	b := f.article(f.ws, ArticleInput{Kind: KindQA, Title: "Есть ли пробный период?", Body: "Да, 7 дней.", Published: true})

	list, err := f.svc.Articles(f.ctx, f.ws)
	if err != nil || len(list) != 2 || list[0].ID != b.ID {
		t.Fatalf("list: %v %+v", err, list)
	}
	if other, _ := f.svc.Articles(f.ctx, f.ws2); len(other) != 0 {
		t.Fatalf("other workspace sees %+v", other)
	}

	up, err := f.svc.UpdateArticle(f.ctx, f.ws, a.ID, ArticleInput{
		Kind: KindArticle, Title: "Тарифы и цены", Body: "Тариф «Старт» — 10 моделей.", Published: false, TopicIDs: []uuid.UUID{crm},
	})
	if err != nil || up.Title != "Тарифы и цены" || up.Published || len(up.TopicIDs) != 1 {
		t.Fatalf("update: %v %+v", err, up)
	}
	if _, err := f.svc.UpdateArticle(f.ctx, f.ws, a.ID, ArticleInput{Kind: KindArticle, Title: "x", Body: "y", TopicIDs: []uuid.UUID{uuid.Must(uuid.NewV7())}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown topic: %v", err)
	}
	if _, err := f.svc.Article(f.ctx, f.ws2, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other workspace reads: %v", err)
	}
	if err := f.svc.DeleteArticle(f.ctx, f.ws2, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other workspace deletes: %v", err)
	}
	if err := f.svc.DeleteArticle(f.ctx, f.ws, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.svc.Article(f.ctx, f.ws, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}

	for name, in := range map[string]ArticleInput{
		"kind":       {Kind: "page", Title: "x", Body: "y"},
		"no title":   {Kind: KindArticle, Title: " ", Body: "y"},
		"two lines":  {Kind: KindArticle, Title: "a\nb", Body: "y"},
		"no body":    {Kind: KindQA, Title: "x", Body: ""},
		"javascript": {Kind: KindArticle, Title: "x", Body: "y", URL: "javascript:alert(1)"},
		"relative":   {Kind: KindArticle, Title: "x", Body: "y", URL: "/help"},
	} {
		if _, err := f.svc.CreateArticle(f.ctx, f.ws, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestKB_GuideAndImport(t *testing.T) {
	f := setup(t)
	crm := f.topic(f.ws, "CRM")
	g, err := f.svc.Guide(f.ctx, f.ws)
	if err != nil || g.About != "" || g.UpdatedAt != nil {
		t.Fatalf("empty guide: %v %+v", err, g)
	}
	if g, err = f.svc.SetGuide(f.ctx, f.ws, Guide{About: " Зерно — CRM для агентств. ", HandoffRules: "Оплата и возвраты — к человеку."}); err != nil || g.About != "Зерно — CRM для агентств." || g.UpdatedAt == nil {
		t.Fatalf("set guide: %v %+v", err, g)
	}
	long := make([]rune, MaxGuideText+1)
	for i := range long {
		long[i] = 'я'
	}
	if _, err := f.svc.SetGuide(f.ctx, f.ws, Guide{About: string(long)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long guide: %v", err)
	}

	rules, checklist := "Сразу к человеку: оплата, блокировка аккаунта.", "Email аккаунта\nВерсия приложения"
	res, err := f.svc.Import(f.ctx, f.ws, Import{HandoffRules: &rules, TaskChecklist: &checklist, Articles: []ArticleInput{
		{Kind: KindArticle, Title: "Подключение аккаунта", Body: "Шаг 1.", Published: true, TopicIDs: []uuid.UUID{crm}},
		{Kind: KindQA, Title: "Есть ли экспорт в Excel?", Body: "Пока нет.", Published: true},
	}})
	if err != nil || res.Created != 2 || res.Updated != 0 {
		t.Fatalf("import: %v %+v", err, res)
	}
	if g, _ = f.svc.Guide(f.ctx, f.ws); g.About != "Зерно — CRM для агентств." || g.HandoffRules != rules || g.TaskChecklist != checklist {
		t.Fatalf("guide after import %+v", g)
	}
	// Again, changed: the same title is replaced, whatever its case.
	res, err = f.svc.Import(f.ctx, f.ws, Import{Articles: []ArticleInput{
		{Kind: KindArticle, Title: "подключение АККАУНТА", Body: "Шаг 1 и шаг 2.", Published: true},
		{Kind: KindQA, Title: "Сколько стоит?", Body: "От 10 $ в месяц.", Published: true},
	}})
	if err != nil || res.Created != 1 || res.Updated != 1 {
		t.Fatalf("reimport: %v %+v", err, res)
	}
	list, _ := f.svc.Articles(f.ctx, f.ws)
	if len(list) != 3 {
		t.Fatalf("after reimport %d entries", len(list))
	}
	// A bad file saves nothing.
	for name, in := range map[string]Import{
		"repeat":    {Articles: []ArticleInput{{Kind: KindQA, Title: "Новый?", Body: "a"}, {Kind: KindQA, Title: "новый?", Body: "b"}}},
		"invalid":   {Articles: []ArticleInput{{Kind: KindQA, Title: "Ещё новый?", Body: "a"}, {Kind: KindQA, Title: "", Body: "b"}}},
		"bad topic": {Articles: []ArticleInput{{Kind: KindQA, Title: "Третий?", Body: "a", TopicIDs: []uuid.UUID{uuid.Must(uuid.NewV7())}}}},
	} {
		if _, err := f.svc.Import(f.ctx, f.ws, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if list, _ = f.svc.Articles(f.ctx, f.ws); len(list) != 3 {
		t.Fatalf("a failed import saved: %d entries", len(list))
	}
}
