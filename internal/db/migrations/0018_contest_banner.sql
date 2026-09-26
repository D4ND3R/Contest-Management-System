-- How a contest presents itself (SPEC_IOI H2): the banner at the top of the
-- contestant and admin dashboards shows a display title, where the contest
-- takes place, a short motto and an optional image.
ALTER TABLE contests ADD COLUMN title text NOT NULL DEFAULT '';
ALTER TABLE contests ADD COLUMN location text NOT NULL DEFAULT '';
ALTER TABLE contests ADD COLUMN tagline text NOT NULL DEFAULT '';
-- The banner image lives in the blob store (PNG, JPEG, GIF or WebP; never
-- SVG, which can carry scripts); its media type is kept to serve it.
ALTER TABLE contests ADD COLUMN banner_digest sha256_digest;
ALTER TABLE contests ADD COLUMN banner_type text NOT NULL DEFAULT '';
