import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { LadoDesktopSettings, LadoDesktopSettingsPatch } from "../../../shared/lado-desktop";
import { aoBridge } from "../../lib/bridge";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Switch } from "../ui/switch";

type BackgroundSetting = keyof LadoDesktopSettingsPatch;

/**
 * LADO fork: Settings > General > Background (docs/LJY-PRD/TDD.md §10).
 * Desktop only: renders nothing when the bridge has no LADO desktop settings.
 */
export function LadoBackgroundSection() {
	const { t } = useTranslation();
	const [settings, setSettings] = useState<LadoDesktopSettings | null>(null);
	const [saving, setSaving] = useState(false);
	const [failed, setFailed] = useState<BackgroundSetting | null>(null);

	useEffect(() => {
		let cancelled = false;
		Promise.resolve(aoBridge.lado?.desktop?.get?.() ?? null)
			.then((next) => {
				if (!cancelled) setSettings(next);
			})
			.catch(() => undefined);
		return () => {
			cancelled = true;
		};
	}, []);

	if (!settings) return null;

	const apply = async (setting: BackgroundSetting, value: boolean) => {
		setSaving(true);
		setFailed(null);
		try {
			const next = await aoBridge.lado.desktop.set({ [setting]: value });
			if (next) setSettings(next);
		} catch {
			setFailed(setting);
		} finally {
			setSaving(false);
		}
	};

	const failure = (setting: BackgroundSetting) =>
		failed === setting ? (
			<span role="alert" className="text-error">
				{t("lado.settings.saveFailed")}
			</span>
		) : undefined;

	return (
		<SettingsSection title={t("lado.settings.background")} grouped>
			{settings.startAtLogin.supported ? (
				<SettingsRow label={t("lado.settings.startAtLogin")} description={failure("startAtLogin")}>
					<Switch
						aria-label={t("lado.settings.startAtLogin")}
						checked={settings.startAtLogin.enabled}
						disabled={saving}
						onCheckedChange={(next) => {
							void apply("startAtLogin", next);
						}}
					/>
				</SettingsRow>
			) : null}
			<SettingsRow
				label={t("lado.settings.keepRunning")}
				description={
					failure("keepRunning") ??
					(settings.keepRunning.setByEnvironment ? t("lado.settings.keepRunningSetByEnv") : undefined)
				}
			>
				<Switch
					aria-label={t("lado.settings.keepRunning")}
					checked={settings.keepRunning.enabled}
					disabled={saving || settings.keepRunning.setByEnvironment}
					onCheckedChange={(next) => {
						void apply("keepRunning", next);
					}}
				/>
			</SettingsRow>
		</SettingsSection>
	);
}
