package contestweb

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/langs"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
	"github.com/D4ND3R/Contest-Management-System/internal/tasktypes"
	"github.com/jackc/pgx/v5"
)

// contestView is the cached, read-mostly state of a contest: settings,
// tasks with their live dataset limits, statements and attachments.
type contestView struct {
	sqlc.Contest
	Loc        *time.Location
	Rules      contest.Rules
	Tasks      []*taskView
	TaskByName map[string]*taskView
	TaskByID   map[int64]*taskView
	Languages  []*langs.Language
	loaded     time.Time
}

type statementView struct {
	Lang, Name, Digest, ContentType string
	Primary                         bool
}

type taskView struct {
	sqlc.Task
	Dataset       *sqlc.Dataset
	Statements    []statementView
	Attachments   []string
	AttachDigest  map[string]string
	MaxScore      float64
	Precision     int
	Formats       []string
	NeedsLanguage bool
	TimeLimit     time.Duration
	MemoryLimit   int64
	TaskType      string
	SourceLimit   int64
}

var errNotFound = errors.New("not found")

// cache holds contest views (by name) and participations (by id).
type cache struct {
	q     *sqlc.Queries
	langs *langs.Registry
	ttl   time.Duration

	mu       sync.Mutex
	contests map[string]*contestView
	parts    map[int64]*partEntry
}

type partEntry struct {
	p      sqlc.GetParticipationViewRow
	loaded time.Time
}

func newCache(q *sqlc.Queries, reg *langs.Registry, ttl time.Duration) *cache {
	return &cache{q: q, langs: reg, ttl: ttl, contests: map[string]*contestView{}, parts: map[int64]*partEntry{}}
}

// invalidateContest drops a contest (by id; 0 = all).
func (c *cache) invalidateContest(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, cv := range c.contests {
		if id == 0 || cv.ID == id {
			delete(c.contests, name)
		}
	}
}

func (c *cache) invalidateParticipation(id int64) {
	c.mu.Lock()
	delete(c.parts, id)
	c.mu.Unlock()
}

func (c *cache) participation(ctx context.Context, id int64) (sqlc.GetParticipationViewRow, error) {
	c.mu.Lock()
	e, ok := c.parts[id]
	c.mu.Unlock()
	if ok && time.Since(e.loaded) < c.ttl {
		return e.p, nil
	}
	p, err := c.q.GetParticipationView(ctx, id)
	if err != nil {
		return p, err
	}
	c.mu.Lock()
	if len(c.parts) > 50000 {
		c.parts = map[int64]*partEntry{}
	}
	c.parts[id] = &partEntry{p: p, loaded: time.Now()}
	c.mu.Unlock()
	return p, nil
}

func (c *cache) contest(ctx context.Context, name string) (*contestView, error) {
	c.mu.Lock()
	cv, ok := c.contests[name]
	c.mu.Unlock()
	if ok && time.Since(cv.loaded) < c.ttl {
		return cv, nil
	}
	cv, err := c.load(ctx, name)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.contests[name] = cv
	c.mu.Unlock()
	return cv, nil
}

