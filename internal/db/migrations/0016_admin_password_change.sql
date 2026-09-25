-- First-boot security: an administrator who logs in with a well-known
-- default password ("admin", the username, ...) must choose a new one
-- before anything else. Read with the admin row by primary key; no index.
ALTER TABLE admins ADD COLUMN password_change_required boolean NOT NULL DEFAULT false;
