CREATE TABLE IF NOT EXISTS knowledge_chunks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  rel_path TEXT NOT NULL,
  heading TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL,
  tsv tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(heading, '') || ' ' || coalesce(body, ''))
  ) STORED
);

CREATE INDEX IF NOT EXISTS knowledge_chunks_tsv_idx ON knowledge_chunks USING GIN (tsv);
CREATE INDEX IF NOT EXISTS knowledge_chunks_path_idx ON knowledge_chunks (rel_path);
