// Package auditlog verifies the hash chain of the audit log (SPEC_IOI
// §9.4, §13; migration 0021 builds it in the database).
package auditlog

import (
	"context"
	"errors"
	"fmt"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// Report is the outcome of a verification.
type Report struct {
	Entries int64
	// HeadSeq and HeadHash identify the latest entry: written down or
	// published, they let anyone check later that nothing before them
	// changed.
	HeadSeq  int64
	HeadHash string
	// Problems lists the first inconsistencies found (at most MaxProblems).
	Problems []string
	// More counts the inconsistencies beyond those listed.
	More int
}

// OK reports whether the chain is intact.
func (r *Report) OK() bool { return len(r.Problems) == 0 }

// MaxProblems bounds Report.Problems.
const MaxProblems = 20

const page = 5000

// Verify walks the whole chain: every entry's hash must match its content
// and the previous entry's hash, and seq must have no gaps.
func Verify(ctx context.Context, q *sqlc.Queries) (*Report, error) {
	rep := &Report{}
	add := func(format string, args ...any) {
		if len(rep.Problems) < MaxProblems {
			rep.Problems = append(rep.Problems, fmt.Sprintf(format, args...))
		} else {
			rep.More++
		}
	}
	if n, err := q.CountUnchainedAudit(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		add("%d entries are outside the chain", n)
	}
	var last int64
	prev := ""
	for {
		rows, err := q.AuditChain(ctx, sqlc.AuditChainParams{AfterSeq: last, MaxRows: page})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Seq != last+1 {
				add("entries %d to %d are missing", last+1, r.Seq-1)
			}
			if r.PrevHash != prev {
				add("entry %d (id %d) does not follow the previous entry", r.Seq, r.ID)
			}
			if r.Hash != r.Expected {
				add("entry %d (id %d) was modified", r.Seq, r.ID)
			}
			last, prev = r.Seq, r.Hash
			rep.Entries++
		}
		if len(rows) < page {
			break
		}
	}
	head, err := q.AuditHead(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		rep.HeadSeq, rep.HeadHash = head.Seq, head.Hash
	}
	return rep, nil
}
