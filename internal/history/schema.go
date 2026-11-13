package history

// The Combat History schema.
//
// One local SQLite file keeps what the player actually fought, separate from the read-only Master. Times are UTC
// nanoseconds of the capture clock (the engine's own times, so durations and DPS reproduce exactly); local_date is the
// calendar date where the encounter started, in the host's local time at that moment, with the offset kept beside it. Both
// are provenance and an index key only: a view lays every instant out in the host's current zone (zone.go).
//
// Facts and engine results are kept apart: boundaries, outcomes, integrity facts and per-contributor totals are what the
// engine decided when an encounter finished. Encounter DPS, peak DPS and healing are stored as the engine computed them
// before data integrity is applied; readers apply the stored integrity with the engine's own function, so a later loss
// report changes the shown status without rewriting the result. Names are never stored: they come from the Master for the
// selected locale when shown.
//
// What produced a record is part of it. A recorded span keeps the WWlogs executable that analysed it (product version,
// official Build Number, commit, profile) and the Master it read; each protocol session keeps the major.minor game version
// it reported, which is the only version a build decodes (capture.CurrentGameVersion). A stored encounter is a snapshot of
// that analyser's results: a later WWlogs reads it as stored and never recomputes it.
//
// The file is marked with PRAGMA application_id and its layout with PRAGMA user_version. A build reads and writes exactly
// schemaVersion; a file of any other layout is never modified (Store.open).

// applicationID marks a WWlogs Combat History file ("WWCH").
const applicationID = 0x57574348

// schemaVersion is the layout this build creates, reads and writes.
const schemaVersion = 1

