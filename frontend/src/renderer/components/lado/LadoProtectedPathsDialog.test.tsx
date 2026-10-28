import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../../lib/bridge";
import type { LadoRule } from "../../lib/lado";
import { LadoProtectedPathsDialog } from "./LadoProtectedPathsDialog";

const GLOBS = [".github/**", "**/CLAUDE.md"];

const pathsRule = {
	id: "paths.protected",
	name: "Protected paths",
	builtin: true,
	protected: true,
	archived: false,
	category: "git",
	instruction: "Do not change protected paths unless the approved plan names them.",
	enforcement: ["validator", "prompt"],
	strength: "detects",
	conflictsWith: [],
	global: "on",
	overrides: [],
	version: 2,
	versionId: "paths.protected-v2",
	sha256: "b".repeat(64),
	createdAt: "2026-10-27T09:00:00Z",
	updatedAt: "2026-10-27T10:00:00Z",
	paths: { globs: [".github/**", "docs/private"] },
} satisfies LadoRule;

// The desktop app's confirmation of a rule change (lib/lado.ts changeLadoProtectedPaths).
const confirmChange = () => vi.mocked(aoBridge.lado.presence.changeRule);

function Harness({ onOpenChange }: { onOpenChange: (open: boolean) => void }) {
	const [open, setOpen] = useState(true);
	return (
		<LadoProtectedPathsDialog
			open={open}
			globs={GLOBS}
			onOpenChange={(next) => {
				onOpenChange(next);
				setOpen(next);
			}}
		/>
	);
}

function renderDialog() {
	const onOpenChange = vi.fn();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<Harness onOpenChange={onOpenChange} />
		</QueryClientProvider>,
	);
	return { onOpenChange };
}

const field = () => screen.getByRole("textbox", { name: "Paths and patterns" });
const saveButton = () => screen.getByRole("button", { name: "Save paths" });

async function replaceText(user: ReturnType<typeof userEvent.setup>, text: string) {
	await user.clear(field());
	// Pasted, so brackets and line breaks arrive as typed text.
	await user.click(field());
	await user.paste(text);
}

// LADO fork: the protected paths' editor (docs/LJY-PRD/HARNESS.md §14).
describe("LadoProtectedPathsDialog", () => {
	beforeEach(() => {
		vi.resetAllMocks();
		vi.spyOn(aoBridge.lado.presence, "changeRule").mockResolvedValue({ status: "cancelled" });
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("starts from the rule's paths, one per line", async () => {
		renderDialog();

		const dialog = await screen.findByRole("dialog", { name: "Edit protected paths" });
		expect(field()).toHaveValue(".github/**\n**/CLAUDE.md");
		expect(field()).toHaveFocus();
		expect(within(dialog).getByTestId("lado-protected-path-count")).toHaveTextContent("2 paths");
		// Nothing changed yet, so there is nothing to save.
		expect(saveButton()).toBeDisabled();
	});

	it("reads one path per line, skips blank lines and names the lines LADO would refuse", async () => {
		const user = userEvent.setup();
		renderDialog();
		await screen.findByRole("dialog", { name: "Edit protected paths" });

		await replaceText(user, ".github/**\n\n   docs/private  \n/etc/hosts\n../outside\nsrc\\main.go\nC:/work\nsrc/[abc\n\n");
		expect(screen.getByTestId("lado-protected-path-count")).toHaveTextContent("7 paths");
		const problems = within(screen.getByTestId("lado-protected-path-problems")).getAllByRole("listitem");
		const invalid = "Use a relative path or pattern, without .., a leading / or a backslash.";
		expect(problems.map((item) => item.textContent)).toEqual([
			`Line 4: ${invalid}`,
			`Line 5: ${invalid}`,
			`Line 6: ${invalid}`,
			`Line 7: ${invalid}`,
			`Line 8: ${invalid}`,
		]);
		expect(field()).toHaveAttribute("aria-invalid", "true");
		expect(saveButton()).toBeDisabled();

		await replaceText(user, ".github/**\n\n   docs/private  \nsrc/[a-c]*.go\n");
		expect(screen.queryByTestId("lado-protected-path-problems")).not.toBeInTheDocument();
		expect(screen.getByTestId("lado-protected-path-count")).toHaveTextContent("3 paths");
		expect(saveButton()).toBeEnabled();

		// No path at all is not a list LADO takes.
		await user.clear(field());
		expect(screen.getByTestId("lado-protected-path-problems")).toHaveTextContent("Enter a path or a pattern.");
		expect(saveButton()).toBeDisabled();
		expect(confirmChange()).not.toHaveBeenCalled();
	});

	it("saves through the desktop app's confirmation and keeps the edits when it is cancelled", async () => {
		const user = userEvent.setup();
		const { onOpenChange } = renderDialog();
		await screen.findByRole("dialog", { name: "Edit protected paths" });

		await replaceText(user, ".github/**\n\n  docs/private \n");
		await user.click(saveButton());

		await waitFor(() => expect(confirmChange()).toHaveBeenCalledWith("paths.protected", { globs: [".github/**", "docs/private"] }));
		await waitFor(() => expect(saveButton()).toBeEnabled());
		expect(screen.getByRole("dialog", { name: "Edit protected paths" })).toBeInTheDocument();
		expect(field()).toHaveValue(".github/**\n\n  docs/private \n");
		expect(onOpenChange).not.toHaveBeenCalled();

		confirmChange().mockResolvedValue({ status: "changed", rule: pathsRule });
		await user.click(saveButton());
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(onOpenChange).toHaveBeenCalledWith(false);
		expect(confirmChange()).toHaveBeenCalledTimes(2);
	});

	it("shows the daemon's refusal on the line it names", async () => {
		const user = userEvent.setup();
		confirmChange().mockResolvedValue({
			status: "error",
			code: "LADO_PROTECTED_PATHS_INVALID",
			message: "Give 1 to 50 protected paths, each a relative path or pattern.",
			details: { index: 1, reason: "invalid" },
		});
		renderDialog();
		await screen.findByRole("dialog", { name: "Edit protected paths" });

		await replaceText(user, ".github/**\n\ndocs/private");
		await user.click(saveButton());

		expect(await screen.findByRole("alert")).toHaveTextContent(
			/^Line 3: Use a relative path or pattern, without \.\., a leading \/ or a backslash\.$/,
		);
		expect(screen.getByRole("dialog", { name: "Edit protected paths" })).toBeInTheDocument();
	});
});
