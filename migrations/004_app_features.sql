ALTER TABLE app_settings
  ADD COLUMN IF NOT EXISTS allowed_folders TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];

ALTER TABLE conversations
  ADD COLUMN IF NOT EXISTS device TEXT NOT NULL DEFAULT '';

INSERT INTO groups (name)
VALUES ('Default')
ON CONFLICT (name) DO NOTHING;

INSERT INTO group_rules (group_id, body)
SELECT g.id, ''
FROM groups g
WHERE g.name = 'Default'
ON CONFLICT (group_id) DO NOTHING;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS group_id UUID REFERENCES groups(id);

UPDATE users
SET group_id = (SELECT id FROM groups WHERE name = 'Default' LIMIT 1)
WHERE group_id IS NULL;

ALTER TABLE users
  ALTER COLUMN group_id SET NOT NULL;

DELETE FROM group_members;

INSERT INTO group_members (group_id, user_id)
SELECT u.group_id, u.id
FROM users u
WHERE u.group_id IS NOT NULL
ON CONFLICT DO NOTHING;

CREATE UNIQUE INDEX IF NOT EXISTS group_members_user_id_uidx
  ON group_members (user_id);

CREATE TABLE IF NOT EXISTS message_feedback (
  message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('like', 'dislike')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (message_id, user_id)
);
