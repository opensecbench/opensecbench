-- ADR-0051: the engagement record — the frame of an assessment (identity, scope posture, rules of
-- engagement, timeline, contacts, reporting). One row per project; every column optional so a project
-- without an engagement row behaves exactly as before. Repeated dates are ISO strings kept as TEXT.
CREATE TABLE engagement (
	project_id     TEXT PRIMARY KEY,
	kinds          TEXT NOT NULL DEFAULT '',
	objective      TEXT NOT NULL DEFAULT '',
	reference      TEXT NOT NULL DEFAULT '',
	environment    TEXT NOT NULL DEFAULT '',
	data_class     TEXT NOT NULL DEFAULT '',
	standard       TEXT NOT NULL DEFAULT '',
	compliance     TEXT NOT NULL DEFAULT '',
	severity_scale TEXT NOT NULL DEFAULT '',
	authorized     INTEGER NOT NULL DEFAULT 0,
	authorizer     TEXT NOT NULL DEFAULT '',
	auth_ref       TEXT NOT NULL DEFAULT '',
	auth_from      TEXT NOT NULL DEFAULT '',
	auth_to        TEXT NOT NULL DEFAULT '',
	window_start   TEXT NOT NULL DEFAULT '',
	window_end     TEXT NOT NULL DEFAULT '',
	report_due     TEXT NOT NULL DEFAULT '',
	techniques     TEXT NOT NULL DEFAULT '',
	notes          TEXT NOT NULL DEFAULT '',
	created_at     TEXT NOT NULL,
	updated_at     TEXT NOT NULL
);

CREATE TABLE engagement_contacts (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	role       TEXT NOT NULL DEFAULT '',
	name       TEXT NOT NULL DEFAULT '',
	email      TEXT NOT NULL DEFAULT '',
	phone      TEXT NOT NULL DEFAULT '',
	note       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_engagement_contacts_project ON engagement_contacts(project_id);

CREATE TABLE engagement_test_accounts (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	role       TEXT NOT NULL DEFAULT '',
	username   TEXT NOT NULL DEFAULT '',
	secret_ref TEXT NOT NULL DEFAULT '',
	note       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_engagement_test_accounts_project ON engagement_test_accounts(project_id);

-- Out-of-scope becomes first-class: a scope entry is an allow rule or a deny (exclusion). Deny wins.
ALTER TABLE scope_entries ADD COLUMN disposition TEXT NOT NULL DEFAULT 'allow';
