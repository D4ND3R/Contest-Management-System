package adminweb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Cloning copies rows with INSERT ... SELECT over every column the catalog
// lists, so columns added by later migrations are copied without touching
// this code; only the columns that must change are named.

// copyableColumns returns the columns of table an INSERT may set (no
// identity or generated columns).
func copyableColumns(ctx context.Context, tx pgx.Tx, table string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT attname::text FROM pg_attribute
WHERE attrelid = $1::regclass AND attnum > 0 AND NOT attisdropped AND attgenerated = '' AND attidentity = ''
ORDER BY attnum`, table)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// copyRows copies the rows of table matching where; set replaces columns
// by SQL expressions. Placeholders in where and set refer to args. It
// returns the ids of the new rows (in the order of the old ids).
func copyRows(ctx context.Context, tx pgx.Tx, table, where string, set map[string]string, args ...any) ([]int64, error) {
	cols, err := copyableColumns(ctx, tx, table)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(cols))
	exprs := make([]string, len(cols))
	for i, c := range cols {
		names[i] = pgx.Identifier{c}.Sanitize()
		exprs[i] = names[i]
		if e, ok := set[c]; ok {
			exprs[i] = e
		}
	}
	for c := range set {
		if !contains(cols, c) {
			return nil, fmt.Errorf("copy %s: unknown column %s", table, c)
		}
	}
	t := pgx.Identifier{table}.Sanitize()
	rows, err := tx.Query(ctx, "INSERT INTO "+t+" ("+strings.Join(names, ", ")+") SELECT "+strings.Join(exprs, ", ")+
		" FROM "+t+" WHERE "+where+" ORDER BY id RETURNING id", args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// cloneOptions says what a contest clone takes along.
type cloneOptions struct {
	Name           string
	TaskSuffix     string
	Participations bool
}

// cloneContest copies a contest (as a draft) with its sites, certificate
// template and tasks — statements, attachments, datasets with testcases
// and managers, the live dataset — and optionally its participations,
// never submissions.
func cloneContest(ctx context.Context, tx pgx.Tx, id int64, o cloneOptions) (int64, error) {
	q := sqlc.New(tx)
	ids, err := copyRows(ctx, tx, "contests", "id = $1", map[string]string{
		"name": "$2", "status": "'draft'", "ranking_unfrozen": "false", "created_at": "now()", "updated_at": "now()",
	}, id, o.Name)
	if err != nil || len(ids) != 1 {
		return 0, cloneErr(err, "contest")
	}
	newID := ids[0]
	if _, err := copyRows(ctx, tx, "sites", "contest_id = $1", map[string]string{"contest_id": "$2"}, id, newID); err != nil {
		return 0, err
	}
	// The certificate template (one row keyed by the contest, no id).
	cols, err := copyableColumns(ctx, tx, "certificate_templates")
	if err != nil {
		return 0, err
	}
	names, exprs := make([]string, len(cols)), make([]string, len(cols))
	for i, c := range cols {
		names[i] = pgx.Identifier{c}.Sanitize()
		exprs[i] = names[i]
		if c == "contest_id" {
			exprs[i] = "$2"
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO certificate_templates ("+strings.Join(names, ", ")+") SELECT "+strings.Join(exprs, ", ")+
		" FROM certificate_templates WHERE contest_id = $1", id, newID); err != nil {
		return 0, err
	}
	tasks, err := q.ListTasksByContest(ctx, &id)
	if err != nil {
		return 0, err
	}
	for _, t := range tasks {
		ids, err := copyRows(ctx, tx, "tasks", "id = $1", map[string]string{
			"contest_id": "$2", "name": "$3", "active_dataset_id": "NULL", "created_at": "now()", "updated_at": "now()",
		}, t.ID, newID, t.Name+o.TaskSuffix)
		if err != nil || len(ids) != 1 {
			return 0, cloneErr(err, "task "+t.Name+o.TaskSuffix)
		}
		nt := ids[0]
		for _, table := range []string{"statements", "attachments"} {
			if _, err := copyRows(ctx, tx, table, "task_id = $1", map[string]string{"task_id": "$2"}, t.ID, nt); err != nil {
				return 0, err
			}
		}
		datasets, err := q.ListDatasetsByTask(ctx, t.ID)
		if err != nil {
			return 0, err
		}
		sort.Slice(datasets, func(i, j int) bool { return datasets[i].ID < datasets[j].ID })
		for _, d := range datasets {
			ids, err := copyRows(ctx, tx, "datasets", "id = $1", map[string]string{"task_id": "$2", "created_at": "now()"}, d.ID, nt)
			if err != nil || len(ids) != 1 {
				return 0, cloneErr(err, "dataset")
			}
			for _, table := range []string{"testcases", "managers"} {
				if _, err := copyRows(ctx, tx, table, "dataset_id = $1", map[string]string{"dataset_id": "$2"}, d.ID, ids[0]); err != nil {
					return 0, err
				}
			}
			if t.ActiveDatasetID != nil && *t.ActiveDatasetID == d.ID {
				if err := q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: nt, ActiveDatasetID: &ids[0]}); err != nil {
					return 0, err
				}
			}
		}
	}
	if o.Participations {
		// Same users, teams and settings; sites mapped by name; nobody has
		// started yet.
		if _, err := copyRows(ctx, tx, "participations", "contest_id = $1", map[string]string{
			"contest_id": "$2", "starting_time": "NULL", "login_nonce": "0",
			"site_id": "(SELECT n.id FROM sites n JOIN sites o ON o.name = n.name WHERE o.id = participations.site_id AND n.contest_id = $2)",
		}, id, newID); err != nil {
			return 0, err
		}
	}
	return newID, nil
}

// cloneErr turns a duplicate name into a message for the form.
func cloneErr(err error, what string) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return &formErr{"The name is already taken: " + what}
	}
	if err == nil {
		return errors.New("copy " + what + ": no row")
	}
	return err
}

type formErr struct{ msg string }

func (e *formErr) Error() string { return e.msg }

func (s *Server) handleContestClone(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	f := newForm(r)
	o := cloneOptions{Name: f.identifier("name", "Name"), Participations: f.check("participations")}
	o.TaskSuffix = strings.TrimSpace(f.str("task_suffix"))
	if o.TaskSuffix == "" {
		o.TaskSuffix = "-" + o.Name
	}
	if !validName(o.TaskSuffix) {
		f.fail("the task name suffix may only have letters, digits, '_', '.' and '-'")
	}
	if f.err == nil {
		if _, err := s.q.GetContestByName(r.Context(), o.Name); err == nil {
			f.fail("a contest named %q already exists", o.Name)
		}
	}
	if f.err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, f.err.Error())
		return
	}
	var newID int64
	err := db.InTx(r.Context(), s.pool, func(tx pgx.Tx, _ *sqlc.Queries) error {
		var err error
		newID, err = cloneContest(r.Context(), tx, c.ID, o)
		return err
	})
	var fe *formErr
	if errors.As(err, &fe) {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, fe.msg)
		return
	}
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", newID)
	rc.note("source", c.ID)
	s.done(w, r, "/contests/"+strconv.FormatInt(newID, 10), "Contest copied as a draft: check its dates and publish it.")
}

func validName(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return s != ""
}
