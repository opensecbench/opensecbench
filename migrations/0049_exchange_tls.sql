-- ADR-0007/review #6: capture the upstream TLS cert summary onto a proxied HTTPS exchange.
ALTER TABLE http_exchanges ADD COLUMN tls TEXT NOT NULL DEFAULT '';
