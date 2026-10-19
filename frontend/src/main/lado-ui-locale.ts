// LADO fork: the desktop app starts in Korean (docs/LJY-PRD/TDD.md §11).
// English stays i18n's fallback and source catalog, and the language of the
// browser build and the tests, which use the shared defaults.
import {
	APP_LOCALES,
	coerceUiSettings,
	DEFAULT_UI_SETTINGS,
	type AppLocale,
	type UiSettings,
} from "../shared/ui-locale";

export const LADO_DESKTOP_LOCALE: AppLocale = "ko";

/** The desktop's UI settings until the developer changes them. */
export const LADO_DESKTOP_UI_SETTINGS: UiSettings = { ...DEFAULT_UI_SETTINGS, locale: LADO_DESKTOP_LOCALE };

/**
 * coerceUiSettings for the desktop: a stored language is kept, and a missing
 * or unknown one becomes the desktop's language instead of English.
 */
export function coerceLadoDesktopUiSettings(raw: unknown): UiSettings {
	const settings = coerceUiSettings(raw);
	const stored = typeof raw === "object" && raw !== null ? (raw as Record<string, unknown>).locale : undefined;
	const known = typeof stored === "string" && (APP_LOCALES as readonly string[]).includes(stored);
	return known ? settings : { ...settings, locale: LADO_DESKTOP_LOCALE };
}
