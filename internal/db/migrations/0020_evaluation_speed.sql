-- Evaluation performance (SPEC_IOI H4).
--
-- Compilations are reused: a successful one is remembered under the hash of
-- everything it depends on (language and commands, task type, sources,
-- graders); an identical job later (a resubmission, a re-evaluation, the
-- same code in another dataset) takes its executables without running the
-- compiler again.
CREATE TABLE compilation_cache (
    key        text PRIMARY KEY,
    text       text NOT NULL,
    stdout     text NOT NULL DEFAULT '',
    stderr     text NOT NULL DEFAULT '',
    time       double precision NOT NULL DEFAULT 0,
    wall_time  double precision NOT NULL DEFAULT 0,
    memory     bigint NOT NULL DEFAULT 0,
    hits       bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE compilation_cache_files (
    key      text NOT NULL REFERENCES compilation_cache(key) ON DELETE CASCADE,
    filename text NOT NULL,
    digest   sha256_digest NOT NULL,
    size     bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (key, filename)
);
-- The blob garbage collector first drops entries unused for a week (used_at).
CREATE INDEX compilation_cache_used_idx ON compilation_cache (used_at);

-- Short-circuit: in a GroupMin/GroupMul subtask, once a testcase scores 0
-- the others cannot change its score; with this option they are skipped.
ALTER TABLE datasets ADD COLUMN short_circuit boolean NOT NULL DEFAULT false;
