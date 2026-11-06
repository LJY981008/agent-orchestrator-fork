import { useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { daemonFailureMessage } from "../../lib/daemon-failure";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";

/**
 * LADO fork: restarts a healthy daemon from Settings > General > Advanced.
 * It calls the same main-process restart as the daemon failure banner; task
 * and chat sessions keep running and reconnect to the new daemon.
 */
export function DaemonRestartRow() {
	const { t } = useTranslation();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [restarting, setRestarting] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const restart = async () => {
		setConfirmOpen(false);
		setRestarting(true);
		setError(null);
		try {
			const next = await aoBridge.daemon.restart();
			if (next.state === "error" || next.state === "stopped") setError(daemonFailureMessage(next, t));
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("daemon.restartFailed"));
		} finally {
			setRestarting(false);
		}
	};

	return (
		<div className="flex w-full flex-col">
			<SettingsRow
				label={t("daemon.restart")}
				description={
					<>
						{t("settings.daemonRestart.description")}
						{error ? <span role="alert" className="block text-error">{error}</span> : null}
					</>
				}
			>
				<Button
					type="button"
					variant="footer"
					disabled={restarting}
					onClick={() => {
						setError(null);
						setConfirmOpen(true);
					}}
				>
					{restarting ? t("daemon.restarting") : t("settings.daemonRestart.action")}
				</Button>
			</SettingsRow>
			<ConfirmDialog
				open={confirmOpen}
				title={t("settings.daemonRestart.confirmTitle")}
				description={t("settings.daemonRestart.confirmDescription")}
				confirmLabel={t("settings.daemonRestart.action")}
				onConfirm={() => void restart()}
				onOpenChange={setConfirmOpen}
			/>
		</div>
	);
}
