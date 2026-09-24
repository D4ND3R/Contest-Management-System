-- Core schema of the Contest Management System.
--
-- Conventions:
--   * ids are bigint identity columns;
--   * durations are integer seconds (*_s) or milliseconds (*_ms);
--   * sizes are bytes;
--   * file contents live in the blob store and are referenced by their
--     SHA-256 hex digest (64 chars) — never stored here;
--   * every index carries a comment with the query it serves.

-- ---------------------------------------------------------------------------
-- Blobs: bookkeeping for the content-addressed store (size, first upload,
-- garbage collection). The content itself is in the blob backend.
CREATE TABLE blobs (
    digest      text PRIMARY KEY CHECK (digest ~ '^[0-9a-f]{64}$'),
    size        bigint NOT NULL CHECK (size >= 0),
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Domain for digest columns.
CREATE DOMAIN sha256_digest AS text CHECK (VALUE ~ '^[0-9a-f]{64}$');

-- ---------------------------------------------------------------------------
-- Programming languages, synchronised from config/languages/*.yaml.
CREATE TABLE languages (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    config     jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Administrators.
CREATE TABLE admins (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text NOT NULL,
    username      text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    enabled       boolean NOT NULL DEFAULT true,
    role          text NOT NULL DEFAULT 'all' CHECK (role IN ('all', 'messaging', 'read_only')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
CREATE TABLE contests (
    id                            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name                          text NOT NULL UNIQUE CHECK (name ~ '^[A-Za-z0-9_.-]+$'),
    description                   text NOT NULL DEFAULT '',
    -- UI localizations allowed (empty = all available).
    allowed_localizations         text[] NOT NULL DEFAULT '{}',
    -- Programming language ids allowed.
    languages                     text[] NOT NULL DEFAULT '{}',
    submissions_download_allowed  boolean NOT NULL DEFAULT true,
    allow_questions               boolean NOT NULL DEFAULT true,
    allow_user_tests              boolean NOT NULL DEFAULT true,
    allow_printing                boolean NOT NULL DEFAULT false,
    block_hidden_participations   boolean NOT NULL DEFAULT false,
    allow_password_authentication boolean NOT NULL DEFAULT true,
    ip_restriction                boolean NOT NULL DEFAULT false,
    ip_autologin                  boolean NOT NULL DEFAULT false,
    -- Block simultaneous logins of the same participation.
    single_login                  boolean NOT NULL DEFAULT false,

    token_mode           text NOT NULL DEFAULT 'disabled' CHECK (token_mode IN ('disabled', 'finite', 'infinite')),
    token_max_number     integer CHECK (token_max_number >= 0),
    token_min_interval_s bigint NOT NULL DEFAULT 0 CHECK (token_min_interval_s >= 0),
    token_gen_initial    integer NOT NULL DEFAULT 2 CHECK (token_gen_initial >= 0),
    token_gen_number     integer NOT NULL DEFAULT 2 CHECK (token_gen_number >= 0),
    token_gen_interval_s bigint NOT NULL DEFAULT 1800 CHECK (token_gen_interval_s > 0),
    token_gen_max        integer CHECK (token_gen_max >= 0),

    start_time           timestamptz NOT NULL,
    stop_time            timestamptz NOT NULL,
    analysis_enabled     boolean NOT NULL DEFAULT false,
    analysis_start       timestamptz,
    analysis_stop        timestamptz,
    timezone             text NOT NULL DEFAULT 'UTC',
    -- When set, each contestant has this many seconds from pressing "start".
    per_user_time_s      bigint CHECK (per_user_time_s > 0),

    max_submission_number     integer CHECK (max_submission_number >= 0),
    max_user_test_number      integer CHECK (max_user_test_number >= 0),
    min_submission_interval_s bigint CHECK (min_submission_interval_s >= 0),
    min_user_test_interval_s  bigint CHECK (min_user_test_interval_s >= 0),
    score_precision           integer NOT NULL DEFAULT 0 CHECK (score_precision BETWEEN 0 AND 6),

    scoring_mode         text NOT NULL DEFAULT 'ioi' CHECK (scoring_mode IN ('ioi', 'icpc')),
    icpc_penalty_minutes integer NOT NULL DEFAULT 20 CHECK (icpc_penalty_minutes >= 0),
    -- Public ranking freezes at this instant (null = never).
    ranking_freeze_time  timestamptz,
    ranking_unfrozen     boolean NOT NULL DEFAULT false,

    max_print_jobs  integer NOT NULL DEFAULT 10 CHECK (max_print_jobs >= 0),
    max_print_pages integer NOT NULL DEFAULT 20 CHECK (max_print_pages >= 0),

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (start_time <= stop_time),
    CHECK (analysis_start IS NULL OR analysis_stop IS NULL OR analysis_start <= analysis_stop),
    CHECK (token_mode <> 'finite' OR token_gen_initial IS NOT NULL)
);

-- ---------------------------------------------------------------------------
CREATE TABLE tasks (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    contest_id        bigint REFERENCES contests(id) ON DELETE SET NULL,
    num               integer,
    name              text NOT NULL UNIQUE CHECK (name ~ '^[A-Za-z0-9_.-]+$'),
    title             text NOT NULL,
    -- Languages whose statement is highlighted as official.
    primary_statements text[] NOT NULL DEFAULT '{}',
    -- Files a submission consists of, e.g. {"sol.%l"}; %l = language extension.
    submission_format text[] NOT NULL DEFAULT '{}',

    token_mode           text NOT NULL DEFAULT 'disabled' CHECK (token_mode IN ('disabled', 'finite', 'infinite')),
    token_max_number     integer CHECK (token_max_number >= 0),
    token_min_interval_s bigint NOT NULL DEFAULT 0 CHECK (token_min_interval_s >= 0),
    token_gen_initial    integer NOT NULL DEFAULT 2 CHECK (token_gen_initial >= 0),
    token_gen_number     integer NOT NULL DEFAULT 2 CHECK (token_gen_number >= 0),
    token_gen_interval_s bigint NOT NULL DEFAULT 1800 CHECK (token_gen_interval_s > 0),
    token_gen_max        integer CHECK (token_gen_max >= 0),

    max_submission_number     integer CHECK (max_submission_number >= 0),
    max_user_test_number      integer CHECK (max_user_test_number >= 0),
    min_submission_interval_s bigint CHECK (min_submission_interval_s >= 0),
    min_user_test_interval_s  bigint CHECK (min_user_test_interval_s >= 0),

    feedback_level  text NOT NULL DEFAULT 'full' CHECK (feedback_level IN ('full', 'restricted')),
    score_precision integer NOT NULL DEFAULT 0 CHECK (score_precision BETWEEN 0 AND 6),
    score_mode      text NOT NULL DEFAULT 'max_subtask' CHECK (score_mode IN ('max', 'max_subtask', 'max_tokened_last')),
    -- The live dataset (FK added below: datasets reference tasks).
    active_dataset_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (contest_id, num)
);
-- CWS/AWS: list the tasks of a contest in order (also covered by the
-- (contest_id, num) unique index, which serves ORDER BY num).

CREATE TABLE statements (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id      bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    language     text NOT NULL,
    digest       sha256_digest NOT NULL,
    content_type text NOT NULL DEFAULT 'application/pdf',
    UNIQUE (task_id, language)
);

CREATE TABLE attachments (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id  bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    filename text NOT NULL,
    digest   sha256_digest NOT NULL,
    UNIQUE (task_id, filename)
);

-- ---------------------------------------------------------------------------
CREATE TABLE datasets (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id            bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    description        text NOT NULL,
    -- Judge submissions on this (non-live) dataset in the background.
    autojudge          boolean NOT NULL DEFAULT false,
    time_limit_ms      integer CHECK (time_limit_ms > 0),
    -- null = max(2*TL, TL+1s) as computed by the dispatcher.
    wall_time_limit_ms integer CHECK (wall_time_limit_ms > 0),
    memory_limit_bytes bigint CHECK (memory_limit_bytes > 0),
    output_limit_bytes bigint NOT NULL DEFAULT 67108864 CHECK (output_limit_bytes > 0),
    process_limit      integer NOT NULL DEFAULT 1 CHECK (process_limit > 0),
    source_size_limit_bytes bigint CHECK (source_size_limit_bytes > 0),
    task_type          text NOT NULL DEFAULT 'Batch',
    task_type_params   jsonb NOT NULL DEFAULT '{}',
    score_type         text NOT NULL DEFAULT 'Sum',
    score_type_params  jsonb NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id, description)
);

ALTER TABLE tasks ADD CONSTRAINT tasks_active_dataset_fk
    FOREIGN KEY (active_dataset_id) REFERENCES datasets(id) ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE managers (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    dataset_id bigint NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    filename   text NOT NULL,
    digest     sha256_digest NOT NULL,
    UNIQUE (dataset_id, filename)
);

CREATE TABLE testcases (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    dataset_id    bigint NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    codename      text NOT NULL,
    public        boolean NOT NULL DEFAULT false,
    input_digest  sha256_digest NOT NULL,
    output_digest sha256_digest NOT NULL,
    UNIQUE (dataset_id, codename)
);

-- ---------------------------------------------------------------------------
CREATE TABLE users (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username            text NOT NULL UNIQUE,
    first_name          text NOT NULL DEFAULT '',
    last_name           text NOT NULL DEFAULT '',
    email               text NOT NULL DEFAULT '',
    password_hash       text NOT NULL,
    timezone            text,
    preferred_languages text[] NOT NULL DEFAULT '{}',
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE teams (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code         text NOT NULL UNIQUE,
    name         text NOT NULL,
    flag_digest  sha256_digest,
    photo_digest sha256_digest
);

CREATE TABLE participations (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    contest_id     bigint NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    user_id        bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id        bigint REFERENCES teams(id) ON DELETE SET NULL,
    -- Participation-specific password (overrides the user's one).
    password_hash  text,
    -- Allowed source networks (empty = any, unless contest.ip_restriction).
    ip             cidr[] NOT NULL DEFAULT '{}',
    starting_time  timestamptz,
    delay_time_s   bigint NOT NULL DEFAULT 0 CHECK (delay_time_s >= 0),
    extra_time_s   bigint NOT NULL DEFAULT 0 CHECK (extra_time_s >= 0),
    hidden         boolean NOT NULL DEFAULT false,
    unrestricted   boolean NOT NULL DEFAULT false,
    -- Incremented on every login; sessions carrying an older value are
    -- rejected when contest.single_login is set.
    login_nonce    bigint NOT NULL DEFAULT 0,
    UNIQUE (contest_id, user_id)
);
-- "Contests of a user" (login page, admin user view).
CREATE INDEX participations_user_idx ON participations (user_id);

-- ---------------------------------------------------------------------------
CREATE TABLE submissions (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participation_id bigint NOT NULL REFERENCES participations(id) ON DELETE CASCADE,
    task_id          bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    submitted_at     timestamptz NOT NULL DEFAULT now(),
    language         text,
    comment          text NOT NULL DEFAULT '',
    -- false for submissions made in analysis mode (not ranked).
    official         boolean NOT NULL DEFAULT true
);
-- CWS: a contestant's submissions to a task (listing, count and interval
-- limits); scoring: all submissions of (participation, task).
CREATE INDEX submissions_participation_task_idx ON submissions (participation_id, task_id, submitted_at);
-- AWS listings / reevaluation by task, newest first.
CREATE INDEX submissions_task_idx ON submissions (task_id, submitted_at DESC);
-- AWS: global listing ordered by time.
CREATE INDEX submissions_time_idx ON submissions (submitted_at DESC);

CREATE TABLE submission_files (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    submission_id bigint NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    filename      text NOT NULL,
    digest        sha256_digest NOT NULL,
    UNIQUE (submission_id, filename)
);

CREATE TABLE tokens (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    submission_id bigint NOT NULL UNIQUE REFERENCES submissions(id) ON DELETE CASCADE,
    played_at     timestamptz NOT NULL DEFAULT now()
);

-- Result of a submission on one dataset.
CREATE TABLE submission_results (
    submission_id bigint NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    dataset_id    bigint NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    -- Bumped whenever the result is invalidated; results of jobs from an
    -- older generation are discarded (idempotent, restart-safe queues).
    generation    integer NOT NULL DEFAULT 0,

    compilation_outcome   text CHECK (compilation_outcome IN ('ok', 'fail')),
    compilation_text      text NOT NULL DEFAULT '',
    compilation_stdout    text NOT NULL DEFAULT '',
    compilation_stderr    text NOT NULL DEFAULT '',
    compilation_tries     integer NOT NULL DEFAULT 0,
    compilation_time      double precision,
    compilation_wall_time double precision,
    compilation_memory    bigint,
    compilation_worker    text,

    evaluation_outcome text CHECK (evaluation_outcome IN ('ok')),
    evaluation_tries   integer NOT NULL DEFAULT 0,
    testcases_total    integer NOT NULL DEFAULT 0,
    testcases_done     integer NOT NULL DEFAULT 0,

    score                 double precision,
    score_details         jsonb,
    public_score          double precision,
    public_score_details  jsonb,
    -- Per-subtask scores shown in the ranking.
    ranking_score_details jsonb,
    scored_at             timestamptz,
    -- Set when the submission cannot be judged (system error); alerts admins.
    system_error          text,
    created_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (submission_id, dataset_id)
);
-- Dispatcher sweeper: results still in flight (small, partial).
CREATE INDEX submission_results_pending_idx ON submission_results (dataset_id, submission_id) WHERE scored_at IS NULL;
-- Reevaluation / statistics by dataset.
CREATE INDEX submission_results_dataset_idx ON submission_results (dataset_id);

CREATE TABLE executables (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    submission_id bigint NOT NULL,
    dataset_id    bigint NOT NULL,
    filename      text NOT NULL,
    digest        sha256_digest NOT NULL,
    FOREIGN KEY (submission_id, dataset_id) REFERENCES submission_results(submission_id, dataset_id) ON DELETE CASCADE,
    UNIQUE (submission_id, dataset_id, filename)
);

-- Outcome of one testcase of one submission result.
CREATE TABLE evaluations (
    submission_id  bigint NOT NULL,
    dataset_id     bigint NOT NULL,
    testcase_id    bigint NOT NULL REFERENCES testcases(id) ON DELETE CASCADE,
    -- Fraction of the testcase score in [0, 1].
    outcome        double precision NOT NULL,
    text           text NOT NULL DEFAULT '',
    execution_time      double precision,
    execution_wall_time double precision,
    execution_memory    bigint,
    exit_status    text NOT NULL,
    exit_code      integer,
    signal         integer,
    worker         text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (submission_id, dataset_id) REFERENCES submission_results(submission_id, dataset_id) ON DELETE CASCADE,
    PRIMARY KEY (submission_id, dataset_id, testcase_id)
);

-- ---------------------------------------------------------------------------
CREATE TABLE user_tests (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participation_id bigint NOT NULL REFERENCES participations(id) ON DELETE CASCADE,
    task_id          bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    submitted_at     timestamptz NOT NULL DEFAULT now(),
    language         text,
    input_digest     sha256_digest NOT NULL
);
-- CWS: a contestant's tests for a task (listing, limits).
CREATE INDEX user_tests_participation_task_idx ON user_tests (participation_id, task_id, submitted_at);

CREATE TABLE user_test_files (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_test_id bigint NOT NULL REFERENCES user_tests(id) ON DELETE CASCADE,
    filename     text NOT NULL,
    digest       sha256_digest NOT NULL,
    UNIQUE (user_test_id, filename)
);

CREATE TABLE user_test_results (
    user_test_id bigint NOT NULL REFERENCES user_tests(id) ON DELETE CASCADE,
    dataset_id   bigint NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    generation   integer NOT NULL DEFAULT 0,
    compilation_outcome   text CHECK (compilation_outcome IN ('ok', 'fail')),
    compilation_text      text NOT NULL DEFAULT '',
    compilation_stdout    text NOT NULL DEFAULT '',
    compilation_stderr    text NOT NULL DEFAULT '',
    compilation_tries     integer NOT NULL DEFAULT 0,
    compilation_time      double precision,
    compilation_wall_time double precision,
    compilation_memory    bigint,
    evaluation_outcome    text CHECK (evaluation_outcome IN ('ok')),
    evaluation_text       text NOT NULL DEFAULT '',
    evaluation_tries      integer NOT NULL DEFAULT 0,
    output_digest         sha256_digest,
    execution_time        double precision,
    execution_wall_time   double precision,
    execution_memory      bigint,
    exit_status           text,
    system_error          text,
    completed_at          timestamptz,
    PRIMARY KEY (user_test_id, dataset_id)
);
-- Dispatcher sweeper: user tests still in flight.
CREATE INDEX user_test_results_pending_idx ON user_test_results (user_test_id) WHERE completed_at IS NULL;

CREATE TABLE user_test_executables (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_test_id bigint NOT NULL,
    dataset_id   bigint NOT NULL,
    filename     text NOT NULL,
    digest       sha256_digest NOT NULL,
    FOREIGN KEY (user_test_id, dataset_id) REFERENCES user_test_results(user_test_id, dataset_id) ON DELETE CASCADE,
    UNIQUE (user_test_id, dataset_id, filename)
);

-- ---------------------------------------------------------------------------
-- Scores aggregated per (participation, task), maintained by the scoring
-- service; feeds the ranking and exports without rescanning submissions.
CREATE TABLE participation_task_scores (
    participation_id bigint NOT NULL REFERENCES participations(id) ON DELETE CASCADE,
    task_id          bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    score            double precision NOT NULL DEFAULT 0,
    subtask_scores   jsonb NOT NULL DEFAULT '[]',
    -- ICPC: solved flag, wrong attempts before the first AC, time of AC.
    icpc_solved      boolean NOT NULL DEFAULT false,
    icpc_attempts    integer NOT NULL DEFAULT 0,
    icpc_solved_at   timestamptz,
    -- Pending (not yet scored) submissions, shown while the ranking is live.
    pending          integer NOT NULL DEFAULT 0,
    last_submission_at timestamptz,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (participation_id, task_id)
);
-- Ranking/export by task.
CREATE INDEX participation_task_scores_task_idx ON participation_task_scores (task_id);

-- ---------------------------------------------------------------------------
-- Communication.
CREATE TABLE questions (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participation_id   bigint NOT NULL REFERENCES participations(id) ON DELETE CASCADE,
    asked_at           timestamptz NOT NULL DEFAULT now(),
    subject            text NOT NULL,
    text               text NOT NULL,
    reply_at           timestamptz,
    reply_subject      text,
    reply_text         text,
    reply_admin_id     bigint REFERENCES admins(id) ON DELETE SET NULL,
    ignored            boolean NOT NULL DEFAULT false
);
-- CWS: a contestant's questions.
CREATE INDEX questions_participation_idx ON questions (participation_id, asked_at);
-- AWS: unanswered questions first.
CREATE INDEX questions_unanswered_idx ON questions (asked_at) WHERE reply_at IS NULL AND NOT ignored;

CREATE TABLE announcements (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    contest_id bigint NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    subject    text NOT NULL,
    text       text NOT NULL,
    admin_id   bigint REFERENCES admins(id) ON DELETE SET NULL
);
-- CWS: announcements of a contest, newest first.
CREATE INDEX announcements_contest_idx ON announcements (contest_id, created_at DESC);

CREATE TABLE messages (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participation_id bigint NOT NULL REFERENCES participations(id) ON DELETE CASCADE,
    created_at       timestamptz NOT NULL DEFAULT now(),
    subject          text NOT NULL,
    text             text NOT NULL,
    admin_id         bigint REFERENCES admins(id) ON DELETE SET NULL
);
-- CWS: a contestant's private messages.
CREATE INDEX messages_participation_idx ON messages (participation_id, created_at DESC);

CREATE TABLE print_jobs (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participation_id bigint NOT NULL REFERENCES participations(id) ON DELETE CASCADE,
    created_at       timestamptz NOT NULL DEFAULT now(),
    filename         text NOT NULL,
    digest           sha256_digest NOT NULL,
    status           text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'printing', 'done', 'failed')),
    status_text      text NOT NULL DEFAULT '',
    pages            integer
);
-- CWS: a contestant's print jobs (listing and per-user limit).
CREATE INDEX print_jobs_participation_idx ON print_jobs (participation_id, created_at);
-- Printing service: next queued job.
CREATE INDEX print_jobs_queued_idx ON print_jobs (id) WHERE status = 'queued';

-- ---------------------------------------------------------------------------
-- Audit log of every administrative action.
CREATE TABLE audit_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    admin_id    bigint REFERENCES admins(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    action      text NOT NULL,
    target_type text NOT NULL DEFAULT '',
    target_id   bigint,
    details     jsonb NOT NULL DEFAULT '{}',
    ip          text NOT NULL DEFAULT ''
);
-- AWS: audit log listing, newest first (the PK serves ORDER BY id DESC);
-- filter by admin.
CREATE INDEX audit_log_admin_idx ON audit_log (admin_id, id DESC);
