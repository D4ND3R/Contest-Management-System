-- Contest operations (SPEC_IOI H5).

-- Emergency controls: submissions (and user tests) paused for the whole
-- contest, with a message for the contestants; one task closed.
ALTER TABLE contests ADD COLUMN submissions_paused boolean NOT NULL DEFAULT false;
ALTER TABLE contests ADD COLUMN pause_message text NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN submissions_closed boolean NOT NULL DEFAULT false;

-- Tamper evidence (SPEC_IOI §9.4, §13): the audit log is append-only and
-- hash-chained. Each entry stores its place in the chain (seq), the hash of
-- the previous entry and its own hash over its content and that previous
-- hash, so changing, removing or reordering entries breaks every later
-- hash ("cms ctl audit-verify"; the head hash can be written down or
-- published during the contest). actor keeps who acted by name (the
-- administrator's username, or "contestant:<username>" for submission
-- receipts): admin_id is set to NULL when an administrator is deleted and
-- is therefore not part of the hash.
ALTER TABLE audit_log ADD COLUMN actor text NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN seq bigint;
ALTER TABLE audit_log ADD COLUMN prev_hash text NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN hash text;
-- The chain head (the entry with the largest seq) is read on every insert,
-- and verification walks the chain in order.
CREATE UNIQUE INDEX audit_log_seq_idx ON audit_log (seq);

CREATE FUNCTION audit_hash(prev text, seq bigint, actor text, action text, target_type text, target_id bigint,
                           details jsonb, ip text, created_at timestamptz) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT encode(sha256(convert_to(concat_ws(chr(31), prev, seq::text, actor, action, target_type,
      coalesce(target_id::text, ''), details::text, ip,
      to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')), 'UTF8')), 'hex')
$$;

UPDATE audit_log a SET actor = ad.username FROM admins ad WHERE ad.id = a.admin_id;
DO $$
DECLARE
  r record;
  n bigint := 0;
  h text := '';
BEGIN
  FOR r IN SELECT * FROM audit_log ORDER BY id LOOP
    n := n + 1;
    UPDATE audit_log SET seq = n, prev_hash = h,
      hash = audit_hash(h, n, r.actor, r.action, r.target_type, r.target_id, r.details, r.ip, r.created_at)
    WHERE id = r.id RETURNING audit_log.hash INTO h;
  END LOOP;
END $$;

-- New entries join the chain one at a time (a transaction-level advisory
-- lock orders concurrent inserts; submissions hold it only for their last
-- statement). An entry that arrives already chained is a backup being
-- restored: it keeps its values, and verification checks them.
CREATE FUNCTION audit_log_chain() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  last_seq bigint;
  last_hash text;
BEGIN
  IF NEW.hash IS NOT NULL THEN
    RETURN NEW;
  END IF;
  PERFORM pg_advisory_xact_lock(7331001);
  IF NEW.actor = '' AND NEW.admin_id IS NOT NULL THEN
    NEW.actor := coalesce((SELECT username FROM admins WHERE id = NEW.admin_id), '');
  END IF;
  SELECT seq, hash INTO last_seq, last_hash FROM audit_log WHERE seq IS NOT NULL ORDER BY seq DESC LIMIT 1;
  NEW.seq := coalesce(last_seq, 0) + 1;
  NEW.prev_hash := coalesce(last_hash, '');
  NEW.hash := audit_hash(NEW.prev_hash, NEW.seq, NEW.actor, NEW.action, NEW.target_type, NEW.target_id,
                         NEW.details, NEW.ip, NEW.created_at);
  RETURN NEW;
END $$;
CREATE TRIGGER audit_log_chain BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION audit_log_chain();

-- Nothing is changed or removed; the only update allowed is the foreign
-- key clearing admin_id when an administrator is deleted.
CREATE FUNCTION audit_log_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.admin_id IS NULL AND (to_jsonb(NEW) - 'admin_id') = (to_jsonb(OLD) - 'admin_id') THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'the audit log is append-only (%)', TG_OP;
END $$;
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
  FOR EACH STATEMENT EXECUTE FUNCTION audit_log_append_only();

-- Roles (SPEC_IOI §9.2, §9.4): task setters prepare tasks (statements,
-- datasets, testcases, graders, the tester); delegation leaders see their
-- team's contestants and nothing else. No index on team_id: the admins
-- table has a handful of rows.
ALTER TABLE admins DROP CONSTRAINT admins_role_check;
ALTER TABLE admins ADD CONSTRAINT admins_role_check CHECK (role IN ('all', 'messaging', 'task_setter', 'read_only', 'leader'));
ALTER TABLE admins ADD COLUMN team_id bigint REFERENCES teams(id) ON DELETE SET NULL;