func (c *cache) load(ctx context.Context, name string) (*contestView, error) {
	ct, err := c.q.GetContestByName(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	cv := &contestView{Contest: ct, TaskByName: map[string]*taskView{}, TaskByID: map[int64]*taskView{}, loaded: time.Now()}
	cv.Loc, err = time.LoadLocation(ct.Timezone)
	if err != nil {
		cv.Loc = time.UTC
	}
	cv.Rules = contest.Rules{Start: ct.StartTime, Stop: ct.StopTime, AnalysisEnabled: ct.AnalysisEnabled,
		AnalysisStart: ct.AnalysisStart, AnalysisStop: ct.AnalysisStop}
	if ct.PerUserTimeS != nil {
		cv.Rules.PerUserTime = time.Duration(*ct.PerUserTimeS) * time.Second
	}
	for _, l := range c.langs.All() {
		if len(ct.Languages) == 0 || contains(ct.Languages, l.ID) {
			cv.Languages = append(cv.Languages, l)
		}
	}
	tasks, err := c.q.ListTasksByContest(ctx, &ct.ID)
	if err != nil {
		return nil, err
	}
	datasets, err := c.q.ListLiveDatasetsByContest(ctx, &ct.ID)
	if err != nil {
		return nil, err
	}
	dsByTask := map[int64]*sqlc.Dataset{}
	var dsIDs []int64
	for i := range datasets {
		dsByTask[datasets[i].TaskID] = &datasets[i]
		dsIDs = append(dsIDs, datasets[i].ID)
	}
	tcs, err := c.q.ListTestcasesByDatasets(ctx, dsIDs)
	if err != nil {
		return nil, err
	}
	tcByDS := map[int64][]sqlc.Testcase{}
	for _, tc := range tcs {
		tcByDS[tc.DatasetID] = append(tcByDS[tc.DatasetID], tc)
	}
	stmts, err := c.q.ListStatementsByContest(ctx, &ct.ID)
	if err != nil {
		return nil, err
	}
	atts, err := c.q.ListAttachmentsByContest(ctx, &ct.ID)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		tv := &taskView{Task: t, AttachDigest: map[string]string{}, Precision: int(t.ScorePrecision), Formats: t.SubmissionFormat}
		for _, f := range t.SubmissionFormat {
			if containsSuffix(f, ".%l") {
				tv.NeedsLanguage = true
			}
		}
		if ds := dsByTask[t.ID]; ds != nil {
			tv.Dataset = ds
			tv.TaskType = ds.TaskType
			if ds.TaskType == "OutputOnly" && len(tv.Formats) == 0 {
				// One output file per testcase, named after the pattern.
				pattern, _, _ := tasktypes.OutputOnlyConfig(ds.TaskTypeParams)
				for _, tc := range tcByDS[ds.ID] {
					tv.Formats = append(tv.Formats, tasktypes.OutputFileName(pattern, tc.Codename))
				}
				sort.Strings(tv.Formats)
			}
			if ds.TimeLimitMs != nil {
				tv.TimeLimit = time.Duration(*ds.TimeLimitMs) * time.Millisecond
			}
			if ds.MemoryLimitBytes != nil {
				tv.MemoryLimit = *ds.MemoryLimitBytes
			}
			if ds.SourceSizeLimitBytes != nil {
				tv.SourceLimit = *ds.SourceSizeLimitBytes
			}
			var codes []string
			var pub []bool
			for _, tc := range tcByDS[ds.ID] {
				codes = append(codes, tc.Codename)
				pub = append(pub, tc.Public)
			}
			if st, err := scoring.New(ds.ScoreType, ds.ScoreTypeParams, codes, pub, tv.Precision); err == nil {
				tv.MaxScore = st.MaxScore()
			}
		}
		cv.Tasks = append(cv.Tasks, tv)
		cv.TaskByName[t.Name] = tv
		cv.TaskByID[t.ID] = tv
	}
	for _, s := range stmts {
		if tv := cv.TaskByID[s.TaskID]; tv != nil {
			tv.Statements = append(tv.Statements, statementView{Lang: s.Language, Name: langName(s.Language),
				Digest: s.Digest, ContentType: s.ContentType, Primary: contains(tv.PrimaryStatements, s.Language)})
		}
	}
	for _, a := range atts {
		if tv := cv.TaskByID[a.TaskID]; tv != nil {
			tv.Attachments = append(tv.Attachments, a.Filename)
			tv.AttachDigest[a.Filename] = a.Digest
		}
	}
	for _, tv := range cv.Tasks {
		sort.SliceStable(tv.Statements, func(i, j int) bool { return tv.Statements[i].Primary && !tv.Statements[j].Primary })
	}
	return cv, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

var statementLangNames = map[string]string{
	"en": "English", "es": "Español", "pt": "Português", "fr": "Français", "it": "Italiano", "de": "Deutsch",
	"ru": "Русский", "zh": "中文", "ja": "日本語", "ko": "한국어", "ar": "العربية", "ca": "Català", "eu": "Euskara",
}

func langName(code string) string {
	if n, ok := statementLangNames[code]; ok {
		return n + " (" + code + ")"
	}
	return code
}
