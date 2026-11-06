package ladoqueue

// The prompts of a parts run's workers (docs/LJY-PRD/TDD.md §5.4). Each
// carries the whole approved plan, so a worker knows what the other parts do,
// and says what this worker alone does. Only the integration worker pushes
// and opens the pull request, and it asks for the evidence the step 8 checks
// read.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// planSection is the approved plan file, unchanged, under its heading, and
// where the worker reads it again after its context is summarized.
func planSection(in ladotask.RunInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Approved plan (revision %d, sha256 %s)\n\n", in.Revision.Number, shortSHA(in.Revision.SHA256))
	b.WriteString(in.PlanText)
	if !strings.HasSuffix(in.PlanText, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(planFileNote(in))
	return b.String()
}

// stepList names step numbers as "3, 4 and 7".
func stepList(steps []int) string {
	names := make([]string, len(steps))
	for i, n := range steps {
		names[i] = strconv.Itoa(n)
	}
	return joinAnd(names)
}

func pathList(paths []string) string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = "`" + p + "`"
	}
	return strings.Join(names, ", ")
}

// partPrompt is the task of a part's worker: its part of the plan only, on
// its own branch, committed and not pushed, ending with a done report. In a
// run in several repositories (#64) folders names them, as "back/": the
// branch is in each, and the worker commits in those its paths lie in.
func partPrompt(task domain.LadoTask, in ladotask.RunInput, part domain.LadoTaskPart, parts []domain.LadoTaskPart, branch string, folders []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are implementing one part of a plan the developer approved in LADO for %s: %s.\n", task.IssueRef, oneLine(task.Issue.Title))
	fmt.Fprintf(&b, "LADO split the plan into parts that separate workers do at the same time. You are part `%s` (%s).\n\n", part.PartID, oneLine(part.Title))
	b.WriteString("## Your part\n\n")
	fmt.Fprintf(&b, "- Do steps %s of the plan below, and only those. Other workers do the other parts; the integration worker does the steps no part took.\n", stepList(part.Steps))
	fmt.Fprintf(&b, "- Change only files under %s. Touch a shared file outside them only when your steps cannot be done without it, and keep that change small.\n", pathList(part.AffectedPaths))
	if len(part.DependsOn) > 0 {
		fmt.Fprintf(&b, "- The parts you depend on (%s) are already merged into your branch: build on what they did.\n", strings.Join(part.DependsOn, ", "))
	}
	if others := runningSiblings(part, parts); others != "" {
		fmt.Fprintf(&b, "- Parts %s run beside you. Do not do their steps, even when you see they are not done yet.\n", others)
	}
	if len(folders) > 1 {
		fmt.Fprintf(&b, "- You work on branch `%s`, which LADO made in each of the repository folders %s. Commit your work on that branch in each repository your paths lie in, following its commit rules, and leave the others as they are. Do not push and do not open a pull request: LADO merges your branch in each repository.\n",
			branch, codeList(folders))
	} else {
		fmt.Fprintf(&b, "- You work on branch `%s`. Commit your work there, following the repository's commit rules. Do not push it and do not open a pull request: LADO merges your branch.\n", branch)
	}
	b.WriteString("- Run the build and the tests that cover your change before you finish.\n")
	b.WriteString("- Commit on that branch before you report done: a part reported done with no commit on its branch stops the whole run, unless the plan marks the part `allow_empty`.\n")
	b.WriteString("- When your part is done and committed, run `ao report --done --note \"<one paragraph: what you changed, the files, the tests you ran, anything the integration worker must know>\"`. LADO reads that report; your session then ends.\n")
	b.WriteString("- If you cannot go on without a decision, run `ao report --needs-input --note \"<what you need>\"` and wait.\n\n")
	b.WriteString(planSection(in))
	return b.String()
}

// runningSiblings names the parts of the run that are not part and that it
// does not depend on.
func runningSiblings(part domain.LadoTaskPart, parts []domain.LadoTaskPart) string {
	var names []string
	for _, other := range parts {
		if other.PartID == part.PartID || other.State == domain.LadoPartMerged {
			continue
		}
		names = append(names, "`"+other.PartID+"`")
	}
	return joinAnd(names)
}

