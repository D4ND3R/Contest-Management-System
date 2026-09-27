-- A populated v0.2.1 database (schema at migration 0016): a contest with
-- sites, teams, tasks, datasets, testcases, 30 contestants, 180 judged
-- submissions, questions, announcements, tokens, adjustments, print jobs,
-- user tests, balloons, certificates and 101 audit log entries.

INSERT INTO blobs (digest, size) VALUES ('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 4), ('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 4);
INSERT INTO teams (code, name) VALUES ('MX', 'México');
INSERT INTO contests (name, start_time, stop_time, description, timezone, scoring_mode) VALUES
  ('omi2026', now() - interval '2 days', now() - interval '2 days' + interval '5 hours', 'OMI 2026 día 1', 'America/Mexico_City', 'ioi'),
  ('icpc', now() - interval '1 day', now() + interval '1 day', 'ICPC práctica', 'UTC', 'icpc');
INSERT INTO sites (contest_id, name) SELECT id, 'Sede CDMX' FROM contests WHERE name='omi2026';
INSERT INTO tasks (name, title, contest_id, num) SELECT 'suma', 'Suma', id, 0 FROM contests WHERE name='omi2026';
INSERT INTO tasks (name, title, contest_id, num, score_mode) SELECT 'grafo', 'Grafo', id, 1, 'max' FROM contests WHERE name='omi2026';
INSERT INTO tasks (name, title) VALUES ('suelta', 'Sin concurso');
INSERT INTO datasets (task_id, description, score_type, score_type_params) SELECT id, 'v1', 'GroupMin', '[[40,"a.*"],[60,"b.*"]]' FROM tasks;
UPDATE tasks t SET active_dataset_id = d.id FROM datasets d WHERE d.task_id = t.id;
INSERT INTO testcases (dataset_id, codename, input_digest, output_digest, public) SELECT d.id, c, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', c = 'a1' FROM datasets d, unnest(array['a1','a2','b1']) c;
INSERT INTO statements (task_id, language, digest) SELECT id, 'es', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' FROM tasks;
INSERT INTO attachments (task_id, filename, digest) SELECT id, 'ejemplo.txt', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' FROM tasks WHERE name='suma';
INSERT INTO managers (dataset_id, filename, digest) SELECT id, 'checker', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' FROM datasets LIMIT 1;
INSERT INTO users (username, password_hash, first_name, last_name, email, preferred_languages) SELECT 'u'||i, 'x', 'Nombre'||i, 'Ápellido', 'u'||i||'@ej.mx', '{es}' FROM generate_series(1,30) i;
INSERT INTO participations (contest_id, user_id, ip, hidden, team_id, site_id)
  SELECT c.id, u.id, '{}', u.username = 'u30', (SELECT id FROM teams), (SELECT id FROM sites) FROM contests c, users u WHERE c.name='omi2026';
INSERT INTO participations (contest_id, user_id, ip) SELECT c.id, u.id, '{}' FROM contests c, users u WHERE c.name='icpc' AND u.username IN ('u1','u2');
INSERT INTO submissions (participation_id, task_id, submitted_at, language, official)
  SELECT p.id, t.id, c.start_time + (s || ' minutes')::interval, 'cpp17', true
  FROM participations p JOIN contests c ON c.id = p.contest_id JOIN tasks t ON t.contest_id = c.id, generate_series(1,3) s;
INSERT INTO submission_files (submission_id, filename, digest) SELECT id, 'suma.%l', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' FROM submissions;
INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, score, score_details, public_score, public_score_details, ranking_score_details, scored_at, evaluation_outcome, verdict, testcases_total, testcases_done)
  SELECT s.id, t.active_dataset_id, 'ok', 40, '{"type":"group"}', 0, '{"type":"group"}', '[40,0]', now(), 'ok', 'WA', 3, 3 FROM submissions s JOIN tasks t ON t.id = s.task_id;
INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome, exit_status, text) SELECT r.submission_id, r.dataset_id, tc.id, 1, 'ok', 'Output is correct' FROM submission_results r JOIN testcases tc ON tc.dataset_id = r.dataset_id;
INSERT INTO executables (submission_id, dataset_id, filename, digest) SELECT submission_id, dataset_id, 'suma', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' FROM submission_results;
INSERT INTO participation_task_scores (participation_id, task_id, score, subtask_scores, last_submission_at) SELECT p.id, t.id, 40, '[40,0]', now() FROM participations p JOIN tasks t ON t.contest_id = p.contest_id;
INSERT INTO tokens (submission_id) SELECT min(id) FROM submissions;
INSERT INTO score_adjustments (participation_id, task_id, points, reason) SELECT p.id, t.id, 5, 'Error del enunciado' FROM participations p JOIN tasks t ON t.contest_id = p.contest_id LIMIT 2;
INSERT INTO questions (participation_id, contest_id, subject, text, reply_text, reply_at) SELECT p.id, p.contest_id, '¿N máximo?', '¿Cuál es el N?', 'Lee el enunciado', now() FROM participations p LIMIT 3;
INSERT INTO announcements (contest_id, subject, text) SELECT id, 'Aviso', 'Se extiende 10 minutos' FROM contests;
INSERT INTO messages (participation_id, subject, text) SELECT id, 'Hola', 'Mensaje privado' FROM participations LIMIT 2;
INSERT INTO print_jobs (participation_id, filename, digest) SELECT id, 'sol.cpp', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' FROM participations LIMIT 2;
INSERT INTO user_tests (participation_id, task_id, input_digest, language) SELECT p.id, t.id, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'cpp17' FROM participations p JOIN tasks t ON t.contest_id = p.contest_id LIMIT 3;
INSERT INTO user_test_files (user_test_id, filename, digest) SELECT id, 'suma.%l', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' FROM user_tests;
INSERT INTO user_test_results (user_test_id, dataset_id) SELECT u.id, t.active_dataset_id FROM user_tests u JOIN tasks t ON t.id = u.task_id;
INSERT INTO balloons (task_id, recipient) SELECT t.id, 'u1' FROM tasks t WHERE t.name='suma';
INSERT INTO certificate_templates (contest_id, title, body) SELECT id, 'Constancia', '# {name}' FROM contests WHERE name='omi2026';
INSERT INTO audit_log (admin_id, action, target_type, target_id, details, ip) SELECT (SELECT id FROM admins LIMIT 1), a, 'contest', (SELECT id FROM contests LIMIT 1), jsonb_build_object('nombre', 'OMI ñ', 'n', i), '10.0.0.1' FROM generate_series(1,50) i, unnest(array['contest.update','task.create']) a;
INSERT INTO audit_log (admin_id, action, details, ip) VALUES (NULL, 'login.failed', '{}', '');

