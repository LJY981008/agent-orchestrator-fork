-- LADO fork: the model and effort each LADO stage's workers run with
-- (issue #103, domain.LadoStageAgents).
--
-- One row is one scope's setting for one stage: project_id '' holds the
-- global defaults, a project id that project's override. No row means the
-- stage follows the default. project_id refers to projects.id without a
-- FOREIGN KEY, as lado_project_settings does, so a project's rows outlive it.
-- harness names the agent the model belongs to; effort '' passes none, so AO
-- picks it at spawn. The service checks the harness and the model against
-- the agent's catalog. A settings table: no change trigger.

-- +goose Up
CREATE TABLE lado_stage_agents (
    project_id TEXT NOT NULL,
    stage      TEXT NOT NULL CHECK (stage IN ('plan', 'critic', 'execution', 'integration', 'resolve')),
    harness    TEXT NOT NULL CHECK (harness <> ''),
    model      TEXT NOT NULL CHECK (model <> ''),
    effort     TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (project_id, stage)
);

-- +goose Down
DROP TABLE lado_stage_agents;
