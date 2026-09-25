package problempkg

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// statementExt is the file extension of a statement content type.
func statementExt(ct string) string {
	switch {
	case strings.HasPrefix(ct, "text/html"):
		return ".html"
	case strings.HasPrefix(ct, "text/markdown"):
		return ".md"
	case strings.HasPrefix(ct, "text/plain"):
		return ".txt"
	}
	return ".pdf"
}

// managerPath places a manager: checker/interactor/manager executables and
// sources at the root, everything else (graders, stubs, headers) in
// graders/.
func managerPath(name string) string {
	ext := path.Ext(name)
	if managerRoots[strings.TrimSuffix(name, ext)] && (ext == "" || ext == ".c" || ext == ".cpp" || ext == ".cc") {
		return name
	}
	return "graders/" + name
}

// Export writes a task with one of its datasets (the live one when
// datasetID is 0) as a package, including the reference solutions
// imported with earlier packages.
func Export(ctx context.Context, q *sqlc.Queries, store blob.Store, taskID, datasetID int64, w io.Writer) error {
	t, err := q.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if datasetID == 0 {
		if t.ActiveDatasetID == nil {
			return fmt.Errorf("the task has no live dataset")
		}
		datasetID = *t.ActiveDatasetID
	}
	d, err := q.GetDataset(ctx, datasetID)
	if err != nil || d.TaskID != t.ID {
		return fmt.Errorf("dataset %d is not a dataset of %s", datasetID, t.Name)
	}
	tcs, err := q.ListTestcases(ctx, d.ID)
	if err != nil {
		return err
	}
	codes, public := make([]string, len(tcs)), make([]bool, len(tcs))
	for i, tc := range tcs {
		codes[i], public[i] = tc.Codename, tc.Public
	}
	c, err := ConfigFromCMS(t, d, codes, public)
	if err != nil {
		return err
	}
	pw := NewWriter(w)
	if err := pw.WriteConfig(c); err != nil {
		return err
	}
	copyBlob := func(name, digest string) error {
		rd, err := store.Open(ctx, digest)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		defer rd.Close()
		return pw.Add(name, rd)
	}
	stmts, err := q.ListStatements(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, s := range stmts {
		if err := copyBlob("statement/"+s.Language+statementExt(s.ContentType), s.Digest); err != nil {
			return err
		}
	}
	for _, tc := range tcs {
		if err := copyBlob("tests/"+tc.Codename+".in", tc.InputDigest); err != nil {
			return err
		}
		if err := copyBlob("tests/"+tc.Codename+".out", tc.OutputDigest); err != nil {
			return err
		}
	}
	managers, err := q.ListManagers(ctx, d.ID)
	if err != nil {
		return err
	}
	for _, m := range managers {
		if err := copyBlob(managerPath(m.Filename), m.Digest); err != nil {
			return err
		}
	}
	atts, err := q.ListAttachments(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, a := range atts {
		if err := copyBlob("attachments/"+a.Filename, a.Digest); err != nil {
			return err
		}
	}
	sols, err := q.ListPackageSolutionFiles(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, s := range sols {
		p := "solutions/" + strings.TrimPrefix(s.Comment, "solutions/")
		if s.OutputOnly {
			p += "/" + s.Filename
		}
		if err := copyBlob(p, s.Digest); err != nil {
			return err
		}
	}
	return pw.Close()
}