// mergePrompt is the task of a merge worker: merge a part's branch, at tip,
// into the integration branch's tip on its own branch, keeping both sides.
// folder is the repository folder, as "front", whose merge conflicted in a
// run in several repositories (#64), empty in a run in one.
func mergePrompt(task domain.LadoTask, in ladotask.RunInput, part domain.LadoTaskPart, branch, folder, tip string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are resolving a merge conflict in a plan the developer approved in LADO for %s: %s.\n", task.IssueRef, oneLine(task.Issue.Title))
	fmt.Fprintf(&b, "LADO runs the plan in parts and merges each finished part into the task's integration branch. Part `%s` (%s) conflicts with what is merged already.\n\n", part.PartID, oneLine(part.Title))
	b.WriteString("## Your task\n\n")
	if folder != "" {
		fmt.Fprintf(&b, "- The conflict is in the repository folder `%s/`; the part's merges in the other repositories are done. Work in `%s/` only.\n", folder, folder)
		fmt.Fprintf(&b, "- You work on branch `%s` there, which starts at that repository's integration branch tip.\n", branch)
	} else {
		fmt.Fprintf(&b, "- You work on branch `%s`, which starts at the integration branch's tip.\n", branch)
	}
	fmt.Fprintf(&b, "- Merge the part's commit `%s` (branch `%s`) into it: `git merge --no-ff %s`.\n", tip, part.Branch, tip)
	b.WriteString("- Resolve every conflict so that both sides keep what they did: each side's steps of the plan must still be done after the merge. Do not drop either side's change to make the conflict go away.\n")
	b.WriteString("- Run the build and the tests that cover the conflicting files, fix what the merge broke, and commit the merge on your branch, following the repository's commit rules.\n")
	b.WriteString("- Do not push and do not open a pull request. Do not do any other step of the plan.\n")
	b.WriteString("- When the merge is committed, run `ao report --done --note \"<one paragraph: the conflicts and how you resolved them>\"`. LADO then moves the integration branch to your commit.\n")
	b.WriteString("- If resolving needs a decision the plan does not make, run `ao report --needs-input --note \"<the decision>\"` and wait.\n\n")
	if part.Summary != "" {
		fmt.Fprintf(&b, "The part's worker reported:\n\n> %s\n\n", oneLine(part.Summary))
	}
	b.WriteString(planSection(in))
	return b.String()
}

