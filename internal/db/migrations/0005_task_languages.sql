-- Languages allowed for a task (K10). Empty: every language of the contest.
ALTER TABLE tasks ADD COLUMN languages text[] NOT NULL DEFAULT '{}';
