-- Invalidated submissions (SPEC_CLOSE A3): excluded from every score and
-- the ranking, with the reason shown to the contestant; reversible.
ALTER TABLE submissions ADD COLUMN invalidated_at timestamptz;
ALTER TABLE submissions ADD COLUMN invalidated_reason text NOT NULL DEFAULT '';
ALTER TABLE submissions ADD COLUMN invalidated_by bigint REFERENCES admins(id) ON DELETE SET NULL;