// integrationPrompt is the task of the integration worker: review the merged
// parts for contradictions and fix them, do the steps no part took, run every
// check, push the integration branch and open the task's pull request, then
// write the evidence. A run in several repositories (#64, repos) has the
// integration branch in each: the worker opens one pull request in each
// repository the parts changed, in the plan's order.
func integrationPrompt(task domain.LadoTask, in ladotask.RunInput, checks []domain.LadoCheckCommand, run domain.LadoPartRun, parts []domain.LadoTaskPart,
	repos []domain.LadoPartRunRepo) string {
	example := ""
	if id, err := domain.ParseLadoIssueRef(task.IssueRef); err == nil {
		if n := domain.LadoIssueNumber(id); n > 0 {
			example = fmt.Sprintf(" (for example \"Closes #%d\")", n)
		}
	}
	plan, _ := domain.ParseLadoPlan([]byte(in.PlanText), domain.LadoPlanExpectations{})
	var b strings.Builder
	fmt.Fprintf(&b, "You are the integration worker of a plan the developer approved in LADO for %s: %s.\n", task.IssueRef, oneLine(task.Issue.Title))
	multi := len(repos) > 1
	if multi {
		fmt.Fprintf(&b, "LADO ran the plan's parts in separate workers and merged each into the integration branch `%s`, which you work on. The branch is in each repository of the plan, in this order:\n\n", run.Branch)
		for _, repo := range repos {
			fmt.Fprintf(&b, "- `%s/`: tip `%s`", strings.Trim(repo.Relative, "/"), repo.HeadSHA)
			if !repo.Changed() {
				b.WriteString(", which the parts did not change")
			}
			b.WriteString(".\n")
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "LADO ran the plan's parts in separate workers and merged each into the integration branch `%s`, which you work on. Its tip is `%s`.\n\n", run.Branch, run.HeadSHA)
	}
	b.WriteString("## The merged parts\n\n")
	for _, part := range parts {
		fmt.Fprintf(&b, "- `%s` (%s): steps %s, paths %s.", part.PartID, oneLine(part.Title), stepList(part.Steps), pathList(part.AffectedPaths))
		if part.Summary != "" {
			fmt.Fprintf(&b, " Its worker reported: %s", oneLine(part.Summary))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## 1. Review the parts against each other first\n\n")
	b.WriteString("The parts were written at the same time by workers that did not see each other's work. Text conflicts are resolved already, but the meaning can still clash. Before anything else, compare the merged result with the whole plan and look for:\n\n")
	b.WriteString("- the same thing implemented twice, or in two different ways;\n")
	b.WriteString("- names, APIs, constants or settings that do not match between parts;\n")
	b.WriteString("- duplicate helpers in the same package;\n")
	b.WriteString("- one part using what another part changed in its old shape;\n")
	b.WriteString("- documents that disagree with the code or with each other;\n")
	b.WriteString("- changes outside a part's paths that the plan did not ask for.\n\n")
	b.WriteString("Fix every contradiction you find and commit the fixes. Write what you fixed and why in the pull request description and in the work log's \"통합 검수\" section when the plan keeps one. ")
	b.WriteString("When a contradiction needs a decision the plan does not make (such as which part's design is right), do not fix it: run `ao report --needs-input --note \"<the contradiction and the choices>\"` and wait for the developer.\n\n")
	b.WriteString("## 2. Finish the plan\n\n")
	if rest := plan.IntegrationSteps(); len(rest) > 0 {
		fmt.Fprintf(&b, "Then do steps %s of the plan, which no part took. ", stepList(rest))
	} else {
		b.WriteString("Every step of the plan belonged to a part. ")
	}
	refspec := "HEAD:refs/heads/" + run.Branch
	if multi {
		b.WriteString(integrationPRs(run, repos, example))
		b.WriteString(changeRequestParagraph(task, changeRequest{targetPerRepo: true, refspec: refspec, multiRepo: true}))
		b.WriteString(reviewThreadsParagraph(task))
	} else {
		fmt.Fprintf(&b, "Run the plan's checks on the whole result, push `%s` and open the task's pull request against the plan's base_ref. Link the issue in its description%s.\n\n", run.Branch, example)
		b.WriteString(sessionBranchParagraph(sessionBranch{name: run.Branch}))
		b.WriteString(changeRequestParagraph(task, changeRequest{target: run.BaseBranch, refspec: refspec}))
		b.WriteString(reviewThreadsParagraph(task))
	}
	b.WriteString("\n")
	b.WriteString(planSection(in))
	b.WriteString("\n## Verification and evidence\n\n")
	b.WriteString(verificationParagraph(in.Snapshot, checks))
	b.WriteString("\n\n" + evidenceForm)
	return b.String()
}

// integrationPRs asks the integration worker of a run in several
// repositories (#64) for one pull request in each repository the parts
// changed, in the plan's order, which is the order they merge in, and for
// none in a repository they did not change, all from the integration branch
// (#71).
func integrationPRs(run domain.LadoPartRun, repos []domain.LadoPartRunRepo, example string) string {
	var changed, unchanged []string
	for _, repo := range repos {
		folder := strings.Trim(repo.Relative, "/") + "/"
		if repo.Changed() {
			base := repo.BaseBranch
			if base == "" {
				base = "its default branch"
			} else {
				base = "`" + base + "`"
			}
			changed = append(changed, fmt.Sprintf("`%s` against %s", folder, base))
		} else {
			unchanged = append(unchanged, folder)
		}
	}
	var b strings.Builder
	if len(changed) > 0 {
		fmt.Fprintf(&b, "Run the plan's checks on the whole result. Then, in each repository below, push `%s` and open one pull request in it, in this order, which is the order they merge in: %s. ",
			run.Branch, strings.Join(changed, ", then "))
	} else {
		fmt.Fprintf(&b, "Run the plan's checks on the whole result. The parts changed none of the repositories: when your own fixes change one, push `%s` there and open one pull request in it. ", run.Branch)
	}
	if len(unchanged) > 0 {
		fmt.Fprintf(&b, "Open no pull request in %s, which the parts did not change, unless your own fixes change it; then open its pull request in the plan's order too. ", codeList(unchanged))
	}
	fmt.Fprintf(&b, "Link the issue in each description%s. LADO verifies each pull request, and the developer approves them together; open every pull request before you report done.\n\n", example)
	b.WriteString(sessionBranchParagraph(sessionBranch{name: run.Branch, multiRepo: true}))
	return b.String()
}

// codeList names items as code, "`a`, `b` and `c`".
func codeList(items []string) string {
	names := make([]string, len(items))
	for i, item := range items {
		names[i] = "`" + item + "`"
	}
	return joinAnd(names)
}
