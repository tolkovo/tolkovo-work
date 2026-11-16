package tasks

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Projects of the section (ADR-0022): a board of its own with numbers
// KEY-N, members and queues.

// Limits and values of projects.
const (
	MaxProjects       = 50
	MaxProjectName    = 60
	MaxProjectMembers = 200
	// AccessMembers: the project's members see it; AccessTeam: everyone.
	AccessMembers = "members"
	AccessTeam    = "team"
	// The default project every workspace has.
	DefaultProjectName = "Задачи"
	DefaultProjectKey  = "TSK"
)

// ProjectIcons are the icons a project may show; none: the key's letters.
var ProjectIcons = []string{"chart", "monitor", "server", "phone", "cart", "rocket", "heart", "globe"}

// Colors are the palette keys of the panel, as of tags.
var projectColors = []string{"gray", "teal", "amber", "red", "violet", "blue", "green"}

var (
	keyRe = regexp.MustCompile(`^[A-Z]{2,6}$`)
	refRe = regexp.MustCompile(`^(?:([A-Za-z]{2,6})-|#)?([1-9][0-9]{0,17})$`)
)

// Errors of projects.
var (
	// ErrProjectTaken: a project of the workspace has this name or key.
	ErrProjectTaken = errors.New("tasks: a project with this name or key exists")
	// ErrProjectNotEmpty: the project has tasks; archive it instead.
	ErrProjectNotEmpty = errors.New("tasks: the project has tasks")
)

// Viewer is who reads tasks: a task is shown to a member of its project, to
// anyone in a project open to the team, to its author and assignee, and to
// a role with tasks.manage (All).
type Viewer struct {
	UserID uuid.UUID
	All    bool
}

// ViewerOf is the viewer of a principal. All follows the role, not the
// scopes of a token: a token narrows what its holder may do, not which
// projects they are in.
func ViewerOf(p authz.Principal) Viewer {
	return Viewer{UserID: p.UserID, All: authz.Allowed(p.Role, authz.TasksManage)}
}

// Project is a project of tasks.
type Project struct {
	ID        uuid.UUID
	Name      string
	Key       string
	Icon      *string
	Color     string
	Access    string
	IsDefault bool
	Position  int32
	// MemberIDs are its members; with AccessTeam everyone works in it.
	MemberIDs  []uuid.UUID
	OpenTasks  int64
	ArchivedAt *time.Time
	CreatedAt  time.Time
	// Member: the viewer works in it (a member, or the project is the
	// team's). The left rail shows these; the rest only the switcher.
	Member bool
}

// ProjectInput is a project to create or change. Key is taken on create;
// changing it comes with its aliases (ADR-0022, P4).
type ProjectInput struct {
	Name      string
	Key       string
	Icon      *string
	Color     string
	Access    string
	Position  int32
	Archived  bool
	MemberIDs []uuid.UUID
}

// Ref is how people call a task: KEY-N.
func Ref(key string, number int64) string { return key + "-" + strconv.FormatInt(number, 10) }

// ParseRef reads KEY-N, #N or N; key is "" for a bare number, which means
// the default project.
func ParseRef(s string) (key string, number int64, ok bool) {
	m := refRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return strings.ToUpper(m[1]), n, true
}

// Projects lists the projects the viewer sees, in the switcher's order:
// active first, the default one at the top.
func (s *Service) Projects(ctx context.Context, workspaceID uuid.UUID, v Viewer) ([]Project, error) {
	out := []Project{}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := s.defaultProject(ctx, q, workspaceID); err != nil {
			return err
		}
		rows, err := q.ListProjects(ctx, workspaceID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			p := projectFrom(r.TaskProject, r.MemberIds, v)
			p.OpenTasks = r.OpenTasks
			if p.Member || v.All {
				out = append(out, p)
			}
		}
		return nil
	})
	if err != nil {
		return nil, dbError(err, "list projects")
	}
	return out, nil
}

// CreateProject adds a project; tasks.manage, checked by the caller.
func (s *Service) CreateProject(ctx context.Context, workspaceID uuid.UUID, v Viewer, in ProjectInput) (Project, error) {
	if err := validProject(&in, true); err != nil {
		return Project{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Project{}, err
	}
	var out Project
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := s.defaultProject(ctx, q, workspaceID); err != nil {
			return err
		}
		n, err := q.CountProjects(ctx, workspaceID)
		if err != nil {
			return err
		}
		if n >= MaxProjects {
			return fmt.Errorf("%w: at most %d projects", ErrInvalid, MaxProjects)
		}
		row, err := q.InsertProject(ctx, store.InsertProjectParams{
			ID: id, WorkspaceID: workspaceID, Name: in.Name, Key: in.Key, Icon: in.Icon, Color: in.Color, Access: in.Access,
			Position: in.Position, CreatedAt: s.now(),
		})
		if err != nil {
			return err
		}
		if err := s.setProjectMembers(ctx, q, workspaceID, id, in.MemberIDs); err != nil {
			return err
		}
		out = projectFrom(row, sorted(in.MemberIDs), v)
		return projectChanged(ctx, tx, workspaceID, id)
	})
	if err != nil {
		return Project{}, dbError(err, "create project")
	}
	return out, nil
}

