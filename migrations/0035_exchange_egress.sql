-- 0035_exchange_egress: record which vantage a Replay/proxy send went out from (ADR-0025). '' = the
-- control-plane host (default); otherwise the enrolled runner id that performed the outbound request.
-- Real assessment provenance: the exchange history shows where each request originated.

ALTER TABLE http_exchanges ADD COLUMN egress TEXT NOT NULL DEFAULT '';
