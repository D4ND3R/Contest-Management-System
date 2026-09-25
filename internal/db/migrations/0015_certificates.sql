-- Certificates (SPEC_CLOSE E2): one template per contest; the PDF is built
-- on demand from the final ranking. Looked up by its primary key only.
CREATE TABLE certificate_templates (
    contest_id   bigint PRIMARY KEY REFERENCES contests(id) ON DELETE CASCADE,
    title        text NOT NULL,
    body         text NOT NULL,
    footer       text NOT NULL DEFAULT '',
    date_text    text NOT NULL DEFAULT '',
    -- [{"name": "...", "role": "..."}]
    signatures   jsonb NOT NULL DEFAULT '[]',
    -- [{"name": "Gold medal", "up_to_rank": 3}], ranks growing
    awards       jsonb NOT NULL DEFAULT '[]',
    min_score    double precision,
    only_awarded boolean NOT NULL DEFAULT false,
    logo_digest  sha256_digest,
    -- Contestants download their own certificate once their window closed.
    contestants_can_download boolean NOT NULL DEFAULT false,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
