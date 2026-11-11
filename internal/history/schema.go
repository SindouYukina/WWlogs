package history

// The Combat History schema.
//
// One local SQLite file keeps what the player actually fought, separate from the read-only Master. Times are UTC
// nanoseconds of the capture clock (the engine's own times, so durations and DPS reproduce exactly); local_date is the
// calendar date where the encounter started, in the host's local time at that moment, with the offset kept beside it.
//
// Facts and engine results are kept apart: boundaries, outcomes, integrity facts and per-contributor totals are what the
// engine decided when an encounter finished. Encounter DPS, peak DPS and healing are stored as the engine computed them
// before data integrity is applied; readers apply the stored integrity with the engine's own function, so a later loss
// report changes the shown status without rewriting the result. Names are never stored: they come from the Master for the
// selected locale when shown.
//
// The schema version is PRAGMA user_version and the file is marked with PRAGMA application_id. Migrations only add: they
// never delete rows, and every one runs in its own transaction after a full copy of the file was taken. A file written by
// a newer schema is opened read-only and never modified.

// applicationID marks a WWlogs Combat History file ("WWLH").
const applicationID = 0x57574C48

// migration is one forward schema step. Its statements run in one transaction.
type migration struct {
	version int
	name    string
	sql     string
}

// migrations is the full schema history. A released step is never edited; changes are new steps.
var migrations = []migration{
	{version: 1, name: "combat facts", sql: schemaV1},
	{version: 2, name: "scene and enemy instances", sql: schemaV2},
	{version: 3, name: "effect lifecycles, event coverage and checkpoints", sql: schemaV3},
}

// latestVersion is the schema this build writes.
func latestVersion() int { return migrations[len(migrations)-1].version }

