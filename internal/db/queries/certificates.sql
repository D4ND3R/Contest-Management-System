-- name: GetCertificateTemplate :one
SELECT * FROM certificate_templates WHERE contest_id = $1;

-- name: UpsertCertificateTemplate :exec
INSERT INTO certificate_templates (contest_id, title, body, footer, date_text, signatures, awards, min_score,
    only_awarded, logo_digest, contestants_can_download, updated_at)
VALUES (sqlc.arg(contest_id), sqlc.arg(title), sqlc.arg(body), sqlc.arg(footer), sqlc.arg(date_text), sqlc.arg(signatures),
    sqlc.arg(awards), sqlc.narg(min_score), sqlc.arg(only_awarded), sqlc.narg(logo_digest), sqlc.arg(contestants_can_download), now())
ON CONFLICT (contest_id) DO UPDATE SET title = EXCLUDED.title, body = EXCLUDED.body, footer = EXCLUDED.footer,
    date_text = EXCLUDED.date_text, signatures = EXCLUDED.signatures, awards = EXCLUDED.awards,
    min_score = EXCLUDED.min_score, only_awarded = EXCLUDED.only_awarded, logo_digest = EXCLUDED.logo_digest,
    contestants_can_download = EXCLUDED.contestants_can_download, updated_at = now();
