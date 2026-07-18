-- 0024_providers: registered LLM providers for the Analyst (the plan's `provider` entity). The
-- active provider is tracked in settings (`analyst.active_provider`); API keys are vault-sealed in
-- key_sealed (never stored in the clear, never in the environment).

CREATE TABLE providers (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL,               -- anthropic | openai | ollama | claude-cli | mock | …
    model      TEXT NOT NULL DEFAULT '',
    base_url   TEXT NOT NULL DEFAULT '',
    key_sealed TEXT NOT NULL DEFAULT '',     -- vault-sealed credential; empty for keyless providers
    created_at TEXT NOT NULL
);
