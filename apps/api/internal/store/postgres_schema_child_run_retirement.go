package store

var postgresChildRunRetirement = []string{
	`DO $$
	BEGIN
		IF to_regclass('run_delegations') IS NOT NULL THEN
			UPDATE collaboration_steps parent_step
			SET output = child_step.output,
				status = child_step.status,
				error = child_step.error,
				updated_at = GREATEST(parent_step.updated_at, child_step.updated_at)
			FROM run_delegations delegation
			JOIN LATERAL (
				SELECT output, status, error, updated_at
				FROM collaboration_steps
				WHERE run_id = delegation.child_run_id AND role = 'worker' AND status = 'completed'
				ORDER BY created_at DESC, id DESC
				LIMIT 1
			) child_step ON true
			WHERE parent_step.id = delegation.parent_stage_id;

			UPDATE stage_checkpoints checkpoint
			SET status = child_checkpoint.status,
				input_hash = child_checkpoint.input_hash,
				output_hash = child_checkpoint.output_hash,
				error = child_checkpoint.error,
				updated_at = GREATEST(checkpoint.updated_at, child_checkpoint.updated_at)
			FROM run_delegations delegation
			JOIN collaboration_steps child_step
				ON child_step.run_id = delegation.child_run_id AND child_step.role = 'worker'
			JOIN stage_checkpoints child_checkpoint
				ON child_checkpoint.run_id = delegation.child_run_id AND child_checkpoint.stage_id = child_step.id
			WHERE checkpoint.run_id = delegation.parent_run_id
				AND checkpoint.stage_id = delegation.parent_stage_id;

			UPDATE tool_effects item
			SET run_id = delegation.parent_run_id, stage_id = delegation.parent_stage_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			UPDATE tool_artifacts item
			SET run_id = delegation.parent_run_id, stage_id = delegation.parent_stage_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			UPDATE model_request_records item
			SET run_id = delegation.parent_run_id, stage_id = delegation.parent_stage_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			UPDATE run_usage_entries item
			SET run_id = delegation.parent_run_id, stage_id = delegation.parent_stage_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			UPDATE verification_evidence item
			SET run_id = delegation.parent_run_id, stage_id = delegation.parent_stage_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			UPDATE verification_artifacts item
			SET run_id = delegation.parent_run_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			UPDATE context_compactions item
			SET run_id = delegation.parent_run_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			UPDATE memories item
			SET run_id = delegation.parent_run_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			UPDATE memory_candidates item
			SET run_id = delegation.parent_run_id
			FROM run_delegations delegation
			WHERE item.run_id = delegation.child_run_id;

			WITH child_events AS (
				SELECT child_event.id, delegation.parent_run_id, delegation.parent_stage_id,
					COALESCE((
						SELECT MAX(parent_event.sequence)
						FROM run_events parent_event
						WHERE parent_event.run_id = delegation.parent_run_id
					), 0) + ROW_NUMBER() OVER (
						PARTITION BY delegation.parent_run_id
						ORDER BY child_event.sequence, child_event.id
					) AS new_sequence
				FROM run_events child_event
				JOIN run_delegations delegation ON delegation.child_run_id = child_event.run_id
				WHERE child_event.type NOT LIKE 'run.%'
					AND child_event.type NOT LIKE 'stage.%'
					AND child_event.type NOT LIKE 'delegation.%'
			)
			UPDATE run_events item
			SET run_id = child_events.parent_run_id,
				stage_id = child_events.parent_stage_id,
				sequence = child_events.new_sequence
			FROM child_events
			WHERE item.id = child_events.id;

			DELETE FROM run_events
			WHERE run_id IN (SELECT child_run_id FROM run_delegations)
				OR type LIKE 'delegation.%';
			DELETE FROM runs WHERE id IN (SELECT child_run_id FROM run_delegations);
			DROP TABLE run_delegations;
		END IF;
	END $$`,
}