// UpdateProject changes a project and replaces its members. The default
// project is never archived.
func (s *Service) UpdateProject(ctx context.Context, workspaceID, projectID uuid.UUID, v Viewer, in ProjectInput) (Project, error) {
	if err := validProject(&in, false); err != nil {
		return Project{}, err
	}
	var out Project
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetProjectForUpdate(ctx, store.GetProjectForUpdateParams{WorkspaceID: workspaceID, ID: projectID})
		if err != nil {
			return err
		}
		if in.Key != "" && in.Key != cur.Key {
			return fmt.Errorf("%w: the key of a project does not change yet", ErrInvalid)
		}
		archived := cur.ArchivedAt
		switch {
		case in.Archived && cur.IsDefault:
			return fmt.Errorf("%w: the default project is never archived", ErrInvalid)
		case in.Archived && archived == nil:
			now := s.now()
			archived = &now
		case !in.Archived:
			archived = nil
		}
		row, err := q.UpdateProject(ctx, store.UpdateProjectParams{
			Name: in.Name, Icon: in.Icon, Color: in.Color, Access: in.Access, Position: in.Position, ArchivedAt: archived,
			UpdatedAt: s.now(), WorkspaceID: workspaceID, ID: projectID,
		})
		if err != nil {
			return err
		}
		if err := q.ClearProjectMembers(ctx, store.ClearProjectMembersParams{WorkspaceID: workspaceID, ProjectID: projectID}); err != nil {
			return err
		}
		if err := s.setProjectMembers(ctx, q, workspaceID, projectID, in.MemberIDs); err != nil {
			return err
		}
		out = projectFrom(row, sorted(in.MemberIDs), v)
		return projectChanged(ctx, tx, workspaceID, projectID)
	})
	if err != nil {
		return Project{}, dbError(err, "update project")
	}
	return out, nil
}

// DeleteProject removes a project without tasks, with its queues; one with
// tasks is ErrProjectNotEmpty, the default one ErrInvalid.
func (s *Service) DeleteProject(ctx context.Context, workspaceID, projectID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetProjectForUpdate(ctx, store.GetProjectForUpdateParams{WorkspaceID: workspaceID, ID: projectID})
		if err != nil {
			return err
		}
		if cur.IsDefault {
			return fmt.Errorf("%w: the default project is never deleted", ErrInvalid)
		}
		n, err := q.DeleteEmptyProject(ctx, store.DeleteEmptyProjectParams{WorkspaceID: workspaceID, ID: projectID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrProjectNotEmpty
		}
		return projectChanged(ctx, tx, workspaceID, projectID)
	})
	if err != nil {
		return dbError(err, "delete project")
	}
	return nil
}

// Visible reports ErrNotFound unless the viewer sees the task: the check
// of every route under /tasks/{id} and of the agents' tools.
func (s *Service) Visible(ctx context.Context, workspaceID uuid.UUID, v Viewer, taskID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		ok, err := store.New(tx).TaskVisible(ctx, store.TaskVisibleParams{Viewer: v.UserID, SeeAll: v.All, WorkspaceID: workspaceID, ID: taskID})
		if err == nil && !ok {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return dbError(err, "task visible")
	}
	return nil
}

// IDByRef finds a task the viewer sees by KEY-N, #N or N, as people and
// agents call it; a bare number is of the default project.
func (s *Service) IDByRef(ctx context.Context, workspaceID uuid.UUID, v Viewer, ref string) (uuid.UUID, error) {
	key, n, ok := ParseRef(ref)
	if !ok {
		return uuid.Nil, ErrNotFound
	}
	var id uuid.UUID
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		var p store.TaskProject
		var err error
		if key == "" {
			p, err = s.defaultProject(ctx, q, workspaceID)
		} else {
			p, err = q.ProjectByKey(ctx, store.ProjectByKeyParams{WorkspaceID: workspaceID, Key: key})
		}
		if err != nil {
			return err
		}
		if id, err = q.TaskIDByNumber(ctx, store.TaskIDByNumberParams{WorkspaceID: workspaceID, ProjectID: p.ID, Number: n}); err != nil {
			return err
		}
		ok, err := q.TaskVisible(ctx, store.TaskVisibleParams{Viewer: v.UserID, SeeAll: v.All, WorkspaceID: workspaceID, ID: id})
		if err == nil && !ok {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return uuid.Nil, dbError(err, "find task")
	}
	return id, nil
}

// defaultProject returns the workspace's default project, making it for a
// workspace that has none yet.
func (s *Service) defaultProject(ctx context.Context, q *store.Queries, workspaceID uuid.UUID) (store.TaskProject, error) {
	p, err := q.DefaultProject(ctx, workspaceID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return p, err
	}
	if err := q.EnsureDefaultProject(ctx, store.EnsureDefaultProjectParams{
		ID: id, WorkspaceID: workspaceID, Name: DefaultProjectName, Key: DefaultProjectKey, CreatedAt: s.now(),
	}); err != nil {
		return p, err
	}
	return q.DefaultProject(ctx, workspaceID)
}

