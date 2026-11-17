package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Articles of the knowledge base linked to tasks, and the tasks of one
// contact across projects (ADR-0022, P5). The knowledge base and the
// contacts are of the whole workspace; the tasks keep their visibility:
// a task the viewer does not see is only counted.

// MaxArticles bounds the articles of one task.
const MaxArticles = 20

// Article is an article of the knowledge base linked to a task.
type Article struct {
	ID        uuid.UUID
	Kind      string // article, qa
	Title     string
	Published bool
	LinkedAt  time.Time
}

// Brief is a task in a list of another section: a contact's or an
// article's.
type Brief struct {
	ID         uuid.UUID
	ProjectID  uuid.UUID
	Ref        string
	Title      string
	Status     string
	AssigneeID *uuid.UUID
	UpdatedAt  time.Time
}

// Briefs are the tasks the viewer sees and the number of those they do
// not; who sees none of a project does not learn its name.
type Briefs struct {
	Items  []Brief
	Hidden int32
}

// Articles lists the articles linked to a task the viewer sees.
func (s *Service) Articles(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID uuid.UUID) ([]Article, error) {
	var out []Article
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := visible(ctx, q, workspaceID, v, taskID); err != nil {
			return err
		}
		var err error
		out, err = articles(ctx, q, workspaceID, taskID)
		return err
	})
	if err != nil {
		return nil, dbError(err, "task articles")
	}
	return out, nil
}

func articles(ctx context.Context, q *store.Queries, workspaceID, taskID uuid.UUID) ([]Article, error) {
	rows, err := q.TaskArticles(ctx, store.TaskArticlesParams{WorkspaceID: workspaceID, TaskID: taskID})
	if err != nil {
		return nil, err
	}
	out := make([]Article, 0, len(rows))
	for _, r := range rows {
		out = append(out, Article{ID: r.ID, Kind: r.Kind, Title: r.Title, Published: r.Published, LinkedAt: r.CreatedAt})
	}
	return out, nil
}

// AddArticle links an article of the knowledge base to a task the viewer
// sees; linking it again changes nothing. The articles of the task after.
func (s *Service) AddArticle(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID, articleID uuid.UUID) ([]Article, error) {
	var out []Article
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := s.lockVisible(ctx, q, workspaceID, v, taskID)
		if err != nil {
			return err
		}
		found, err := q.ArticleExists(ctx, store.ArticleExistsParams{WorkspaceID: workspaceID, ID: articleID})
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: unknown article", ErrInvalid)
		}
		n, err := q.CountTaskArticles(ctx, store.CountTaskArticlesParams{WorkspaceID: workspaceID, TaskID: taskID})
		if err != nil {
			return err
		}
		if n >= MaxArticles {
			return fmt.Errorf("%w: a task has at most %d articles", ErrInvalid, MaxArticles)
		}
		added, err := q.InsertTaskArticle(ctx, store.InsertTaskArticleParams{
			WorkspaceID: workspaceID, TaskID: taskID, ArticleID: articleID, CreatedBy: &v.UserID, CreatedAt: s.now(),
		})
		if err != nil {
			return err
		}
		if added > 0 {
			if err := changed(ctx, tx, row, &v.UserID, false); err != nil {
				return err
			}
		}
		out, err = articles(ctx, q, workspaceID, taskID)
		return err
	})
	if err != nil {
		return nil, dbError(err, "link article")
	}
	return out, nil
}

// RemoveArticle unlinks an article from a task the viewer sees.
func (s *Service) RemoveArticle(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID, articleID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := s.lockVisible(ctx, q, workspaceID, v, taskID)
		if err != nil {
			return err
		}
		n, err := q.DeleteTaskArticle(ctx, store.DeleteTaskArticleParams{WorkspaceID: workspaceID, TaskID: taskID, ArticleID: articleID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return changed(ctx, tx, row, &v.UserID, false)
	})
	if err != nil {
		return dbError(err, "unlink article")
	}
	return nil
}

// lockVisible locks a task the viewer sees.
func (s *Service) lockVisible(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, v Viewer, taskID uuid.UUID) (store.Task, error) {
	if err := visible(ctx, q, workspaceID, v, taskID); err != nil {
		return store.Task{}, err
	}
	return q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: taskID})
}

// ArticleTasks lists the tasks an article is linked to.
func (s *Service) ArticleTasks(ctx context.Context, workspaceID uuid.UUID, v Viewer, articleID uuid.UUID) (Briefs, error) {
	var out Briefs
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ArticleTasks(ctx, store.ArticleTasksParams{Viewer: v.UserID, SeeAll: v.All, WorkspaceID: workspaceID, ArticleID: articleID})
		if err != nil {
			return err
		}
		out = Briefs{Items: []Brief{}}
		for _, r := range rows {
			out.add(r.Visible, Brief{
				ID: r.ID, ProjectID: r.ProjectID, Ref: Ref(r.ProjectKey, r.Number), Title: r.Title, Status: r.Status, AssigneeID: r.AssigneeID, UpdatedAt: r.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return Briefs{}, dbError(err, "article tasks")
	}
	return out, nil
}

// ContactTasks lists the tasks linked to the conversations of a contact, in
// every project.
func (s *Service) ContactTasks(ctx context.Context, workspaceID uuid.UUID, v Viewer, contactID uuid.UUID) (Briefs, error) {
	var out Briefs
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ContactTasks(ctx, store.ContactTasksParams{Viewer: v.UserID, SeeAll: v.All, WorkspaceID: workspaceID, ContactID: contactID})
		if err != nil {
			return err
		}
		out = Briefs{Items: []Brief{}}
		for _, r := range rows {
			out.add(r.Visible, Brief{
				ID: r.ID, ProjectID: r.ProjectID, Ref: Ref(r.ProjectKey, r.Number), Title: r.Title, Status: r.Status, AssigneeID: r.AssigneeID, UpdatedAt: r.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return Briefs{}, dbError(err, "contact tasks")
	}
	return out, nil
}

func (b *Briefs) add(visible bool, x Brief) {
	if visible {
		b.Items = append(b.Items, x)
	} else {
		b.Hidden++
	}
}
