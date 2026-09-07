"use client";

import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type {
	RecoveryAction,
	RecoverySummary,
	ToolEffect,
	ToolEffectReconciliationAction
} from "../../lib/api";
import { listToolEffects, reconcileToolEffect } from "../../lib/api";

export function RecoverySummaryPanel({
	summary,
	onAction
}: {
	summary?: RecoverySummary;
	onAction: (action: RecoveryAction) => void;
}) {
	if (!summary) return null;
	return (
		<section className="recovery-summary" aria-labelledby="recovery-summary-title">
			<header>
				<div>
					<div className="recovery-summary-kicker">Recovery status</div>
					<h2 id="recovery-summary-title">{summary.title}</h2>
					<p>{summary.message}</p>
				</div>
				<code>{summary.reason}</code>
			</header>

			{summary.evidence.length > 0 ? (
				<div className="recovery-evidence">
					{summary.evidence.map((item, index) => (
						<div className="recovery-evidence-row" key={`${item.kind}-${item.id ?? index}`}>
							<div>
								<strong>{evidenceLabel(item.kind)}</strong>
								{item.id ? <code>{item.id}</code> : null}
							</div>
							<p>{item.summary}</p>
							{item.status ? <span>{item.status}</span> : null}
						</div>
					))}
				</div>
			) : (
				<p className="recovery-evidence-empty">No detailed evidence was recorded for this state.</p>
			)}

			{summary.artifact_refs.length > 0 ? (
				<div className="recovery-artifacts">
					<strong>Partial output references</strong>
					{summary.artifact_refs.map((ref) => <code key={ref}>{ref}</code>)}
				</div>
			) : null}

			{summary.actions.length > 0 ? (
				<div className="recovery-actions">
					{summary.actions.map((action, index) => (
						<div key={`${action.kind}-${action.target_id ?? index}`}>
							<button disabled={!action.enabled} onClick={() => onAction(action)} type="button">
								{action.label}
							</button>
							{!action.enabled && action.unavailable_reason ? <span>{action.unavailable_reason}</span> : null}
						</div>
					))}
				</div>
			) : null}
		</section>
	);
}

export function ToolEffectReconciliationPanel({ runId, onChanged }: { runId: string; onChanged: () => Promise<void> }) {
	const [effects, setEffects] = useState<ToolEffect[]>([]);
	const [error, setError] = useState("");
	const [loading, setLoading] = useState(true);

	async function load() {
		try {
			setError("");
			const items = await listToolEffects(runId);
			setEffects(items.filter((item) => item.status === "needs_reconciliation" || item.status === "reconciling"));
		} catch (err) {
			setError(err instanceof Error ? err.message : "Failed to load tool effects");
		} finally {
			setLoading(false);
		}
	}

	useEffect(() => {
		let canceled = false;
		async function loadInitial() {
			try {
				const items = await listToolEffects(runId);
				if (!canceled) {
					setEffects(items.filter((item) => item.status === "needs_reconciliation" || item.status === "reconciling"));
				}
			} catch (err) {
				if (!canceled) setError(err instanceof Error ? err.message : "Failed to load tool effects");
			} finally {
				if (!canceled) setLoading(false);
			}
		}
		void loadInitial();
		return () => { canceled = true; };
	}, [runId]);

	return (
		<section className="tool-effect-recovery" id="tool-effect-recovery" aria-labelledby="tool-effect-recovery-title">
			<header>
				<div>
					<h2 id="tool-effect-recovery-title">Tool effect reconciliation</h2>
					<p>Confirm the external outcome before this run resumes.</p>
				</div>
				<span>{effects.length} unresolved</span>
			</header>
			{loading ? <div className="empty compact">Loading tool effects...</div> : null}
			{error ? <div className="error">{error}</div> : null}
			{!loading && !error && effects.length === 0 ? <div className="empty compact">No unresolved tool effects remain.</div> : null}
			{effects.map((effect) => (
				<ToolEffectForm
					effect={effect}
					key={effect.idempotency_key}
					onApplied={async () => { await load(); await onChanged(); }}
					runId={runId}
				/>
			))}
		</section>
	);
}

function ToolEffectForm({ effect, runId, onApplied }: { effect: ToolEffect; runId: string; onApplied: () => Promise<void> }) {
	const availableActions = Array.isArray(effect.available_actions) ? effect.available_actions : [];
	const [action, setAction] = useState<ToolEffectReconciliationAction | "">(availableActions[0] ?? "");
	const [actor, setActor] = useState("");
	const [reason, setReason] = useState("");
	const [result, setResult] = useState("");
	const [error, setError] = useState("");
	const [submitting, setSubmitting] = useState(false);

	async function submit(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		if (!action || !actor.trim() || !reason.trim()) return;
		let parsedResult: unknown;
		if (action === "confirm_committed") {
			try {
				parsedResult = JSON.parse(result);
			} catch {
				setError("Committed result must be valid JSON.");
				return;
			}
		}
		setSubmitting(true);
		setError("");
		try {
			await reconcileToolEffect(runId, effect.idempotency_key, {
				command_id: crypto.randomUUID(),
				action,
				expected_version: effect.version,
				actor: actor.trim(),
				reason: reason.trim(),
				...(action === "confirm_committed" ? { result: parsedResult } : {})
			});
			await onApplied();
		} catch (err) {
			setError(err instanceof Error ? err.message : "Failed to reconcile tool effect");
		} finally {
			setSubmitting(false);
		}
	}

	return (
		<form className="tool-effect-form" onSubmit={submit}>
			<div className="tool-effect-identity">
				<div><strong>{effect.tool_name}</strong><code>{effect.idempotency_key}</code></div>
				<span>{effect.status}</span>
			</div>
			{effect.error ? <p className="tool-effect-error">{effect.error}</p> : null}
			{availableActions.length === 0 ? (
				<p>No reconciliation action is permitted by the current Tool policy and Binding.</p>
			) : (
				<>
					<div className="tool-effect-fields">
						<label>Action<select value={action} onChange={(event) => setAction(event.target.value as ToolEffectReconciliationAction)}>
							{availableActions.map((item) => <option key={item} value={item}>{actionLabel(item)}</option>)}
						</select></label>
						<label>Actor<input value={actor} onChange={(event) => setActor(event.target.value)} placeholder="Operator identity" /></label>
						<label className="tool-effect-reason">Reason<input value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Observed external outcome" /></label>
					</div>
					{action === "confirm_committed" ? (
						<label className="tool-effect-result">Committed result<textarea value={result} onChange={(event) => setResult(event.target.value)} placeholder='{"status":"accepted"}' /></label>
					) : null}
					<div className="tool-effect-submit">
						{error ? <span className="error">{error}</span> : null}
						<button disabled={submitting || !action || !actor.trim() || !reason.trim() || (action === "confirm_committed" && !result.trim())} type="submit">
							{submitting ? "Applying..." : "Apply reconciliation"}
						</button>
					</div>
				</>
			)}
		</form>
	);
}

function evidenceLabel(kind: string) {
	return ({ run_error: "Run error", tool_effect: "Tool effect", verification: "Verification", task_blocker: "Task blocker", child_run: "Child run", parent_run: "Parent run" } as Record<string, string>)[kind] ?? kind;
}

function actionLabel(action: ToolEffectReconciliationAction) {
	return ({ confirm_committed: "Confirm committed", confirm_failed: "Confirm failed", retry_with_same_key: "Retry with same key", compensate: "Compensate" } as Record<ToolEffectReconciliationAction, string>)[action];
}
