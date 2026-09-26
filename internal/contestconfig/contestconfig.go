// Package contestconfig keeps a contest's configuration in a directory
// that can live in Git (SPEC_IOI §15): contest.yaml with the contest's
// settings and task order, and tasks/<name>/ with each task as a problem
// package. Export writes the directory from the database; Apply makes the
// database match it, deterministically and idempotently: applying the same
// directory twice changes nothing the second time.
//
// Contestants are not part of it: their credentials do not belong in a
// repository (they are imported from CSV).
package contestconfig

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/problempkg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

// FileName is the contest file at the root of the directory.
const FileName = "contest.yaml"

// Version is the format of contest.yaml.
const Version = 1

// excluded are the contest columns contest.yaml does not carry, with why.
var excluded = map[string]string{
	"id": "", "name": "", "created_at": "", "updated_at": "",
	"banner_digest":      "the banner is uploaded from the admin panel",
	"banner_type":        "the banner is uploaded from the admin panel",
	"status":             "it is operational state, changed from the admin panel",
	"submissions_paused": "it is operational state, changed from the admin panel",
	"pause_message":      "it is operational state, changed from the admin panel",
	"ranking_unfrozen":   "it is operational state, changed from the admin panel",
	"invitation_code":    "it is a secret and does not belong in a repository",
}

// File is contest.yaml.
type File struct {
	Format   int            `yaml:"format"`
	Name     string         `yaml:"name"`
	Settings map[string]any `yaml:"settings"`
	Tasks    []string       `yaml:"tasks"`
}

const header = `# Contest configuration: see docs/en/contest-config.md (docs/es/configuracion-en-git.md).
# Apply with: cms ctl contest-config apply <this directory>
`

// settingsOf is a contest's configurable settings, times in UTC.
func settingsOf(c any) (map[string]any, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, v := range m {
		if _, skip := excluded[k]; skip {
			delete(m, k)
			continue
		}
		m[k] = normalize(v)
	}
	return m, nil
}

// normalize makes equal values compare equal: timestamps in UTC.
func normalize(v any) any {
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return v
}

var taskNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// Export writes a contest's configuration to dir: contest.yaml and
// tasks/<name>/ (the live dataset of each task). Both are replaced;
// other files in dir (a README, .git) are left alone.
func Export(ctx context.Context, q *sqlc.Queries, store blob.Store, contest, dir string) error {
	c, err := q.GetContestByName(ctx, contest)
	if err != nil {
		return fmt.Errorf("contest %q: %w", contest, err)
	}
	settings, err := settingsOf(c)
	if err != nil {
		return err
	}
	tasks, err := q.ListTasksByContest(ctx, &c.ID)
	if err != nil {
		return err
	}
	f := File{Format: Version, Name: c.Name, Settings: settings, Tasks: []string{}}
	for _, t := range tasks {
		if !taskNameRe.MatchString(t.Name) {
			return fmt.Errorf("task name %q cannot be a directory name", t.Name)
		}
		f.Tasks = append(f.Tasks, t.Name)
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dir, "tasks")); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), append([]byte(header), b...), 0o644); err != nil {
		return err
	}
	for _, t := range tasks {
		var buf bytes.Buffer
		if err := problempkg.Export(ctx, q, store, t.ID, 0, &buf); err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			return err
		}
		if err := unzip(zr, filepath.Join(dir, "tasks", t.Name)); err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
	}
	return nil
}