// taskProject picks and checks the project of a new task: the given one,
// the topic's, or the default. A viewer must see it; the bot (nil) takes
// any. An archived project takes no tasks.
func (s *Service) taskProject(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, v *Viewer, id *uuid.UUID) (store.TaskProject, error) {
	if id == nil {
		return s.defaultProject(ctx, q, workspaceID)
	}
	p, err := q.GetProject(ctx, store.GetProjectParams{WorkspaceID: workspaceID, ID: *id})
	if errors.Is(err, pgx.ErrNoRows) {
		return p, fmt.Errorf("%w: unknown project", ErrInvalid)
	}
	if err != nil {
		return p, err
	}
	if p.ArchivedAt != nil {
		return p, fmt.Errorf("%w: the project is archived", ErrInvalid)
	}
	if v != nil && !v.All && p.Access != AccessTeam {
		ok, err := q.IsProjectMember(ctx, store.IsProjectMemberParams{WorkspaceID: workspaceID, ProjectID: p.ID, UserID: v.UserID})
		if err != nil {
			return p, err
		}
		if !ok {
			return p, fmt.Errorf("%w: unknown project", ErrInvalid)
		}
	}
	return p, nil
}

// checkAssignee refuses an assignee who does not work in the project.
func checkAssignee(ctx context.Context, q *store.Queries, workspaceID, projectID uuid.UUID, assignee *uuid.UUID) error {
	if assignee == nil {
		return nil
	}
	ok, err := q.IsProjectMember(ctx, store.IsProjectMemberParams{WorkspaceID: workspaceID, ProjectID: projectID, UserID: *assignee})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: the assignee is not a member of the project", ErrMember)
	}
	return nil
}

// checkQueue refuses a queue of another project.
func checkQueue(ctx context.Context, q *store.Queries, workspaceID, projectID uuid.UUID, queue *uuid.UUID) error {
	if queue == nil {
		return nil
	}
	row, err := q.GetQueue(ctx, store.GetQueueParams{WorkspaceID: workspaceID, ID: *queue})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.ProjectID != projectID {
		return fmt.Errorf("%w: unknown queue", ErrInvalid)
	}
	return err
}

func (s *Service) setProjectMembers(ctx context.Context, q *store.Queries, workspaceID, projectID uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		if err := q.AddProjectMember(ctx, store.AddProjectMemberParams{
			WorkspaceID: workspaceID, ProjectID: projectID, UserID: id, CreatedAt: s.now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// projectKeys maps the projects of the workspace to their keys, for the
// refs of a page of tasks.
func projectKeys(ctx context.Context, q *store.Queries, workspaceID uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := q.ProjectKeys(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Key
	}
	return out, nil
}

// fillRef writes the ref of one task.
func fillRef(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, t *Task) error {
	p, err := q.GetProject(ctx, store.GetProjectParams{WorkspaceID: workspaceID, ID: t.ProjectID})
	if err != nil {
		return err
	}
	t.ProjectKey, t.Ref = p.Key, Ref(p.Key, t.Number)
	return nil
}

func validProject(in *ProjectInput, create bool) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Name == "" || utf8.RuneCountInString(in.Name) > MaxProjectName || !validText(in.Name) {
		return fmt.Errorf("%w: project name must be 1 to %d characters", ErrInvalid, MaxProjectName)
	}
	in.Key = strings.ToUpper(strings.TrimSpace(in.Key))
	if (create || in.Key != "") && !keyRe.MatchString(in.Key) {
		return fmt.Errorf("%w: project key must be 2 to 6 latin letters", ErrInvalid)
	}
	if in.Icon != nil && !slices.Contains(ProjectIcons, *in.Icon) {
		return fmt.Errorf("%w: unknown project icon", ErrInvalid)
	}
	if in.Color == "" {
		in.Color = "blue"
	}
	if !slices.Contains(projectColors, in.Color) {
		return fmt.Errorf("%w: unknown color", ErrInvalid)
	}
	if in.Access == "" {
		in.Access = AccessMembers
	}
	if in.Access != AccessMembers && in.Access != AccessTeam {
		return fmt.Errorf("%w: access must be members or team", ErrInvalid)
	}
	if in.Position < 0 || in.Position > 10000 {
		return fmt.Errorf("%w: position must be 0 to 10000", ErrInvalid)
	}
	if len(in.MemberIDs) > MaxProjectMembers {
		return fmt.Errorf("%w: at most %d project members", ErrInvalid, MaxProjectMembers)
	}
	if create && in.Archived {
		return fmt.Errorf("%w: a new project is not archived", ErrInvalid)
	}
	return nil
}

func projectFrom(r store.TaskProject, members []uuid.UUID, v Viewer) Project {
	if members == nil {
		members = []uuid.UUID{}
	}
	return Project{
		ID: r.ID, Name: r.Name, Key: r.Key, Icon: r.Icon, Color: r.Color, Access: r.Access, IsDefault: r.IsDefault,
		Position: r.Position, MemberIDs: members, ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt,
		Member: r.Access == AccessTeam || slices.Contains(members, v.UserID),
	}
}
