-- Server-wide settings (SPEC_MIN §4): a single row. timezone is the zone
-- every site shows times in; administrators change it at any moment.
CREATE TABLE server_settings (
    id         boolean PRIMARY KEY DEFAULT true CHECK (id),
    timezone   text NOT NULL DEFAULT 'UTC',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Upgrades start from the zone of the latest contest, so pages keep
-- showing the times they showed.
INSERT INTO server_settings (timezone)
SELECT COALESCE((SELECT timezone FROM contests ORDER BY start_time DESC, id DESC LIMIT 1), 'UTC');

-- Every contest follows the server's zone, whatever creates or edits it
-- (forms, imports, cmsctl): contest pages, rankings and certificates read
-- contests.timezone. The lookup is the single-row primary key.
CREATE FUNCTION contests_follow_server_timezone() RETURNS trigger AS $$
BEGIN
    NEW.timezone := (SELECT timezone FROM server_settings WHERE id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER contests_timezone BEFORE INSERT OR UPDATE OF timezone ON contests
    FOR EACH ROW EXECUTE FUNCTION contests_follow_server_timezone();

UPDATE contests SET timezone = (SELECT timezone FROM server_settings WHERE id);