const schemaV1 = `
CREATE TABLE schema_migration (
	version       INTEGER PRIMARY KEY,
	name          TEXT    NOT NULL,
	applied_at_ns INTEGER NOT NULL,
	app_version   TEXT    NOT NULL,
	app_commit    TEXT    NOT NULL
);

-- One capture session: one Npcap handle or one replayed file. It is not an encounter.
CREATE TABLE capture_session (
	id              INTEGER PRIMARY KEY,
	session_uid     TEXT    NOT NULL UNIQUE,
	source          TEXT    NOT NULL CHECK (source IN ('live', 'replay')),
	-- SHA-256 of a replayed file, so the same capture is never stored twice; NULL for live capture.
	source_sha256   TEXT,
	status          TEXT    NOT NULL CHECK (status IN ('open', 'closed', 'abandoned')),
	stop_reason     TEXT,
	-- Host wall clock (UTC ns) and the host's UTC offset when the session opened.
	opened_at_ns    INTEGER NOT NULL,
	closed_at_ns    INTEGER,
	utc_offset_s    INTEGER NOT NULL,
	zone_name       TEXT    NOT NULL,
	history_level   TEXT    NOT NULL CHECK (history_level IN ('encounters', 'events')),
	-- Journal entries lost: refused by the engine's bound, refused by the full storage queue, or lost with a failed transaction.
	journal_dropped INTEGER NOT NULL DEFAULT 0,
	queue_dropped   INTEGER NOT NULL DEFAULT 0,
	write_failed    INTEGER NOT NULL DEFAULT 0,
	app_version     TEXT    NOT NULL,
	app_build       INTEGER NOT NULL,
	app_commit      TEXT    NOT NULL,
	app_dirty       TEXT    NOT NULL,
	app_profile     TEXT    NOT NULL,
	protocol_schema TEXT    NOT NULL,
	master_sha256   TEXT    NOT NULL,
	UNIQUE (source, source_sha256)
);

-- Transport loss episodes of a session (capture time, UTC ns). to_ns is NULL while the loss lasted to the end.
CREATE TABLE capture_gap (
	capture_session_id INTEGER NOT NULL REFERENCES capture_session (id) ON DELETE CASCADE,
	loss_id            INTEGER NOT NULL,
	direction          TEXT    NOT NULL CHECK (direction IN ('in', 'out')),
	from_ns            INTEGER NOT NULL,
	to_ns              INTEGER,
	failed             INTEGER NOT NULL DEFAULT 0 CHECK (failed IN (0, 1)),
	PRIMARY KEY (capture_session_id, loss_id)
) WITHOUT ROWID;

-- One encounter as the engine segmented it. state is the history lifecycle: pending (opened, may still be discarded), open
-- (has confirmed local activity), finished, abandoned (the application ended before it finished). complete is 0 when a
-- journal entry of the encounter never reached storage.
CREATE TABLE encounter (
	id                           INTEGER PRIMARY KEY,
	capture_session_id           INTEGER NOT NULL REFERENCES capture_session (id) ON DELETE CASCADE,
	statistics_interval          INTEGER NOT NULL,
	sequence                     INTEGER NOT NULL,
	protocol_session             INTEGER NOT NULL,
	state                        TEXT    NOT NULL CHECK (state IN ('pending', 'open', 'finished', 'abandoned')),
	outcome                      TEXT    NOT NULL CHECK (outcome IN ('OPEN', 'CONFIRMED', 'INFERRED', 'INTERRUPTED', 'INCOMPLETE')),
	complete                     INTEGER NOT NULL DEFAULT 1 CHECK (complete IN (0, 1)),
	started_at_ns                INTEGER NOT NULL,
	start_basis                  TEXT    NOT NULL,
	start_confirmed              INTEGER NOT NULL DEFAULT 0,
	battle_entered_at_ns         INTEGER,
	ended_at_ns                  INTEGER,
	end_basis                    TEXT,
	end_decided_at_ns            INTEGER,
	first_activity_at_ns         INTEGER,
	last_activity_at_ns          INTEGER,
	reentries                    INTEGER NOT NULL DEFAULT 0,
	local_date                   TEXT    NOT NULL,
	utc_offset_s                 INTEGER NOT NULL,
	damage_dealt                 INTEGER,
	damage_taken                 INTEGER,
	outgoing_events              INTEGER,
	incoming_events              INTEGER,
	unattributed_outgoing_damage INTEGER,
	unattributed_incoming_damage INTEGER,
	unattributed_outgoing_events INTEGER,
	unattributed_incoming_events INTEGER,
	integrity_status             TEXT CHECK (integrity_status IN ('INTACT', 'DEGRADED', 'UNRELIABLE')),
	transport_losses             INTEGER,
	unrecovered_losses           INTEGER,
	context_downgraded_events    INTEGER,
	first_loss_at_ns             INTEGER,
	dps_status                   TEXT,
	dps_reason                   TEXT,
	dps_value                    REAL,
	dps_damage                   INTEGER,
	dps_duration_ns              INTEGER,
	dps_start_ns                 INTEGER,
	dps_end_ns                   INTEGER,
	dps_confidence               TEXT,
	peak_status                  TEXT,
	peak_reason                  TEXT,
	peak_value                   REAL,
	peak_damage                  INTEGER,
	peak_duration_ns             INTEGER,
	peak_start_ns                INTEGER,
	peak_end_ns                  INTEGER,
	peak_confidence              TEXT,
	healing_status               TEXT,
	healing_reason               TEXT,
	healing_effective            INTEGER,
	healing_reported             INTEGER,
	healing_overheal             INTEGER,
	healing_events               INTEGER,
	healing_unknown_effective    INTEGER,
	healing_unknown_events       INTEGER,
	unassigned_damage_dealt      INTEGER,
	unassigned_outgoing_events   INTEGER,
	unassigned_damage_taken      INTEGER,
	unassigned_incoming_events   INTEGER,
	unassigned_overflow_events   INTEGER,
	UNIQUE (capture_session_id, statistics_interval, sequence)
);
CREATE INDEX encounter_by_date ON encounter (local_date, started_at_ns);
CREATE INDEX encounter_by_state ON encounter (state);

CREATE TABLE encounter_risk (
	encounter_id INTEGER NOT NULL REFERENCES encounter (id) ON DELETE CASCADE,
	risk         TEXT    NOT NULL,
	PRIMARY KEY (encounter_id, risk)
) WITHOUT ROWID;

-- A role or the vehicle the engine credited confirmed local damage to, in the engine's order.
CREATE TABLE encounter_contributor (
	encounter_id                     INTEGER NOT NULL REFERENCES encounter (id) ON DELETE CASCADE,
	ordinal                          INTEGER NOT NULL,
	kind                             TEXT    NOT NULL CHECK (kind IN ('role', 'vehicle')),
	player_id                        INTEGER NOT NULL DEFAULT 0,
	role_id                          INTEGER NOT NULL DEFAULT 0,
	vehicle_kind                     TEXT    NOT NULL DEFAULT '',
	vehicle_evidence                 TEXT    NOT NULL DEFAULT '',
	damage_dealt                     INTEGER NOT NULL,
	outgoing_events                  INTEGER NOT NULL,
	direct_damage                    INTEGER NOT NULL DEFAULT 0,
	summon_damage                    INTEGER NOT NULL DEFAULT 0,
	damage_taken                     INTEGER NOT NULL DEFAULT 0,
	incoming_events                  INTEGER NOT NULL DEFAULT 0,
	unassigned_damage                INTEGER NOT NULL DEFAULT 0,
	unassigned_events                INTEGER NOT NULL DEFAULT 0,
	unassigned_missing_skill_events  INTEGER NOT NULL DEFAULT 0,
	unassigned_overflow_events       INTEGER NOT NULL DEFAULT 0,
	unassigned_damage_ids_incomplete INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (encounter_id, ordinal)
) WITHOUT ROWID;

-- A contributor's confirmed damage by DamageContext.SkillId.
CREATE TABLE encounter_skill (
	encounter_id          INTEGER NOT NULL,
	contributor_ordinal   INTEGER NOT NULL,
	skill_id              INTEGER NOT NULL CHECK (skill_id > 0),
	damage_dealt          INTEGER NOT NULL,
	outgoing_events       INTEGER NOT NULL,
	damage_ids_incomplete INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (encounter_id, contributor_ordinal, skill_id),
	FOREIGN KEY (encounter_id, contributor_ordinal) REFERENCES encounter_contributor (encounter_id, ordinal) ON DELETE CASCADE
) WITHOUT ROWID;

-- The DamageIds and held Echo Visions behind a skill row, or behind a contributor's hits without a SkillId (skill_id 0).
-- They only name a row (skill_names.go) and never change one.
CREATE TABLE encounter_skill_evidence (
	encounter_id        INTEGER NOT NULL,
	contributor_ordinal INTEGER NOT NULL,
	skill_id            INTEGER NOT NULL,
	kind                TEXT    NOT NULL CHECK (kind IN ('damage_id', 'echo_vision')),
	value               INTEGER NOT NULL,
	PRIMARY KEY (encounter_id, contributor_ordinal, skill_id, kind, value),
	FOREIGN KEY (encounter_id, contributor_ordinal) REFERENCES encounter_contributor (encounter_id, ordinal) ON DELETE CASCADE
) WITHOUT ROWID;

CREATE TABLE encounter_healer (
	encounter_id INTEGER NOT NULL REFERENCES encounter (id) ON DELETE CASCADE,
	player_id    INTEGER NOT NULL,
	role_id      INTEGER NOT NULL,
	effective    INTEGER NOT NULL,
	reported     INTEGER NOT NULL,
	events       INTEGER NOT NULL,
	PRIMARY KEY (encounter_id, player_id, role_id)
) WITHOUT ROWID;

-- The engine's stored DPS timeline intervals (encounter_timeline.go StoredTimeline).
CREATE TABLE encounter_timeline (
	encounter_id INTEGER PRIMARY KEY REFERENCES encounter (id) ON DELETE CASCADE,
	anchor_ns    INTEGER NOT NULL,
	scale_s      INTEGER NOT NULL CHECK (scale_s >= 1)
);
CREATE TABLE encounter_timeline_contributor (
	encounter_id INTEGER NOT NULL REFERENCES encounter_timeline (encounter_id) ON DELETE CASCADE,
	position     INTEGER NOT NULL,
	kind         TEXT    NOT NULL CHECK (kind IN ('role', 'vehicle', 'unassigned')),
	player_id    INTEGER NOT NULL DEFAULT 0,
	role_id      INTEGER NOT NULL DEFAULT 0,
	vehicle_kind TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (encounter_id, position)
) WITHOUT ROWID;
CREATE TABLE encounter_timeline_bucket (
	encounter_id INTEGER NOT NULL,
	bucket       INTEGER NOT NULL,
	position     INTEGER NOT NULL,
	damage       INTEGER NOT NULL CHECK (damage > 0),
	PRIMARY KEY (encounter_id, bucket, position),
	FOREIGN KEY (encounter_id, position) REFERENCES encounter_timeline_contributor (encounter_id, position) ON DELETE CASCADE
) WITHOUT ROWID;

-- Every combat result the statistics accepted, in acceptance order: the normalized record only, never packet content.
-- encounter_id is the encounter the engine assigned it to, NULL outside every encounter.
CREATE TABLE combat_event (
	capture_session_id   INTEGER NOT NULL REFERENCES capture_session (id) ON DELETE CASCADE,
	sequence             INTEGER NOT NULL,
	encounter_id         INTEGER REFERENCES encounter (id) ON DELETE SET NULL,
	protocol_session     INTEGER NOT NULL,
	rpc_id               INTEGER NOT NULL,
	observed_at_ns       INTEGER,
	kind                 TEXT    NOT NULL CHECK (kind IN ('damage', 'healing')),
	flow                 TEXT    NOT NULL,
	confidence           TEXT    NOT NULL,
	evidence             TEXT    NOT NULL,
	amount               INTEGER NOT NULL,
	applied_healing      INTEGER NOT NULL DEFAULT 0,
	healing_inconsistent INTEGER NOT NULL DEFAULT 0,
	damage_id            INTEGER NOT NULL DEFAULT 0,
	skill_id             INTEGER NOT NULL DEFAULT 0,
	source_entity_id     INTEGER NOT NULL,
	source_generation    INTEGER NOT NULL,
	source_relation      TEXT    NOT NULL,
	source_player_id     INTEGER,
	source_role_id       INTEGER,
	source_role_basis    TEXT,
	target_entity_id     INTEGER NOT NULL,
	target_generation    INTEGER NOT NULL,
	target_relation      TEXT    NOT NULL,
	target_player_id     INTEGER,
	target_role_id       INTEGER,
	target_role_basis    TEXT,
	vehicle_kind         TEXT    NOT NULL DEFAULT '',
	context_downgrade    TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (capture_session_id, sequence)
) WITHOUT ROWID;
CREATE UNIQUE INDEX combat_event_response ON combat_event (capture_session_id, protocol_session, kind, rpc_id) WHERE rpc_id != 0;
CREATE INDEX combat_event_by_encounter ON combat_event (encounter_id) WHERE encounter_id IS NOT NULL;
`

