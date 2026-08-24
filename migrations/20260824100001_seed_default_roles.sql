-- Migration: 20260824100001_seed_default_roles.sql
-- The two roles every company needs: exactly one owner, any number of
-- admins. A host project's own migrations can INSERT more rows into this
-- same table to extend the list — no Kiosk API needed for that, just
-- another migration (see spark's own database/*_seed_admin_user.sql for
-- the pattern this follows).

-- up
INSERT INTO roles (uuid, name, description, created_at, updated_at) VALUES
  ('df9202c9-466b-40e0-b434-23aaa6765af9', 'owner', 'Full control over the company, billing, and members.', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
  ('c53392c4-7511-4d45-bb06-fe8a595f1690', 'admin', 'Manage company members and settings.', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

-- down
DELETE FROM roles WHERE name IN ('owner', 'admin');
