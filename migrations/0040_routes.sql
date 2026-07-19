-- 0040_routes: HTTP route inventory (ADR-0033). Routes declared in source (extracted by the route-map
-- capability) are the app's entry points. Cross-referenced with captured proxy traffic (observed=1) they
-- become confirmed-exposed routes, and a finding whose handler file matches a route is tied to that entry
-- point — refining the coarse project-level "exposed" signal to a specific route.

CREATE TABLE routes (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    method       TEXT NOT NULL DEFAULT '',   -- GET/POST/... or '' = any/unknown
    path         TEXT NOT NULL,              -- declared route path, e.g. /users/{id}
    handler_file TEXT NOT NULL DEFAULT '',   -- source file where the route/handler is declared
    handler_line INTEGER NOT NULL DEFAULT 0,
    framework    TEXT NOT NULL DEFAULT '',   -- flask / express / net-http / gin / ...
    source       TEXT NOT NULL DEFAULT '',   -- the capability that extracted it
    observed     INTEGER NOT NULL DEFAULT 0, -- 1 = matched captured traffic (confirmed exposed)
    updated_at   TEXT NOT NULL,
    UNIQUE (project_id, method, path)
);
CREATE INDEX idx_routes_project ON routes(project_id);
CREATE INDEX idx_routes_handler ON routes(project_id, handler_file);
