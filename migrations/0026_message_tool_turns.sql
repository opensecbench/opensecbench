-- Canonical tool turns on messages (ADR-0017): an assistant turn carries the structured tool calls it
-- requested (tool_calls, a JSON array), and a "tool" turn carries the result it answers (tool_call_id)
-- plus whether that call failed (tool_error). Persisting these makes a thread vendor-portable — it can
-- move between providers that use different native tool-call formats.
ALTER TABLE messages ADD COLUMN tool_calls TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN tool_error INTEGER NOT NULL DEFAULT 0;