const schemaDDL = `
-- One recorded span of a capture: an Npcap handle or a replayed file, from the moment its history started recording to
-- the moment it stopped. A capture recorded from start to end is one span. It is not an encounter.
CREATE TABLE capture_session (
	id               INTEGER PRIMARY KEY,
	session_uid      TEXT    NOT NULL UNIQUE,
	source           TEXT    NOT NULL CHECK (source IN ('live', 'replay')),
	-- SHA-256 of a replayed file, so the same capture is never stored twice; NULL for live capture and for later spans.
	source_sha256    TEXT,
	status           TEXT    NOT NULL CHECK (status IN ('open', 'closed', 'abandoned')),
	start_reason     TEXT    NOT NULL CHECK (start_reason IN ('capture_start', 'recording_enabled')),
	stop_reason      TEXT,
	-- Host wall clock (UTC ns) and the host's zone when the span opened.
	opened_at_ns     INTEGER NOT NULL,
	closed_at_ns     INTEGER,
	utc_offset_s     INTEGER NOT NULL,
	zone_name        TEXT    NOT NULL,
	-- Journal entries of the span that never reached storage: refused by the engine journal's bound, by the full storage
	-- queue, or lost with a failed transaction. effects_rejected counts effect APPLYs the effect tracker refused at its bound.
	journal_dropped  INTEGER NOT NULL DEFAULT 0,
	queue_dropped    INTEGER NOT NULL DEFAULT 0,
	write_failed     INTEGER NOT NULL DEFAULT 0,
	effects_rejected INTEGER NOT NULL DEFAULT 0,
	-- The WWlogs executable that analysed the span: product version (MAJOR.MINOR.PATCH; NULL when the build carried none),
	-- official Build Number (NULL for an unofficial build), commit (NULL when unknown), working tree state and profile; and
	-- the Master it read.
	app_version      TEXT    CHECK (app_version GLOB '[0-9]*.[0-9]*.[0-9]*'),
	app_build        INTEGER CHECK (app_build > 0),
	app_commit       TEXT    CHECK (length(app_commit) = 40),
	app_dirty        TEXT    NOT NULL CHECK (app_dirty IN ('true', 'false', 'unknown')),
	app_profile      TEXT    NOT NULL CHECK (app_profile IN ('development', 'public')),
	master_sha256    TEXT    NOT NULL,
	UNIQUE (source, source_sha256)
);

-- One protocol session of a span: one game connection from its handshake, numbered within the capture. game_version is the
-- major.minor game version the session reported (LoginRequest or CheckClientPush). A build decodes only the session that
-- reported the version it decodes, so every protocol session here had its version confirmed before anything of it was
-- recorded, and everything the span stored belongs to one of them.
CREATE TABLE protocol_session (
	capture_session_id INTEGER NOT NULL REFERENCES capture_session (id) ON DELETE CASCADE,
	protocol_session   INTEGER NOT NULL CHECK (protocol_session > 0),
	game_version       TEXT    NOT NULL CHECK (game_version GLOB '[0-9]*.[0-9]*' AND game_version NOT GLOB '*[^0-9.]*' AND game_version NOT GLOB '*.*.*'),
	PRIMARY KEY (capture_session_id, protocol_session)
) WITHOUT ROWID;

-- Transport loss episodes of a span (capture time, UTC ns). to_ns is NULL while the loss lasted to the end.
CREATE TABLE capture_gap (
	capture_session_id INTEGER NOT NULL REFERENCES capture_session (id) ON DELETE CASCADE,
	loss_id            INTEGER NOT NULL,
	direction          TEXT    NOT NULL CHECK (direction IN ('in', 'out')),
	from_ns            INTEGER NOT NULL,
	to_ns              INTEGER,
	failed             INTEGER NOT NULL DEFAULT 0 CHECK (failed IN (0, 1)),
	PRIMARY KEY (capture_session_id, loss_id)
) WITHOUT ROWID;

-- One encounter as the engine segmented it, in the protocol session it opened in. state is the history lifecycle: pending
-- (opened, may still be discarded), open (has confirmed local activity), finished, abandoned (the application ended before
-- it finished). complete is 0 when a journal entry of the encounter never reached storage; incomplete_reason says why it is
-- incomplete or was abandoned (entries_lost, application_exit, recording_disabled, not_finished). checkpoint_at_ns is the
-- capture time the stored results of an unfinished encounter describe: the engine's rendering at its last checkpoint.
-- scene_instance_id is JoinSceneNotify.SceneInfo.InstanceId of the protocol session when the encounter opened; NULL when
-- none was observed. enemies_overflow counts enemy instances beyond the engine's bound.
CREATE TABLE encounter (
	id                           INTEGER PRIMARY KEY,
	capture_session_id           INTEGER NOT NULL REFERENCES capture_session (id) ON DELETE CASCADE,
	statistics_interval          INTEGER NOT NULL,
	sequence                     INTEGER NOT NULL,
	protocol_session             INTEGER NOT NULL,
	state                        TEXT    NOT NULL CHECK (state IN ('pending', 'open', 'finished', 'abandoned')),
	outcome                      TEXT    NOT NULL CHECK (outcome IN ('OPEN', 'CONFIRMED', 'INFERRED', 'INTERRUPTED', 'INCOMPLETE')),
	complete                     INTEGER NOT NULL DEFAULT 1 CHECK (complete IN (0, 1)),
	incomplete_reason            TEXT    CHECK (incomplete_reason IN ('entries_lost', 'application_exit', 'recording_disabled', 'not_finished')),
	checkpoint_at_ns             INTEGER,
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
	scene_instance_id            INTEGER,
	enemies_overflow             INTEGER NOT NULL DEFAULT 0,
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
	UNIQUE (capture_session_id, statistics_interval, sequence),
	FOREIGN KEY (capture_session_id, protocol_session) REFERENCES protocol_session (capture_session_id, protocol_session) ON DELETE CASCADE
);
CREATE INDEX encounter_by_date ON encounter (local_date, started_at_ns);
CREATE INDEX encounter_by_state ON encounter (state);
CREATE INDEX encounter_by_protocol_session ON encounter (capture_session_id, protocol_session);

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

-- Every combat result the statistics accepted, in acceptance order: the normalized record only, never packet content.
-- encounter_id is the encounter the engine assigned it to, NULL outside every encounter. The endpoint monster and template
-- IDs are the static identity at event time (0: unknown).
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
	source_monster_id    INTEGER NOT NULL DEFAULT 0,
	source_template_id   INTEGER NOT NULL DEFAULT 0,
	target_entity_id     INTEGER NOT NULL,
	target_generation    INTEGER NOT NULL,
	target_relation      TEXT    NOT NULL,
	target_player_id     INTEGER,
	target_role_id       INTEGER,
	target_role_basis    TEXT,
	target_monster_id    INTEGER NOT NULL DEFAULT 0,
	target_template_id   INTEGER NOT NULL DEFAULT 0,
	vehicle_kind         TEXT    NOT NULL DEFAULT '',
	context_downgrade    TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (capture_session_id, sequence),
	FOREIGN KEY (capture_session_id, protocol_session) REFERENCES protocol_session (capture_session_id, protocol_session) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE UNIQUE INDEX combat_event_response ON combat_event (capture_session_id, protocol_session, kind, rpc_id) WHERE rpc_id != 0;
CREATE INDEX combat_event_by_encounter ON combat_event (encounter_id) WHERE encounter_id IS NOT NULL;
CREATE INDEX combat_event_by_protocol_session ON combat_event (capture_session_id, protocol_session);

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
	PRIMARY KEY (capture_session_id, instance),
	FOREIGN KEY (capture_session_id, protocol_session) REFERENCES protocol_session (capture_session_id, protocol_session) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX effect_instance_by_effect ON effect_instance (effect_id) WHERE effect_id IS NOT NULL;
CREATE INDEX effect_instance_by_protocol_session ON effect_instance (capture_session_id, protocol_session);

-- One accepted effect lifecycle change, in the tracker's order (sequence, unique in the span), with the instance's state
-- after it. The primary key leads with the instance, so the changes of one instance, and the foreign key's cascade, are one
-- key range: without statistics SQLite would otherwise search a span-wide key prefix once per instance.
-- after_combat_sequence is the combat_event sequence of the last combat result journaled before it, so effect changes and
-- combat results interleave exactly. observation is observed (a message stated it), derived (an end implied by the holder's
-- removal or the handle's reuse) or untracked (tracking stopped; nothing is known afterwards). An estimated expiry is never
-- stored as a change: remaining_s at basis_ns is what the server stated, and any expiry computed from it is an estimate.
CREATE TABLE effect_event (
	capture_session_id    INTEGER NOT NULL,
	instance              INTEGER NOT NULL,
	sequence              INTEGER NOT NULL,
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
	PRIMARY KEY (capture_session_id, instance, sequence),
	UNIQUE (capture_session_id, sequence),
	FOREIGN KEY (capture_session_id, instance) REFERENCES effect_instance (capture_session_id, instance) ON DELETE CASCADE
) WITHOUT ROWID;
-- A timeline reads an encounter's window through effect_event_by_time.
CREATE INDEX effect_event_by_time ON effect_event (capture_session_id, observed_at_ns);
`
