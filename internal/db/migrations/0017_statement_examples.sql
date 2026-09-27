-- Statements are written in Markdown, LaTeX or HTML (rendered on the task
-- page and typeset to PDF) or uploaded as PDF (SPEC_IOI H1). The examples
-- shown in them belong to the task, not to a dataset: an input, its
-- expected output and an optional explanation (Markdown).
CREATE TABLE task_examples (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id       bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    position      integer NOT NULL,
    input_digest  sha256_digest NOT NULL,
    output_digest sha256_digest NOT NULL,
    note          text NOT NULL DEFAULT ''
);
-- CWS loads every task's examples of a contest at once, AWS one task's, in
-- order.
CREATE INDEX task_examples_task_idx ON task_examples (task_id, position);
