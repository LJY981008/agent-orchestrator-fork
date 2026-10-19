// LADO fork: no PostHog project key ships with the fork, so telemetry stays off
// by default (docs/LJY-PRD/TDD.md §11). AO_TELEMETRY_POSTHOG_KEY (daemon) and
// VITE_AO_POSTHOG_KEY (renderer build) still opt in with your own project.
export const DEFAULT_POSTHOG_PROJECT_KEY = "";
export const DEFAULT_POSTHOG_HOST = "https://us.i.posthog.com";
