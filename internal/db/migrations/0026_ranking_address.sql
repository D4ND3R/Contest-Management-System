-- The public address of the ranking web site, set in the administration
-- (Server page) so the secret link of an administrator-only scoreboard
-- can be shown when cms.yaml has no ranking_web.public_url (installations
-- by ports). ":8001" means "the host of the page, port 8001".
ALTER TABLE server_settings ADD COLUMN ranking_url text NOT NULL DEFAULT '';
