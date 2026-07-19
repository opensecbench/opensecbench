-- 0042_corpus_chunks: semantic retrieval index for the project corpus + KB (ADR-0039). Each row is a text
-- chunk of a source (a corpus context item or a KB entry) with its embedding vector. Retrieval loads a
-- project's chunks and ranks them by cosine similarity in Go (modernc SQLite has no vector extension). The
-- `embedding` BLOB is the vector as little-endian float32 bytes — the schema's first binary column.

CREATE TABLE corpus_chunks (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL,             -- 'context' (corpus doc) | 'kb' (knowledge-base entry)
    source_id   TEXT NOT NULL,             -- the context_item / kb_entry id
    source_name TEXT NOT NULL DEFAULT '',  -- human label (doc name / KB title) for the retrieval result
    chunk_index INTEGER NOT NULL,
    text        TEXT NOT NULL,
    embedding   BLOB NOT NULL,             -- []float32 little-endian
    dim         INTEGER NOT NULL,
    model       TEXT NOT NULL DEFAULT '',  -- the embedding backend id (provenance)
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_corpus_chunks_project ON corpus_chunks(project_id);
CREATE INDEX idx_corpus_chunks_source ON corpus_chunks(project_id, source_kind, source_id);
