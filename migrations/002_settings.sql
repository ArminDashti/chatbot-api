CREATE TABLE IF NOT EXISTS app_settings (
  id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  chat_base_url TEXT NOT NULL DEFAULT '',
  chat_api_key TEXT NOT NULL DEFAULT '',
  chat_model TEXT NOT NULL DEFAULT 'auto',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO app_settings (id, chat_base_url, chat_api_key, chat_model)
VALUES (1, '', '', 'auto')
ON CONFLICT (id) DO NOTHING;