// schemaV2 adds what the daily list and the encounter detail need to say where a fight happened and against whom. Rows
// written by schema 1 keep NULL and 0: their scene and enemies were never recorded, and readers say so.
const schemaV2 = `
-- JoinSceneNotify.SceneInfo.InstanceId of the encounter's protocol session when it opened; NULL when not observed.
-- scene_recorded is 1 for rows that recorded it (written by schema 2 or later), so NULL there means not observed.
ALTER TABLE encounter ADD COLUMN scene_instance_id INTEGER;
ALTER TABLE encounter ADD COLUMN scene_recorded INTEGER NOT NULL DEFAULT 0;
-- 1 once the enemy split was recorded for the encounter; enemies_overflow counts instances beyond the engine's bound.
ALTER TABLE encounter ADD COLUMN enemies_recorded INTEGER NOT NULL DEFAULT 0;
ALTER TABLE encounter ADD COLUMN enemies_overflow INTEGER NOT NULL DEFAULT 0;

-- One enemy instance of an encounter: a non-player entity with confirmed local flows. Same-type enemies are separate rows.
-- monster_id is MonsterInfo.Id and template_id TemplateConfig.Id (0: unknown); both are CORRELATED Master links.
CREATE TABLE encounter_enemy (
	encounter_id      INTEGER NOT NULL REFERENCES encounter (id) ON DELETE CASCADE,
	ordinal           INTEGER NOT NULL,
	monster_id        INTEGER NOT NULL DEFAULT 0,
	template_id       INTEGER NOT NULL DEFAULT 0,
	identity_conflict INTEGER NOT NULL DEFAULT 0,
	damage_taken      INTEGER NOT NULL,
	hits_taken        INTEGER NOT NULL,
	damage_dealt      INTEGER NOT NULL,
	hits_dealt        INTEGER NOT NULL,
	first_seen_ns     INTEGER,
	last_seen_ns      INTEGER,
	PRIMARY KEY (encounter_id, ordinal)
) WITHOUT ROWID;

-- The static identity of each endpoint at event time (0: unknown).
ALTER TABLE combat_event ADD COLUMN source_monster_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE combat_event ADD COLUMN source_template_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE combat_event ADD COLUMN target_monster_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE combat_event ADD COLUMN target_template_id INTEGER NOT NULL DEFAULT 0;
`