func unzip(zr *zip.Reader, dir string) error {
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		name := filepath.FromSlash(zf.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unsafe path %q", zf.Name)
		}
		dst := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		rd, err := zf.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(dst)
		if err != nil {
			rd.Close()
			return err
		}
		_, err = out.ReadFrom(rd)
		rd.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Options tune Apply.
type Options struct {
	// DryRun only reports what would change.
	DryRun bool
	// Activate makes a changed task's new dataset live even once the
	// contest has started (before the start it always is).
	Activate bool
	// Packages are the problem package reading options (languages).
	Packages problempkg.Options
	// Now is the current time (tests).
	Now time.Time
}

// TaskChange is what Apply did (or would do) to one task.
type TaskChange struct {
	Name string
	// Action: "created"; "updated" (a new dataset, statements synced);
	// "activated" (a dataset imported earlier from this content made
	// live); "pending" (that dataset exists but may not go live: the
	// contest has started); "unchanged".
	Action  string
	Dataset string
	// Live reports whether the dataset is (now) the live one.
	Live bool
}

// Report is what Apply did.
type Report struct {
	Contest string
	// Created: the contest did not exist.
	Created bool
	// Settings lists the settings that changed.
	Settings []string
	Tasks    []TaskChange
	// Reordered: the task order changed.
	Reordered bool
	// Unlisted are the contest's tasks contest.yaml does not name: they are
	// left alone (removing a task is done from the admin panel).
	Unlisted []string
	// LiveChanged are the tasks whose live dataset changed (the dispatcher
	// must be told).
	LiveChanged []int64
	Warnings    []string
}

// Changed reports whether anything changed.
func (r *Report) Changed() bool {
	if r.Created || len(r.Settings) > 0 || r.Reordered {
		return true
	}
	for _, t := range r.Tasks {
		if t.Action != "unchanged" && t.Action != "pending" {
			return true
		}
	}
	return false
}

// Load reads contest.yaml.
func Load(dir string) (*File, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if f.Format > Version {
		return nil, fmt.Errorf("%s: format %d is newer than this CMS understands (%d)", FileName, f.Format, Version)
	}
	if f.Name == "" {
		return nil, fmt.Errorf("%s: name is required", FileName)
	}
	seen := map[string]bool{}
	for _, t := range f.Tasks {
		if !taskNameRe.MatchString(t) {
			return nil, fmt.Errorf("%s: %q is not a valid task name", FileName, t)
		}
		if seen[t] {
			return nil, fmt.Errorf("%s: task %q is listed twice", FileName, t)
		}
		seen[t] = true
	}
	for k := range f.Settings {
		if why, ok := excluded[k]; ok {
			if why == "" {
				why = "it is not a setting"
			}
			return nil, fmt.Errorf("%s: %s cannot be set here: %s", FileName, k, why)
		}
	}
	return &f, nil
}

type taskPlan struct {
	name   string
	pkg    *problempkg.Package
	digest string
	task   *sqlc.Task
	// imported is the task's dataset already holding this content.
	imported *sqlc.Dataset
}

// tagRe is the digest tag Apply appends to the datasets it creates (with
// the suffix that keeps descriptions unique).
var tagRe = regexp.MustCompile(` @[0-9a-f]{12}( \(\d+\))?$`)

func tag(digest string) string { return " @" + digest[:12] }

// Apply makes the database match the directory. Every package is read and
// checked before anything is written; the contest settings and task order
// are written in one transaction, each changed task in its own.
func Apply(ctx context.Context, pool *pgxpool.Pool, store blob.Store, dir string, o Options) (*Report, error) {
	f, err := Load(dir)
	if err != nil {
		return nil, err
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	q := sqlc.New(pool)
	rep := &Report{Contest: f.Name}

	// The contest settings.
	cur, err := q.GetContestByName(ctx, f.Name)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	rep.Created = !exists
	known, err := settingsOf(sqlc.Contest{})
	if err != nil {
		return nil, err
	}
	for k := range f.Settings {
		if _, ok := known[k]; !ok {
			return nil, fmt.Errorf("%s: unknown setting %q", FileName, k)
		}
	}
	var start, stop time.Time
	if exists {
		base, err := settingsOf(cur)
		if err != nil {
			return nil, err
		}
		up, err := merge(cur, f.Settings)
		if err != nil {
			return nil, err
		}
		after, err := settingsOf(up)
		if err != nil {
			return nil, err
		}
		for k, v := range after {
			if _, ok := base[k]; ok && !reflect.DeepEqual(v, base[k]) {
				rep.Settings = append(rep.Settings, k)
			}
		}
	} else {
		if start, stop, err = times(f.Settings); err != nil {
			return nil, err
		}
		if _, err := merge(sqlc.Contest{}, f.Settings); err != nil {
			return nil, err
		}
		for k := range f.Settings {
			rep.Settings = append(rep.Settings, k)
		}
	}
	sort.Strings(rep.Settings)

	// The tasks: read and check every package first.
	var plans []taskPlan
	var problems []string
	for _, name := range f.Tasks {
		fsys := os.DirFS(filepath.Join(dir, "tasks", name))
		p := problempkg.ReadFS(fsys, o.Packages)
		for _, e := range p.Errors {
			problems = append(problems, fmt.Sprintf("tasks/%s: %s", name, e))
		}
		for _, w := range p.Warnings {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("tasks/%s: %s", name, w))
		}
		if !p.OK() {
			continue
		}
		if p.Config.Name != name {
			problems = append(problems, fmt.Sprintf("tasks/%s: problem.yaml names the task %q", name, p.Config.Name))
			continue
		}
		digest, err := problempkg.DigestFS(fsys)
		if err != nil {
			return nil, err
		}
		tp := taskPlan{name: name, pkg: p, digest: digest}
		t, err := q.GetTaskByName(ctx, name)
		switch {
		case err == nil:
			if t.ContestID != nil && (!exists || *t.ContestID != cur.ID) {
				problems = append(problems, fmt.Sprintf("tasks/%s: the task belongs to another contest", name))
				continue
			}
			tp.task = &t
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
		plans = append(plans, tp)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("nothing was applied:\n  %s", strings.Join(problems, "\n  "))
	}
	started := exists && !o.Now.Before(cur.StartTime)
	for i := range plans {
		tp := &plans[i]
		tc := TaskChange{Name: tp.name, Action: "created", Live: true}
		if tp.task != nil {
			live, imported, err := current(ctx, q, store, tp.task, tp.digest)
			if err != nil {
				return nil, err
			}
			tp.imported = imported
			mayGoLive := !started || o.Activate
			switch {
			case live:
				tc.Action, tc.Live = "unchanged", true
			case imported != nil && mayGoLive:
				tc.Action, tc.Live, tc.Dataset = "activated", true, imported.Description
			case imported != nil:
				tc.Action, tc.Dataset = "pending", imported.Description
			default:
				tc.Action, tc.Live = "updated", mayGoLive
			}
		}
		rep.Tasks = append(rep.Tasks, tc)
	}
	if exists {
		current, err := q.ListTasksByContest(ctx, &cur.ID)
		if err != nil {
			return nil, err
		}
		listed := map[string]bool{}
		for _, t := range f.Tasks {
			listed[t] = true
		}
		var order []string
		for _, t := range current {
			if listed[t.Name] {
				order = append(order, t.Name)
			} else {
				rep.Unlisted = append(rep.Unlisted, t.Name)
			}
		}
		rep.Reordered = !slices.Equal(order, f.Tasks) || len(rep.Unlisted) > 0 && !prefixOf(current, f.Tasks)
	} else {
		rep.Reordered = len(f.Tasks) > 0
	}
	if o.DryRun {
		return rep, nil
	}

	// Write: the contest first, then each task, then the order.
	var contestID int64
	err = db.InTx(ctx, pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		if !exists {
			c, err := q.CreateContest(ctx, db.NewContestParams(f.Name, start, stop))
			if err != nil {
				return err
			}
			cur = c
		}
		contestID = cur.ID
		if len(rep.Settings) == 0 {
			return nil
		}
		up, err := merge(cur, f.Settings)
		if err != nil {
			return err
		}
		up.ID, up.Name = cur.ID, f.Name
		_, err = q.UpdateContest(ctx, up)
		return err
	})
	if err != nil {
		return nil, err
	}
	for i, tp := range plans {
		tc := &rep.Tasks[i]
		switch tc.Action {
		case "unchanged", "pending":
			continue
		case "activated":
			if err := q.SetActiveDataset(ctx, sqlc.SetActiveDatasetParams{ID: tp.task.ID, ActiveDatasetID: &tp.imported.ID}); err != nil {
				return rep, err
			}
			rep.LiveChanged = append(rep.LiveChanged, tp.task.ID)
			continue
		}
		desc := tagRe.ReplaceAllString(tp.pkg.Config.Dataset, "") + tag(tp.digest)
		opts := problempkg.ImportOptions{Description: desc, ContestID: &contestID}
		if tp.task != nil {
			opts = problempkg.ImportOptions{TaskID: tp.task.ID, Description: desc, Sync: true, Live: tc.Live}
		}
		res, err := problempkg.Import(ctx, pool, store, tp.pkg, opts)
		if err != nil {
			return rep, fmt.Errorf("tasks/%s: %w", tp.name, err)
		}
		tc.Dataset = res.Dataset
		if tp.task != nil && tp.task.ContestID == nil {
			rep.Reordered = true // attached below
		}
		if tc.Live && tp.task != nil {
			rep.LiveChanged = append(rep.LiveChanged, res.TaskID)
		}
	}
	if rep.Reordered {
		if err := reorder(ctx, pool, contestID, f.Tasks); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// merge overlays contest.yaml's settings on a contest (the columns it
// does not carry keep their values).
func merge(c sqlc.Contest, settings map[string]any) (sqlc.UpdateContestParams, error) {
	var up sqlc.UpdateContestParams
	b, err := json.Marshal(c)
	if err != nil {
		return up, err
	}
	merged := map[string]any{}
	if err := json.Unmarshal(b, &merged); err != nil {
		return up, err
	}
	for k, v := range settings {
		merged[k] = v
	}
	if b, err = json.Marshal(merged); err != nil {
		return up, err
	}
	if err := json.Unmarshal(b, &up); err != nil {
		return up, fmt.Errorf("%s: %w", FileName, err)
	}
	return up, nil
}

// prefixOf reports whether the listed tasks come first, in order, among
// the contest's tasks (unlisted ones may follow).
func prefixOf(current []sqlc.Task, listed []string) bool {
	if len(current) < len(listed) {
		return false
	}
	for i, n := range listed {
		if current[i].Name != n {
			return false
		}
	}
	return true
}

// times reads the start and stop a new contest needs.
func times(s map[string]any) (start, stop time.Time, err error) {
	get := func(k string) (time.Time, error) {
		switch v := s[k].(type) {
		case time.Time:
			return v, nil
		case string:
			return time.Parse(time.RFC3339, v)
		}
		return time.Time{}, fmt.Errorf("%s: a new contest needs %s (RFC 3339, e.g. 2030-07-01T09:00:00Z)", FileName, k)
	}
	if start, err = get("start_time"); err != nil {
		return
	}
	stop, err = get("stop_time")
	return
}

// current reports whether a task's live dataset already is the package
// (it was imported from this very content, or exporting it gives the same
// files: the directory was written by Export), and which dataset imported
// earlier holds the content when it is not live.
func current(ctx context.Context, q *sqlc.Queries, store blob.Store, t *sqlc.Task, digest string) (bool, *sqlc.Dataset, error) {
	all, err := q.ListDatasetsByTask(ctx, t.ID)
	if err != nil {
		return false, nil, err
	}
	var imported *sqlc.Dataset
	for i, d := range all {
		if strings.Contains(d.Description, tag(digest)) {
			if t.ActiveDatasetID != nil && d.ID == *t.ActiveDatasetID {
				return true, nil, nil
			}
			if imported == nil {
				imported = &all[i]
			}
		}
	}
	if t.ActiveDatasetID == nil {
		return false, imported, nil
	}
	var buf bytes.Buffer
	if err := problempkg.Export(ctx, q, store, t.ID, 0, &buf); err != nil {
		return false, nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		return false, nil, err
	}
	d, err := problempkg.DigestZip(zr)
	return d == digest, imported, err
}

// reorder numbers the listed tasks first, in order, and attaches those not
// yet in the contest; unlisted tasks keep their relative order after them.
func reorder(ctx context.Context, pool *pgxpool.Pool, contestID int64, names []string) error {
	return db.InTx(ctx, pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		current, err := q.ListTasksByContest(ctx, &contestID)
		if err != nil {
			return err
		}
		// Move everything out of the way of the (contest_id, num) unique
		// constraint first.
		for i, t := range current {
			n := int32(100000 + i)
			if err := q.SetTaskContest(ctx, sqlc.SetTaskContestParams{ID: t.ID, ContestID: &contestID, Num: &n}); err != nil {
				return err
			}
		}
		listed := map[string]bool{}
		var num int32
		for _, name := range names {
			t, err := q.GetTaskByName(ctx, name)
			if err != nil {
				return fmt.Errorf("task %s: %w", name, err)
			}
			n := num
			if err := q.SetTaskContest(ctx, sqlc.SetTaskContestParams{ID: t.ID, ContestID: &contestID, Num: &n}); err != nil {
				return err
			}
			listed[name] = true
			num++
		}
		for _, t := range current {
			if listed[t.Name] {
				continue
			}
			n := num
			if err := q.SetTaskContest(ctx, sqlc.SetTaskContestParams{ID: t.ID, ContestID: &contestID, Num: &n}); err != nil {
				return err
			}
			num++
		}
		return nil
	})
}