// schemaV3 adds effect lifecycles, what each recorded span covered, and checkpoints of unfinished encounters.
//
// Event coverage is what a span's recorder stored, kept apart from capture integrity (capture_gap and the encounter
// integrity columns): a span without any transport loss can still lack effect changes because its build never stored
// them. Rows written before schema 3 stored combat results only at the events level and never stored effect changes or
// skill casts, so the new coverage columns default to 0 for them and only combat_events_recorded is derived from
// history_level. skill_casts_recorded stays 0: no build has an independent, server-confirmed skill cast source.
//
// Combat History exists only in the v0.7.0 development line, so a session labelled v0.6.0 was written by a v0.7.0-line
// build that still carried the stale version registry; its label is corrected and the written label kept beside it.
const schemaV3 = `
ALTER TABLE capture_session ADD COLUMN combat_events_recorded INTEGER NOT NULL DEFAULT 0 CHECK (combat_events_recorded IN (0, 1));
ALTER TABLE capture_session ADD COLUMN effect_events_recorded INTEGER NOT NULL DEFAULT 0 CHECK (effect_events_recorded IN (0, 1));
ALTER TABLE capture_session ADD COLUMN skill_casts_recorded INTEGER NOT NULL DEFAULT 0 CHECK (skill_casts_recorded IN (0, 1));
-- Effect APPLYs the tracker refused at its capacity bound: their instances are missing from effect_instance.
ALTER TABLE capture_session ADD COLUMN effects_rejected INTEGER NOT NULL DEFAULT 0;
-- How the span started: with the capture, or when recording was switched on during it.
ALTER TABLE capture_session ADD COLUMN start_reason TEXT NOT NULL DEFAULT 'capture_start' CHECK (start_reason IN ('capture_start', 'recording_enabled'));
-- The version label the session was written with, when a migration corrected app_version; NULL when never corrected.
ALTER TABLE capture_session ADD COLUMN recorded_app_version TEXT;
UPDATE capture_session SET combat_events_recorded = 1 WHERE history_level = 'events';
UPDATE capture_session SET recorded_app_version = app_version, app_version = 'v0.7.0' WHERE app_version = 'v0.6.0';

-- checkpoint_at_ns is the capture time the stored results of an unfinished encounter describe: the engine's rendering at its
-- last checkpoint. It is NULL for a finished encounter and for one without a checkpoint. incomplete_reason says why
-- complete is 0 or the encounter was abandoned: entries_lost, application_exit, recording_disabled or not_finished. Rows
-- written before schema 3 keep NULL: their reason was not recorded.
ALTER TABLE encounter ADD COLUMN checkpoint_at_ns INTEGER;
ALTER TABLE encounter ADD COLUMN incomplete_reason TEXT;

-- One effect instance the effect tracker followed within a span: instance numbers it within the capture session, since
-- handles are reused and are not stored. The identity columns are the normalized holder and source as first stored; a
-- later change may only fill what was unknown. effect_id is BuffId (NULL when no message named it); a definition link to
-- the Master is CORRELATED. Roles and monster or template types are resolved like those of combat events.
CREATE TABLE effect_instance (
	capture_session_id INTEGER NOT NULL REFERENCES capture_session (id) ON DELETE CASCADE,
	instance           INTEGER NOT NULL,
	protocol_session   INTEGER NOT NULL,
	holder_entity_id   INTEGER NOT NULL,
	holder_generation  INTEGER NOT NULL,
	effect_id          INTEGER,
	definition_linked  INTEGER NOT NULL DEFAULT 0 CHECK (definition_linked IN (0, 1)),
	source_entity_id   INTEGER,
	holder_relation    TEXT    NOT NULL,
	source_relation    TEXT    NOT NULL,
	holder_player_id   INTEGER,
	holder_role_id     INTEGER,
	holder_role_basis  TEXT,
	holder_monster_id  INTEGER NOT NULL DEFAULT 0,
	holder_template_id INTEGER NOT NULL DEFAULT 0,
	source_player_id   INTEGER,
	source_role_id     INTEGER,
	source_role_basis  TEXT,
	source_monster_id  INTEGER NOT NULL DEFAULT 0,
	source_template_id INTEGER NOT NULL DEFAULT 0,
	origin             TEXT    NOT NULL,
	first_observed_ns  INTEGER,
	-- 1 when the stored changes begin with the instance's first observation (apply, snapshot or instant); 0 when the
	-- instance was already tracked before the span started or its first change never reached storage.
	start_recorded     INTEGER NOT NULL CHECK (start_recorded IN (0, 1)),
	PRIMARY KEY (capture_session_id, instance)
) WITHOUT ROWID;
CREATE INDEX effect_instance_by_effect ON effect_instance (effect_id) WHERE effect_id IS NOT NULL;

-- One accepted effect lifecycle change, in the tracker's order (sequence), with the instance's state after it.
-- after_combat_sequence is the combat_event sequence of the last combat result journaled before it, so effect changes and
-- combat results interleave exactly. observation is observed (a message stated it), derived (an end implied by the holder's
-- removal or the handle's reuse) or untracked (tracking stopped; nothing is known afterwards). An estimated expiry is never
-- stored as a change: remaining_s at basis_ns is what the server stated, and any expiry computed from it is an estimate.
CREATE TABLE effect_event (
	capture_session_id    INTEGER NOT NULL,
	sequence              INTEGER NOT NULL,
	instance              INTEGER NOT NULL,
	after_combat_sequence INTEGER NOT NULL,
	observed_at_ns        INTEGER,
	change_kind           TEXT    NOT NULL CHECK (change_kind IN ('apply', 'snapshot', 'refresh', 'stack', 'duration', 'remove', 'holder_removed', 'superseded', 'untracked', 'instant')),
	observation           TEXT    NOT NULL CHECK (observation IN ('observed', 'derived', 'untracked')),
	evidence              TEXT    NOT NULL,
	reason                TEXT    NOT NULL DEFAULT '',
	stack                 INTEGER,
	duration_s            REAL,
	remaining_s           REAL,
	basis_ns              INTEGER,
	time_reason           TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (capture_session_id, sequence),
	FOREIGN KEY (capture_session_id, instance) REFERENCES effect_instance (capture_session_id, instance) ON DELETE CASCADE
) WITHOUT ROWID;
-- A timeline reads an encounter's window through effect_event_by_time; one instance's changes are in sequence order within
-- the span's primary key range, so no second index is kept for them.
CREATE INDEX effect_event_by_time ON effect_event (capture_session_id, observed_at_ns);
`
